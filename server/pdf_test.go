package server

import (
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

// TestPDFGetPreview_Storage404 verifies 404 pass-through.
func TestPDFGetPreview_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.ItemNotFound) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.ItemNotFound)
	}
}

// TestPDFGetPreview_StorageUnavailable verifies 502 when storage is down.
func TestPDFGetPreview_StorageUnavailable(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.StorageUnavailable) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.StorageUnavailable)
	}
}

// TestPDFGetPreview_InvalidUUID verifies that a bad UUID returns 400.
func TestPDFGetPreview_InvalidUUID(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := "/preview/pdf/not-a-uuid/1/?service_type=files"
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
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

// TestPDFGetPreview_InvalidPageRange verifies first_page > last_page → 400
// with the NumberOfPagesNotValid message.
func TestPDFGetPreview_InvalidPageRange(t *testing.T) {
	store := &mockStore{blob: []byte(fakePDFBytes)}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files&first_page=5&last_page=2", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.NumberOfPagesNotValid) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.NumberOfPagesNotValid)
	}
}

// TestPDFGetPreview_FirstPageZero verifies that first_page=0 → 400 (must be >= 1).
func TestPDFGetPreview_FirstPageZero(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/?service_type=files&first_page=0", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
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

// TestPDFGetThumbnail_Storage404 verifies 404 pass-through on thumbnail.
func TestPDFGetThumbnail_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// TestPDFGetThumbnail_InvalidArea verifies bad area → 400.
func TestPDFGetThumbnail_InvalidArea(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/badarea/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
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

// TestPDFPostPreview_NoFile verifies that POST without a file field → 400.
func TestPDFPostPreview_NoFile(t *testing.T) {
	cfg := testCfg()
	mux := buildPDFMux(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/pdf/", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.FileNotValid) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.FileNotValid)
	}
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

// TestPDFGetPreview_MissingServiceType verifies missing service_type → 400.
func TestPDFGetPreview_MissingServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildPDFMux(cfg, store)

	path := fmt.Sprintf("/preview/pdf/%s/1/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
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
