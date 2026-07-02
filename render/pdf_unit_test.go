// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// This file covers ONLY the non-pdfium branches of render/pdf.go — the guards
// that run before any PDFium call (semaphore gate, empty-data guard, pool-nil
// guard) — plus the pure-Go helpers QualityToInt, SetImageMinRes,
// SetVipsConcurrency and BuildSemaphore.
//
// The genuinely cgo-pipeline paths are NOT exercised here and stay covered by
// pdf_e2e_test.go (//go:build cgo, which boots a real single/multi-threaded
// PDFium worker). Those e2e-only branches are:
//   - PDFSlice: GetInstance success, OpenDocument, FPDF_GetPageCount, the page-
//     range arithmetic and whole-doc shortcut (pdf.go:241-242),
//     FPDF_CreateNewDocument / FPDF_ImportPagesByIndex / FPDF_SaveAsCopy.
//   - PDFRasterize: GetInstance success, OpenDocument, RenderPageInDPI, the
//     libvips RGBA render pipeline and rounded-mask application (pdf.go:120-172).
// We deliberately do NOT fake pdfium.Pool: the interface is large and a fake
// would re-implement PDFium. This is the deliberate cgo-only boundary.

package render

import (
	"strings"
	"testing"
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
	_, err := PDFSlice(nil, nil, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "empty PDF data") {
		t.Fatalf("want empty-data error, got %v", err)
	}
}

// TestPDFSlice_PoolNotInitialised passes non-empty data (to clear the empty-data
// guard) and a nil pool, so it hits the pool-nil guard.
func TestPDFSlice_PoolNotInitialised(t *testing.T) {
	withNilPool(t)
	_, err := PDFSlice(nil, []byte("%PDF-1.4 not really"), 1, 0)
	if err == nil || !strings.Contains(err.Error(), "pool not initialised") {
		t.Fatalf("want pool-nil error, got %v", err)
	}
}

// TestPDFSlice_SemaphoreGate exercises the semaphore acquire/release path
// (pdf.go:190-193) with a buffered semaphore that returns a slot immediately;
// it still hits the empty-data guard so no worker is needed.
func TestPDFSlice_SemaphoreGate(t *testing.T) {
	withNilPool(t)
	sem := BuildSemaphore(1)
	_, err := PDFSlice(sem, nil, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "empty PDF data") {
		t.Fatalf("want empty-data error, got %v", err)
	}
	// The slot must have been released (deferred <-sem), so the channel is empty
	// again and a fresh acquire would not block.
	select {
	case sem <- struct{}{}:
		<-sem
	default:
		t.Error("semaphore slot was not released by PDFSlice")
	}
}

// TestPDFRasterize_PoolNotInitialised: PDFRasterize has no empty-data guard, so
// the first guard reached with a nil pool is the pool-nil guard.
func TestPDFRasterize_PoolNotInitialised(t *testing.T) {
	withNilPool(t)
	_, err := PDFRasterize(nil, []byte("data"), 0, 100, 100, "jpeg", "medium", "rectangular")
	if err == nil || !strings.Contains(err.Error(), "pool not initialised") {
		t.Fatalf("want pool-nil error, got %v", err)
	}
}

// TestPDFRasterize_SemaphoreGate exercises the semaphore acquire/release path
// (pdf.go:103-106); it still hits the pool-nil guard so no worker is needed.
func TestPDFRasterize_SemaphoreGate(t *testing.T) {
	withNilPool(t)
	sem := BuildSemaphore(1)
	_, err := PDFRasterize(sem, []byte("data"), 0, 100, 100, "jpeg", "medium", "rectangular")
	if err == nil || !strings.Contains(err.Error(), "pool not initialised") {
		t.Fatalf("want pool-nil error, got %v", err)
	}
	select {
	case sem <- struct{}{}:
		<-sem
	default:
		t.Error("semaphore slot was not released by PDFRasterize")
	}
}

// TestQualityToInt covers every named level plus the default arm.
func TestQualityToInt(t *testing.T) {
	cases := map[string]int{
		"lowest": 0, "low": 15, "medium": 50, "high": 80, "highest": 95,
		"": 50, "garbage": 50, // default arm
	}
	for in, want := range cases {
		if got := QualityToInt(in); got != want {
			t.Errorf("QualityToInt(%q)=%d, want %d", in, got, want)
		}
	}
}

// TestBuildSemaphore verifies the channel capacity equals n.
func TestBuildSemaphore(t *testing.T) {
	sem := BuildSemaphore(3)
	if cap(sem) != 3 {
		t.Errorf("cap=%d, want 3", cap(sem))
	}
	if len(sem) != 0 {
		t.Errorf("len=%d, want 0 (empty)", len(sem))
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
