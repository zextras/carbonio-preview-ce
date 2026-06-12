// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// fakePDFBytes is a minimal stand-in for real PDF content.
const fakePDFBytes = "%PDF-1.4 fake"

// stubPDFSlice replaces pdfSliceFunc for the duration of a test.
func stubPDFSlice(returnData []byte, returnErr error) (restore func()) {
	prev := pdfSliceFunc
	pdfSliceFunc = func(_ []byte, _, _ int) ([]byte, error) {
		return returnData, returnErr
	}
	return func() { pdfSliceFunc = prev }
}

// stubPDFSliceRelay replaces pdfSliceRelayFunc for the duration of a test.
func stubPDFSliceRelay(returnData []byte, returnErr error) (restore func()) {
	prev := pdfSliceRelayFunc
	pdfSliceRelayFunc = func(
		_ context.Context,
		_ []byte,
		_, _ int,
		_ *http.Client,
		_ string,
	) ([]byte, error) {
		return returnData, returnErr
	}
	return func() { pdfSliceRelayFunc = prev }
}

// stubPDFRasterize replaces pdfRasterizeFunc for the duration of a test.
func stubPDFRasterize(returnData []byte, returnErr error) (restore func()) {
	prev := pdfRasterizeFunc
	pdfRasterizeFunc = func(
		_ chan struct{}, _ []byte, _, _, _ int, _, _, _ string,
	) ([]byte, error) {
		return returnData, returnErr
	}
	return func() { pdfRasterizeFunc = prev }
}

// buildPDFMux registers PDF routes on a fresh mux.
func buildPDFMux(cfg *config.Config, store *mockStore) *http.ServeMux {
	mux := http.NewServeMux()
	// Use nil relayClient and empty pdfInternalAddr so PDF worker path is used
	// (renderPDFThumbnail will call pdfRasterizeFunc directly).
	registerPDFRoutes(mux, cfg, store, nil, nil, "")
	return mux
}

// TestPDFGetPreview_Success verifies GET /{id}/{version}/ returns 200 + application/pdf.
func TestPDFGetPreview_Success(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type: got %q, want application/pdf", ct)
	}
}

// TestPDFGetPreview_Storage404 verifies 404 JSON body.
func TestPDFGetPreview_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestPDFGetPreview_StorageError verifies non-404 storage error → 422 JSON.
func TestPDFGetPreview_StorageError(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

// TestPDFGetPreview_InvalidUUID verifies that a bad UUID returns 422.
func TestPDFGetPreview_InvalidUUID(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := "/preview/pdf/not-a-uuid/1/?service_type=files"
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "id")
}

// TestPDFGetPreview_PageRange verifies that first_page and last_page are
// accepted as query params and the response is still 200.
func TestPDFGetPreview_PageRange(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files&first_page=2&last_page=5", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestPDFGetPreview_InvalidPageRange verifies first_page > last_page → 422
// with string-detail body containing NumberOfPagesNotValid.
func TestPDFGetPreview_InvalidPageRange(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files&first_page=5&last_page=2", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	// pages validation → 422 + string-detail (not array-detail)
	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.NumberOfPagesNotValid)
}

// TestPDFGetPreview_FirstPageZero verifies that first_page=0 → 422 (must be >= 1).
func TestPDFGetPreview_FirstPageZero(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files&first_page=0", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.NumberOfPagesNotValid)
}

// TestPDFGetPreview_LastPageZeroMeansAll verifies last_page=0 is accepted
// (means "all pages" per spec).
func TestPDFGetPreview_LastPageZeroMeansAll(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files&last_page=0", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestPDFGetThumbnail_Success verifies GET /{id}/{version}/{area}/thumbnail/
// returns 200 with image/jpeg content-type (using pdfRasterizeFunc stub).
func TestPDFGetThumbnail_Success(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreRaster := stubPDFRasterize([]byte("jpeg-output"), nil)
	defer restoreRaster()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
}

// TestPDFGetThumbnail_Storage404 verifies 404 JSON body on thumbnail.
func TestPDFGetThumbnail_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestPDFGetThumbnail_InvalidArea verifies bad area → 422 with loc["path","area"].
func TestPDFGetThumbnail_InvalidArea(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/badarea/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "area")
}

// TestPDFPostPreview_Success verifies POST / returns 200 + application/pdf.
func TestPDFPostPreview_Success(t *testing.T) {
	restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type: got %q, want application/pdf", ct)
	}
}

// TestPDFPostPreview_NoFile verifies that POST without a file field → 422
// (body param validation).
func TestPDFPostPreview_NoFile(t *testing.T) {
	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "file")
}

