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
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// stubCollaboraConvert replaces collaboraConvertFunc for the duration of a test.
func stubCollaboraConvert(returnData []byte, returnErr error) (restore func()) {
	prev := collaboraConvertFunc
	collaboraConvertFunc = func(
		_ context.Context,
		_ []byte,
		_ string,
		_ string,
		_ time.Duration,
	) ([]byte, error) {
		return returnData, returnErr
	}
	return func() { collaboraConvertFunc = prev }
}

// buildDocMux registers document routes on a fresh mux.
// If relayClient / pdfInternalAddr are empty, PDF rasterisation uses pdfRasterizeFunc.
func buildDocMux(cfg *config.Config, store *mockStore) *http.ServeMux {
	mux := http.NewServeMux()
	registerDocumentRoutes(mux, cfg, store, nil, nil, "")
	return mux
}

// docDisabledCfg returns a config where both document features are disabled.
func docDisabledCfg() *config.Config {
	c := testCfg()
	c.ServiceEnableDocumentPreview = false
	c.ServiceEnableDocumentThumbnail = false
	return c
}

// docPreviewDisabledCfg returns a config where document preview is disabled.
func docPreviewDisabledCfg() *config.Config {
	c := testCfg()
	c.ServiceEnableDocumentPreview = false
	c.ServiceEnableDocumentThumbnail = true
	return c
}

// docThumbnailDisabledCfg returns a config where document thumbnail is disabled.
func docThumbnailDisabledCfg() *config.Config {
	c := testCfg()
	c.ServiceEnableDocumentPreview = true
	c.ServiceEnableDocumentThumbnail = false
	return c
}

// TestDocGetPreview_DisabledByFlag verifies that GET /{id}/{version}/ returns
// 400 with DOCUMENT_PREVIEW_NOT_ENABLED_ERROR when the flag is false.
func TestDocGetPreview_DisabledByFlag(t *testing.T) {
	store := &mockStore{}
	cfg := docPreviewDisabledCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
}

// TestDocPostPreview_DisabledByFlag verifies that POST / returns 400 when
// document preview is disabled.
func TestDocPostPreview_DisabledByFlag(t *testing.T) {
	cfg := docPreviewDisabledCfg()
	mux := buildDocMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.docx", []byte("doc-bytes"))
	req := httptest.NewRequest(http.MethodPost, "/preview/document/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
}

// TestDocGetThumbnail_DisabledByFlag verifies that GET /{id}/{version}/{area}/thumbnail/
// returns 400 with DOCUMENT_THUMBNAIL_NOT_ENABLED_ERROR when the flag is false.
func TestDocGetThumbnail_DisabledByFlag(t *testing.T) {
	store := &mockStore{}
	cfg := docThumbnailDisabledCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.DocumentThumbnailDisabled)
}

// TestDocPostThumbnail_DisabledByFlag verifies that POST /{area}/thumbnail/
// returns 400 when document thumbnail is disabled.
func TestDocPostThumbnail_DisabledByFlag(t *testing.T) {
	cfg := docThumbnailDisabledCfg()
	mux := buildDocMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "doc.docx", []byte("doc-bytes"))
	req := httptest.NewRequest(http.MethodPost, "/preview/document/100x200/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.DocumentThumbnailDisabled)
}

// TestDocGetPreview_BothDisabled verifies both disabled → 400 on each endpoint.
func TestDocGetPreview_BothDisabled(t *testing.T) {
	cfg := docDisabledCfg()
	store := &mockStore{}
	mux := buildDocMux(cfg, store)

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "preview GET",
			path: fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID),
			want: config.Msg.DocumentPreviewDisabled,
		},
		{
			name: "thumbnail GET",
			path: fmt.Sprintf("/preview/document/%s/1/100x200/thumbnail/?service_type=files", validUUID),
			want: config.Msg.DocumentThumbnailDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(mux, http.MethodGet, tt.path)
			assertStringDetail(t, rec, http.StatusBadRequest, tt.want)
		})
	}
}

// TestDocGetPreview_HappyPath verifies GET /{id}/{version}/ returns 200 +
// application/pdf when Collabora and PDFSlice succeed.
func TestDocGetPreview_HappyPath(t *testing.T) {
	store := &mockStore{blob: []byte("docx-bytes")}
	restoreCollab := stubCollaboraConvert([]byte(fakePDFBytes), nil)
	defer restoreCollab()
	restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (body: %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type: got %q, want application/pdf", ct)
	}
}

// TestDocGetPreview_Storage404 verifies that storage ErrNotFound → 404 JSON.
func TestDocGetPreview_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}
	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestDocGetPreview_StorageError verifies that non-404 storage error → 422 JSON.
func TestDocGetPreview_StorageError(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}
	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

// TestDocGetPreview_CollaboraFailure verifies that a Collabora conversion error → 502 JSON.
func TestDocGetPreview_CollaboraFailure(t *testing.T) {
	store := &mockStore{blob: []byte("docx-bytes")}
	restoreCollab := stubCollaboraConvert(nil, fmt.Errorf("collabora unavailable"))
	defer restoreCollab()

	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadGateway, config.Msg.StorageUnavailable)
}

