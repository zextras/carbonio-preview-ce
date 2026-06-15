// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// fakePDFBytes is a minimal stand-in for real PDF content.
const fakePDFBytes = "%PDF-1.4 fake"

// stubPDFSlice replaces pdfSliceFunc for the duration of a test.
func stubPDFSlice(returnData []byte, returnErr error) (restore func()) {
	prev := pdfSliceFunc
	pdfSliceFunc = func(_ chan struct{}, _ []byte, _, _ int) ([]byte, error) {
		return returnData, returnErr
	}
	return func() { pdfSliceFunc = prev }
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
	registerPDFRoutes(mux, cfg, store, nil, nil)
	return mux
}

// TestPDFGetPreview_Success verifies GET /{id}/{version}/ returns 200 + application/pdf.
func TestPDFGetPreview_Success(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreSlice := stubPDFSlice([]byte(fakePDFBytes), nil)
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
	restoreSlice := stubPDFSlice([]byte(fakePDFBytes), nil)
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
	restoreSlice := stubPDFSlice([]byte(fakePDFBytes), nil)
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
	restoreSlice := stubPDFSlice([]byte(fakePDFBytes), nil)
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

// TestPDFGetPreview_UnmatchedPath verifies unrecognised paths → 404 JSON.
func TestPDFGetPreview_UnmatchedPath(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/extra/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
}

// TestPDFGetPreview_PoolUnavailable_503 verifies that ErrRenderUnavailable from
// PDFSlice → HTTP 503 with the standard detail message.
// This is a deliberate divergence from the old Python service (which had no
// pool-timeout concept); we return 503 to distinguish transient capacity
// problems from permanent document-level errors (400).
func TestPDFGetPreview_PoolUnavailable_503(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreSlice := stubPDFSlice(nil, render.ErrRenderUnavailable)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
}

// TestPDFPostPreview_PoolUnavailable_503 verifies POST / → 503 on pool exhaustion.
func TestPDFPostPreview_PoolUnavailable_503(t *testing.T) {
	restoreSlice := stubPDFSlice(nil, render.ErrRenderUnavailable)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
}

// TestPDFGetThumbnail_PoolUnavailable_503 verifies that ErrRenderUnavailable
// from PDFRasterize → HTTP 503 on the thumbnail endpoint.
func TestPDFGetThumbnail_PoolUnavailable_503(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreRaster := stubPDFRasterize(nil, render.ErrRenderUnavailable)
	defer restoreRaster()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
}

// TestPDFGetPreview_RenderError_400 verifies that a genuine (non-pool) PDFSlice
// error → 400 with the InputError detail (distinct from the 503 pool arm).
func TestPDFGetPreview_RenderError_400(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreSlice := stubPDFSlice(nil, errors.New("corrupt pdf"))
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.InputError)
}

// TestPDFPostPreview_InvalidPages_422 covers the parsePages error arm of
// pdfPostPreview (first_page > last_page → 422 string-detail).
func TestPDFPostPreview_InvalidPages_422(t *testing.T) {
	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/?first_page=5&last_page=2", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.NumberOfPagesNotValid)
}

// TestPDFPostPreview_RenderError_400 verifies the POST preview render-error arm.
func TestPDFPostPreview_RenderError_400(t *testing.T) {
	restoreSlice := stubPDFSlice(nil, errors.New("corrupt pdf"))
	defer restoreSlice()

	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.InputError)
}

// TestPDFGetThumbnail_RenderError_400 verifies that a genuine (non-pool)
// PDFRasterize error → 400 via renderPDFThumbnail.
func TestPDFGetThumbnail_RenderError_400(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	restoreRaster := stubPDFRasterize(nil, errors.New("bad page"))
	defer restoreRaster()

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.InputError)
}

// TestPDFPostThumbnail_InvalidQueryParams covers the query-param validation arms
// of pdfPostThumbnail (shape, quality, output_format → 422) and the area arm.
func TestPDFPostThumbnail_InvalidQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		param string
	}{
		{"bad area", "/preview/pdf/badarea/thumbnail/", "area"},
		{"bad shape", "/preview/pdf/100x100/thumbnail/?shape=hexagonal", "shape"},
		{"bad quality", "/preview/pdf/100x100/thumbnail/?quality=extreme", "quality"},
		{"bad output_format", "/preview/pdf/100x100/thumbnail/?output_format=tiff", "output_format"},
	}
	cfg := testCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := buildPDFMux(cfg, nil)

			body, ct := buildMultipart(t, "file", "x.pdf", []byte(fakePDFBytes))
			req := httptest.NewRequest(http.MethodPost, tt.path, body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			assertValidationError(t, rec, tt.param)
		})
	}
}

// TestPDFPostThumbnail_NotMultipart_422 covers the multipart parse-error arm of
// pdfPostThumbnail.
func TestPDFPostThumbnail_NotMultipart_422(t *testing.T) {
	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/100x200/thumbnail/", strings.NewReader("not-multipart"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "file")
}

// TestPDFPostThumbnail_PoolUnavailable_503 verifies POST thumbnail → 503 on pool exhaustion.
func TestPDFPostThumbnail_PoolUnavailable_503(t *testing.T) {
	restoreRaster := stubPDFRasterize(nil, render.ErrRenderUnavailable)
	defer restoreRaster()

	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/100x200/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
}
