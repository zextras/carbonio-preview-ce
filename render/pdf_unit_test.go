// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// This file covers ONLY the non-pdfium branches of render/pdf.go — the pool-nil
// guard, the worker borrow loop (getPdfInstance, against a fake pool), the DPI
// helper — plus SetVipsConcurrency.
//
// The genuinely cgo-pipeline path (PDFPage1Pair: OpenDocument, page size,
// RenderPageInDPI and the libvips renditions) is NOT exercised here and stays
// covered by pdf_e2e_test.go (//go:build cgo, which boots a real multi-threaded
// PDFium worker).
// We deliberately do NOT fake a pdfium.Pdfium instance: the interface is large
// and a fake would re-implement PDFium. This is the deliberate cgo-only
// boundary; the small pdfium.Pool interface is faked only to drive the borrow loop.

package render

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pdfium "github.com/klippa-app/go-pdfium"
)

// withNilPool sets globalPdfPool to nil for the duration of a test and restores
// the previous value afterwards, so these guard tests cannot interfere with the
// e2e tests (which initialise a real pool) regardless of run order.
func withNilPool(t *testing.T) {
	t.Helper()
	prev := globalPdfPool
	globalPdfPool = nil
	t.Cleanup(func() { globalPdfPool = prev })
}

type fakePool struct {
	calls  atomic.Int32
	borrow func(ctx context.Context, call int32) (pdfium.Pdfium, error)
}

func (p *fakePool) GetInstance(time.Duration) (pdfium.Pdfium, error) {
	panic("not used: getPdfInstance borrows with a context")
}

func (p *fakePool) GetInstanceWithContext(ctx context.Context) (pdfium.Pdfium, error) {
	return p.borrow(ctx, p.calls.Add(1))
}

func (p *fakePool) Close() error { return nil }

type fakeInstance struct{ pdfium.Pdfium }

func withFakePool(t *testing.T, p *fakePool) {
	t.Helper()
	prev := globalPdfPool
	globalPdfPool = p
	t.Cleanup(func() { globalPdfPool = prev })
}

func withPdfInstanceWait(t *testing.T, d time.Duration) {
	t.Helper()
	prev := pdfInstanceWait
	pdfInstanceWait = d
	t.Cleanup(func() { pdfInstanceWait = prev })
}

func TestGetPdfInstance_RetriesAfterBorrowTimeout(t *testing.T) {
	withPdfInstanceWait(t, time.Millisecond)
	want := &fakeInstance{}
	p := &fakePool{borrow: func(ctx context.Context, call int32) (pdfium.Pdfium, error) {
		if call < 3 {
			<-ctx.Done()
			return nil, errors.New("Timeout waiting for idle object")
		}
		return want, nil
	}}
	withFakePool(t, p)

	got, err := getPdfInstance(context.Background())
	if err != nil {
		t.Fatalf("getPdfInstance: %v", err)
	}
	if got != want {
		t.Errorf("instance=%v, want the one borrowed on the third attempt", got)
	}
	if n := p.calls.Load(); n != 3 {
		t.Errorf("borrow attempts=%d, want 3", n)
	}
}

func TestGetPdfInstance_CtxDoneWhileWaiting(t *testing.T) {
	entered := make(chan struct{})
	p := &fakePool{borrow: func(ctx context.Context, _ int32) (pdfium.Pdfium, error) {
		close(entered)
		<-ctx.Done()
		return nil, errors.New("Timeout waiting for idle object")
	}}
	withFakePool(t, p)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := getPdfInstance(ctx)
		errCh <- err
	}()
	<-entered
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrRenderUnavailable) {
		t.Fatalf("err=%v, want context.Canceled and not ErrRenderUnavailable", err)
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("borrow attempts=%d, want 1 (no retry once ctx is done)", n)
	}
}

func TestGetPdfInstance_DoneCtxStillTakesFreeWorker(t *testing.T) {
	want := &fakeInstance{}
	withFakePool(t, &fakePool{borrow: func(context.Context, int32) (pdfium.Pdfium, error) {
		return want, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := getPdfInstance(ctx)
	if err != nil || got != want {
		t.Fatalf("got=%v err=%v, want the free worker", got, err)
	}
}

func TestGetPdfInstance_NonTimeoutErrorNotRetried(t *testing.T) {
	closed := errors.New("pool is closed")
	p := &fakePool{borrow: func(context.Context, int32) (pdfium.Pdfium, error) {
		return nil, closed
	}}
	withFakePool(t, p)

	_, err := getPdfInstance(context.Background())
	if !errors.Is(err, ErrRenderUnavailable) || !errors.Is(err, closed) {
		t.Fatalf("err=%v, want ErrRenderUnavailable wrapping the pool error", err)
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("borrow attempts=%d, want 1", n)
	}
}

// TestSetVipsConcurrency covers the no-op (n<=0) branch and the apply (n>0)
// branch. The apply branch calls into libvips, which TestMain has already
// initialised, so it is safe to invoke here.
func TestSetVipsConcurrency(t *testing.T) {
	// No-op branch: must not panic.
	SetVipsConcurrency(0)
	SetVipsConcurrency(-1)
	// Apply branch: must not panic (libvips is initialised by TestMain).
	SetVipsConcurrency(2)
}

func TestPDFPage1Pair_PoolNotInitialised(t *testing.T) {
	withNilPool(t)
	_, err := PDFPage1Pair(context.Background(), []byte("%PDF-1.4"))
	if err == nil || !strings.Contains(err.Error(), "pool not initialised") {
		t.Fatalf("err=%v, want pool not initialised", err)
	}
}

func TestPDFPage1Pair_PoolUnavailable(t *testing.T) {
	withFakePool(t, &fakePool{borrow: func(context.Context, int32) (pdfium.Pdfium, error) {
		return nil, errors.New("worker failed to start")
	}})
	if _, err := PDFPage1Pair(context.Background(), []byte("%PDF-1.4")); !errors.Is(err, ErrRenderUnavailable) {
		t.Fatalf("err=%v, want ErrRenderUnavailable", err)
	}
}

func TestPDFPage1DPI(t *testing.T) {
	if dpi, err := pdfPage1DPI(595, 842); err != nil || dpi != 110 {
		t.Fatalf("A4 portrait dpi=%d err=%v, want 110 (ceil(1280*72/842))", dpi, err)
	}
	if dpi, err := pdfPage1DPI(842, 595); err != nil || dpi != 110 {
		t.Fatalf("A4 landscape dpi=%d err=%v, want 110", dpi, err)
	}
	if dpi, err := pdfPage1DPI(10, 10); err != nil || dpi != 9216 {
		t.Fatalf("tiny page dpi=%d err=%v, want 9216", dpi, err)
	}
	for _, sz := range [][2]float64{{0, 0}, {0, 100}, {-1, 5}} {
		if _, err := pdfPage1DPI(sz[0], sz[1]); err == nil {
			t.Errorf("size %v: want error", sz)
		}
	}
}
