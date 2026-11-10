// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// This file covers ONLY the non-pdfium branches of render/pdf.go — the guards
// that run before any PDFium call (empty-data guard, pool-nil guard), the
// worker borrow loop (getPdfInstance, against a fake pool) — plus the pure-Go
// helpers QualityToInt, SetImageMinRes and SetVipsConcurrency.
//
// The genuinely cgo-pipeline paths are NOT exercised here and stay covered by
// pdf_e2e_test.go (//go:build cgo, which boots a real single/multi-threaded
// PDFium worker). Those e2e-only branches are:
//   - PDFSlice: GetInstance success, OpenDocument, FPDF_GetPageCount, the page-
//     range arithmetic and whole-doc shortcut (pdf.go:241-242),
//     FPDF_CreateNewDocument / FPDF_ImportPagesByIndex / FPDF_SaveAsCopy.
//   - PDFRasterize: GetInstance success, OpenDocument, RenderPageInDPI, the
//     libvips RGBA render pipeline and rounded-mask application (pdf.go:120-172).
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

// TestPDFSlice_EmptyData hits the empty-data guard, which runs before the
// pool-nil guard (so it fires even with a nil pool and no worker).
func TestPDFSlice_EmptyData(t *testing.T) {
	withNilPool(t)
	_, err := PDFSlice(context.Background(), nil, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "empty PDF data") {
		t.Fatalf("want empty-data error, got %v", err)
	}
}

// TestPDFSlice_PoolNotInitialised passes non-empty data (to clear the empty-data
// guard) and a nil pool, so it hits the pool-nil guard.
func TestPDFSlice_PoolNotInitialised(t *testing.T) {
	withNilPool(t)
	_, err := PDFSlice(context.Background(), []byte("%PDF-1.4 not really"), 1, 0)
	if err == nil || !strings.Contains(err.Error(), "pool not initialised") {
		t.Fatalf("want pool-nil error, got %v", err)
	}
}

// TestPDFRasterize_PoolNotInitialised: PDFRasterize has no empty-data guard, so
// the first guard reached with a nil pool is the pool-nil guard.
func TestPDFRasterize_PoolNotInitialised(t *testing.T) {
	withNilPool(t)
	_, err := PDFRasterize(context.Background(), []byte("data"), 0, 100, 100, "jpeg", "medium", "rectangular")
	if err == nil || !strings.Contains(err.Error(), "pool not initialised") {
		t.Fatalf("want pool-nil error, got %v", err)
	}
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

func TestPDFSliceAndRasterize_PoolUnavailable(t *testing.T) {
	withFakePool(t, &fakePool{borrow: func(context.Context, int32) (pdfium.Pdfium, error) {
		return nil, errors.New("worker failed to start")
	}})

	if _, err := PDFSlice(context.Background(), []byte("%PDF"), 1, 0); !errors.Is(err, ErrRenderUnavailable) {
		t.Errorf("PDFSlice err=%v, want ErrRenderUnavailable", err)
	}
	if _, err := PDFRasterize(context.Background(), []byte("%PDF"), 0, 10, 10, "jpeg", "medium", "rectangular"); !errors.Is(err, ErrRenderUnavailable) {
		t.Errorf("PDFRasterize err=%v, want ErrRenderUnavailable", err)
	}
}

// TestQualityToInt covers every named level plus the default arm.
func TestQualityToInt(t *testing.T) {
	cases := map[string]int{
		"lowest": 5, "low": 15, "medium": 50, "high": 80, "highest": 95,
		"": 50, "garbage": 50, // default arm
	}
	for in, want := range cases {
		if got := QualityToInt(in); got != want {
			t.Errorf("QualityToInt(%q)=%d, want %d", in, got, want)
		}
	}
}

// TestSetImageMinRes covers both the positive (applied) and non-positive
// (no-op) branches of SetImageMinRes, restoring the global afterwards.
func TestSetImageMinRes(t *testing.T) {
	prev := ImageMinRes
	t.Cleanup(func() { ImageMinRes = prev })

	SetImageMinRes(123)
	if ImageMinRes != 123 {
		t.Errorf("ImageMinRes=%d, want 123", ImageMinRes)
	}
	// Non-positive is a no-op: the value must stay at 123.
	SetImageMinRes(0)
	if ImageMinRes != 123 {
		t.Errorf("after SetImageMinRes(0) ImageMinRes=%d, want 123 (no-op)", ImageMinRes)
	}
	SetImageMinRes(-5)
	if ImageMinRes != 123 {
		t.Errorf("after SetImageMinRes(-5) ImageMinRes=%d, want 123 (no-op)", ImageMinRes)
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