// TestDocGetPreview_InvalidUUID verifies bad UUID → 422 validation.
func TestDocGetPreview_InvalidUUID(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := "/preview/document/not-a-uuid/1/?service_type=files"
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "id")
}

// TestDocGetPreview_LangTag verifies that lang_tag is accepted and the
// request succeeds (default en-US and custom value).
func TestDocGetPreview_LangTag(t *testing.T) {
	tests := []struct {
		name    string
		langTag string
	}{
		{"default (no param)", ""},
		{"explicit en-US", "en-US"},
		{"it-IT", "it-IT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{blob: []byte("doc")}
			restoreCollab := stubCollaboraConvert([]byte(fakePDFBytes), nil)
			defer restoreCollab()
			restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
			defer restoreSlice()

			cfg := testCfg()
			mux := buildDocMux(cfg, store)

			path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
			if tt.langTag != "" {
				path += "&lang_tag=" + tt.langTag
			}
			rec := doRequest(mux, http.MethodGet, path)

			if rec.Code != http.StatusOK {
				t.Errorf("lang_tag=%q: status %d, want 200", tt.langTag, rec.Code)
			}
		})
	}
}

// TestDocPostPreview_HappyPath verifies POST / returns 200 + application/pdf.
func TestDocPostPreview_HappyPath(t *testing.T) {
	restoreCollab := stubCollaboraConvert([]byte(fakePDFBytes), nil)
	defer restoreCollab()
	restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
	defer restoreSlice()

	cfg := testCfg()
	mux := buildDocMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "test.docx", []byte("docx-bytes"))
	req := httptest.NewRequest(http.MethodPost, "/preview/document/", body)
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

// TestDocPostPreview_NoFile verifies POST without a file field → 422 (body param).
func TestDocPostPreview_NoFile(t *testing.T) {
	cfg := testCfg()
	mux := buildDocMux(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/document/", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "file")
}

// TestDocGetThumbnail_HappyPath verifies GET /{id}/{version}/{area}/thumbnail/
// returns 200 + image/jpeg when Collabora and PDF rasterize succeed.
func TestDocGetThumbnail_HappyPath(t *testing.T) {
	store := &mockStore{blob: []byte("docx-bytes")}
	restoreCollab := stubCollaboraConvert([]byte(fakePDFBytes), nil)
	defer restoreCollab()
	restoreRaster := stubPDFRasterize([]byte("jpeg-output"), nil)
	defer restoreRaster()

	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (body: %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
}

// TestDocGetThumbnail_Storage404 verifies 404 JSON body.
func TestDocGetThumbnail_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}
	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestDocGetThumbnail_InvalidArea verifies bad area → 422.
func TestDocGetThumbnail_InvalidArea(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/badarea/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "area")
}

// TestDocGetPreview_PageRangeValidation tests page range edge cases for document endpoints.
func TestDocGetPreview_PageRangeValidation(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"valid first=1 last=0", "first_page=1&last_page=0", http.StatusOK},
		{"valid first=2 last=5", "first_page=2&last_page=5", http.StatusOK},
		// page errors → 422 string-detail
		{"invalid first=0", "first_page=0", http.StatusUnprocessableEntity},
		{"invalid first>last", "first_page=5&last_page=3", http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{blob: []byte("doc")}
			restoreCollab := stubCollaboraConvert([]byte(fakePDFBytes), nil)
			defer restoreCollab()
			restoreSlice := stubPDFSliceRelay([]byte(fakePDFBytes), nil)
			defer restoreSlice()

			cfg := testCfg()
			mux := buildDocMux(cfg, store)

			path := fmt.Sprintf("/preview/document/%s/1/?service_type=files&%s", validUUID, tt.query)
			rec := doRequest(mux, http.MethodGet, path)
			if rec.Code != tt.wantStatus {
				t.Errorf("query=%q: status %d, want %d (body: %q)", tt.query, rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestDocMethodNotAllowed verifies that DELETE on a document route returns 405.
func TestDocMethodNotAllowed(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodDelete, path)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// TestDocPostThumbnail_HappyPath verifies POST /{area}/thumbnail/ returns 200.
func TestDocPostThumbnail_HappyPath(t *testing.T) {
	restoreCollab := stubCollaboraConvert([]byte(fakePDFBytes), nil)
	defer restoreCollab()
	restoreRaster := stubPDFRasterize([]byte("jpeg-output"), nil)
	defer restoreRaster()

	cfg := testCfg()
	mux := buildDocMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "doc.docx", []byte("doc-bytes"))
	req := httptest.NewRequest(http.MethodPost, "/preview/document/100x200/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestDocGetPreview_UnmatchedPath verifies that unrecognised paths → 404 JSON.
func TestDocGetPreview_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	store := &mockStore{}
	mux := buildDocMux(cfg, store)

	path := fmt.Sprintf("/preview/document/%s/1/extra/path/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
}