// TestPDFPostThumbnail_Success verifies POST /{area}/thumbnail/ returns 200.
func TestPDFPostThumbnail_Success(t *testing.T) {
	restoreRaster := stubPDFRasterize([]byte("jpeg-output"), nil)
	defer restoreRaster()

	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/100x200/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestPDFGetPreview_MissingServiceType verifies missing service_type → 422.
func TestPDFGetPreview_MissingServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "service_type")
}

// TestPDFGetPreview_RelayPath verifies that when a relayClient and
// pdfInternalAddr are provided, the thumbnail request is relayed to the worker.
func TestPDFGetPreview_RelayPath(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}

	// Start a fake PDF worker.
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != internalPDFRenderPath {
			t.Errorf("worker: unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("worker-jpeg")) //nolint:errcheck
	}))
	defer worker.Close()

	relayClient := &http.Client{}

	cfg := testCfg()
	mux := http.NewServeMux()
	registerPDFRoutes(mux, cfg, store, nil, relayClient, worker.URL)

	path := fmt.Sprintf("/preview/pdf/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if rec.Body.String() != "worker-jpeg" {
		t.Errorf("body: got %q, want worker-jpeg", rec.Body.String())
	}
}

// TestPDFGetPreview_UnmatchedPath verifies unrecognised paths → 404 JSON.
func TestPDFGetPreview_UnmatchedPath(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/extra/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
}

// TestPDFPostPreview_RelaySlice verifies that POST /preview/pdf/ relays slicing to
// a fake worker and returns 200 + application/pdf when the worker succeeds.
func TestPDFPostPreview_RelaySlice(t *testing.T) {
	slicedPDF := []byte("%PDF-1.4 sliced")

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != internalPDFSlicePath {
			t.Errorf("worker: unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("worker: unexpected method %q", r.Method)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		w.Write(slicedPDF) //nolint:errcheck
	}))
	defer worker.Close()

	relayClient := &http.Client{}
	cfg := testCfg()

	mux := http.NewServeMux()
	registerPDFRoutes(mux, cfg, nil, nil, relayClient, worker.URL)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (body: %q)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("Content-Type: got %q, want application/pdf", rec.Header().Get("Content-Type"))
	}
	if string(rec.Body.Bytes()) != string(slicedPDF) {
		t.Errorf("body: got %q, want %q", rec.Body.String(), slicedPDF)
	}
}

// TestPDFGetPreview_RelaySlice verifies that GET /preview/pdf/{id}/{version}/
// relays slicing to the worker and returns 200.
func TestPDFGetPreview_RelaySlice(t *testing.T) {
	slicedPDF := []byte("%PDF-1.4 sliced-get")
	store := &mockStore{blob: []byte(fakePDFBytes)}

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != internalPDFSlicePath {
			t.Errorf("worker: unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		w.Write(slicedPDF) //nolint:errcheck
	}))
	defer worker.Close()

	relayClient := &http.Client{}
	cfg := testCfg()

	mux := http.NewServeMux()
	registerPDFRoutes(mux, cfg, store, nil, relayClient, worker.URL)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (body: %q)", rec.Code, rec.Body.String())
	}
	if string(rec.Body.Bytes()) != string(slicedPDF) {
		t.Errorf("body: got %q, want %q", rec.Body.String(), slicedPDF)
	}
}

// TestPDFPostPreview_WorkerSliceError verifies that when the worker returns a
// non-200 status, the main handler returns 400 InputError.
func TestPDFPostPreview_WorkerSliceError(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer worker.Close()

	relayClient := &http.Client{}
	cfg := testCfg()

	mux := http.NewServeMux()
	registerPDFRoutes(mux, cfg, nil, nil, relayClient, worker.URL)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.InputError)
}

// TestPDFPreview_MainDoesNotCallPDFium is a regression guard: the main-role handler
// with pdfSliceFunc unstubbed (would return "PDFium pool not initialised" if called)
// must return 200 when a fake worker handles the relay — proving relay is used, not
// direct pdfium.
func TestPDFPreview_MainDoesNotCallPDFium(t *testing.T) {
	slicedPDF := []byte("%PDF-1.4 worker-slice")

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == internalPDFSlicePath {
			w.Header().Set("Content-Type", "application/pdf")
			w.WriteHeader(http.StatusOK)
			w.Write(slicedPDF) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer worker.Close()

	// Do NOT stub pdfSliceFunc — it calls render.PDFSlice which returns
	// "PDFium pool not initialised" when PDFWorkerInit was never called.
	// A 200 response proves main used the relay path, not pdfium directly.

	relayClient := &http.Client{}
	cfg := testCfg()

	mux := http.NewServeMux()
	registerPDFRoutes(mux, cfg, nil, nil, relayClient, worker.URL)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("regression: main process called pdfium directly — got %d, want 200 (body: %q)",
			rec.Code, rec.Body.String())
	}
}
