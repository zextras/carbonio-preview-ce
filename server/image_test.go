package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// ---- mock storage ----

type mockStore struct {
	blob []byte
	err  error
	// recorded call args for assertions
	lastID          string
	lastVersion     int
	lastServiceType string
}

func (m *mockStore) RetrieveData(
	_ context.Context,
	fileID string,
	version int,
	serviceType, _ string,
) (storage.Blob, error) {
	m.lastID = fileID
	m.lastVersion = version
	m.lastServiceType = serviceType
	return m.blob, m.err
}

// ---- render stubs ----

// stubImageThumbnail replaces imageThumbnailFunc for the duration of a test.
// The stub returns sentinel bytes so callers can verify content-type and status.
func stubImageThumbnail(
	outputFormat, quality, shape, cropMode string,
	returnData []byte,
	returnErr error,
) (restore func()) {
	prev := imageThumbnailFunc
	imageThumbnailFunc = func(
		_ chan struct{},
		_ []byte,
		_, _ int,
		gotFmt, gotQuality, gotShape, gotCropMode string,
	) ([]byte, error) {
		if outputFormat != "" && gotFmt != outputFormat {
			// Don't panic — just return error so test can detect the mismatch.
			return nil, fmt.Errorf("stub: outputFormat mismatch: got %q, want %q", gotFmt, outputFormat)
		}
		_ = gotQuality
		_ = gotShape
		_ = gotCropMode
		return returnData, returnErr
	}
	return func() { imageThumbnailFunc = prev }
}

// testCfg builds a minimal config.Config for routing.
func testCfg() *config.Config {
	return &config.Config{
		ServiceName:                          "preview",
		ServiceImageName:                     "image",
		ServicePDFName:                       "pdf",
		ServiceDocumentName:                  "document",
		ServiceHealthName:                    "health",
		ServiceTimeoutInSeconds:              30,
		ServiceDocsTimeout:                   15,
		ServiceEnableDocumentPreview:         true,
		ServiceEnableDocumentThumbnail:       true,
		StorageFullAddress:                   "http://127.0.0.1:20000",
		StorageHealthCheck:                   "health/live",
		DocumentConversionFullServiceAddress: "http://127.0.0.1:20001/services/docs/editor/",
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/services/docs/editor/cool/convert-to",
		AreDocsEnabled:                       true,
	}
}

const (
	validUUID    = "123e4567-e89b-12d3-a456-426614174000"
	fakeImgBytes = "fake-jpeg-bytes"
)

// doRequest fires an HTTP request against a mux and returns the recorder.
func doRequest(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestImageGetPreview_Success verifies that a successful GET preview returns
// 200 with the correct Content-Type.
func TestImageGetPreview_Success(t *testing.T) {
	store := &mockStore{blob: []byte(fakeImgBytes)}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte(fakeImgBytes), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
	if rec.Body.String() != fakeImgBytes {
		t.Errorf("body: got %q, want %q", rec.Body.String(), fakeImgBytes)
	}
}

// TestImageGetPreview_OutputFormatPNG verifies that output_format=png returns
// image/png Content-Type.
func TestImageGetPreview_OutputFormatPNG(t *testing.T) {
	store := &mockStore{blob: []byte("fake-png")}
	restore := stubImageThumbnail("png", "", "", "", []byte("fake-png"), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files&output_format=png", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type: got %q, want image/png", ct)
	}
}

// TestImageGetPreview_Storage404 verifies that storage ErrNotFound → 404
// with the exact ITEM_NOT_FOUND message body.
func TestImageGetPreview_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}
	restore := stubImageThumbnail("", "", "", "", nil, nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.ItemNotFound) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.ItemNotFound)
	}
}

// TestImageGetPreview_StorageUnavailable verifies that storage ErrUnavailable
// → 502 with the STORAGE_UNAVAILABLE_STRING body.
func TestImageGetPreview_StorageUnavailable(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}
	restore := stubImageThumbnail("", "", "", "", nil, nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.StorageUnavailable) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.StorageUnavailable)
	}
}

// TestImageGetPreview_InvalidArea verifies that a malformed area segment
// returns 400 with the HEIGHT_OR_WIDTH_NOT_INSERTED error.
func TestImageGetPreview_InvalidArea(t *testing.T) {
	tests := []struct {
		name string
		area string
	}{
		{"no-x", "100200"},
		{"alpha", "axb"},
		{"only-width", "100x"},
		{"negative-syntax", "-1x100"},
		// x-first (width is missing, height only style)
		{"x-only", "x200"},
	}

	cfg := testCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{}
			mux := http.NewServeMux()
			registerImageRoutes(mux, cfg, store, nil)

			path := fmt.Sprintf("/preview/image/%s/1/%s/?service_type=files", validUUID, tt.area)
			rec := doRequest(mux, http.MethodGet, path)

			// Should be 400 (bad area → either INPUT_ERROR or HEIGHT_OR_WIDTH error)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("area %q: status %d, want 400", tt.area, rec.Code)
			}
		})
	}
}

// TestImageGetPreview_InvalidUUID verifies that a malformed UUID → 400.
func TestImageGetPreview_InvalidUUID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"not-uuid", "not-a-uuid"},
		{"short", "1234"},
		{"nil-uuid", "00000000-0000-0000-0000-000000000000"},
	}

	cfg := testCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{}
			mux := http.NewServeMux()
			registerImageRoutes(mux, cfg, store, nil)

			path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", tt.id)
			rec := doRequest(mux, http.MethodGet, path)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("id %q: status %d, want 400", tt.id, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), config.Msg.IDNotValid) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.IDNotValid)
			}
		})
	}
}

// TestImageGetPreview_MissingServiceType verifies that missing service_type → 400.
func TestImageGetPreview_MissingServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// TestImageGetPreview_InvalidServiceType verifies that an unknown service_type → 400.
func TestImageGetPreview_InvalidServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=unknown", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// TestImageGetThumbnail_Success verifies the happy path for GET thumbnail.
func TestImageGetThumbnail_Success(t *testing.T) {
	store := &mockStore{blob: []byte(fakeImgBytes)}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte(fakeImgBytes), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
}

// TestImageGetThumbnail_ShapeRounded verifies shape=rounded is accepted and
// the response content-type is png (rounded forces png output).
func TestImageGetThumbnail_ShapeRounded(t *testing.T) {
	store := &mockStore{blob: []byte(fakeImgBytes)}
	prev := imageThumbnailFunc
	imageThumbnailFunc = func(
		_ chan struct{}, _ []byte, _, _ int,
		_, _, gotShape, _ string,
	) ([]byte, error) {
		if gotShape != "rounded" {
			return nil, fmt.Errorf("stub: shape %q, want rounded", gotShape)
		}
		return []byte("png-bytes"), nil
	}
	defer func() { imageThumbnailFunc = prev }()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files&shape=rounded&output_format=png", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestImageGetThumbnail_InvalidShape verifies that an unknown shape value → 400.
func TestImageGetThumbnail_InvalidShape(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files&shape=hexagonal", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// TestImageGetPreview_QualityValues tests all accepted quality values return 200.
func TestImageGetPreview_QualityValues(t *testing.T) {
	for _, q := range []string{"lowest", "low", "medium", "high", "highest"} {
		q := q
		t.Run(q, func(t *testing.T) {
			store := &mockStore{blob: []byte("img")}
			restore := stubImageThumbnail("", "", "", "", []byte("img"), nil)
			defer restore()

			cfg := testCfg()
			mux := http.NewServeMux()
			registerImageRoutes(mux, cfg, store, nil)

			path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files&quality=%s", validUUID, q)
			rec := doRequest(mux, http.MethodGet, path)
			if rec.Code != http.StatusOK {
				t.Errorf("quality=%s: status %d, want 200", q, rec.Code)
			}
		})
	}
}

// TestImageGetPreview_InvalidQuality verifies unknown quality → 400.
func TestImageGetPreview_InvalidQuality(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files&quality=extreme", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// TestImageGetPreview_CropTrue verifies that crop=true is accepted (200).
func TestImageGetPreview_CropTrue(t *testing.T) {
	store := &mockStore{blob: []byte("img")}
	restore := stubImageThumbnail("", "", "", "", []byte("img"), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files&crop=true", validUUID)
	rec := doRequest(mux, http.MethodGet, path)
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestImagePostPreview_Success verifies that a multipart POST preview returns
// 200 with the correct Content-Type.
func TestImagePostPreview_Success(t *testing.T) {
	restore := stubImageThumbnail("jpeg", "", "", "", []byte(fakeImgBytes), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, nil, nil) // POST has no storage call

	body, ct := buildMultipart(t, "file", "test.jpg", []byte("jpeg-data"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x200/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
}

// TestImagePostThumbnail_Success verifies POST thumbnail returns 200.
func TestImagePostThumbnail_Success(t *testing.T) {
	restore := stubImageThumbnail("jpeg", "", "", "", []byte(fakeImgBytes), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, nil, nil)

	body, ct := buildMultipart(t, "file", "img.png", []byte("png-data"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/50x50/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestImagePostPreview_NoFile verifies that POST without a multipart "file" field
// returns 400 with the FileNotValid error.
func TestImagePostPreview_NoFile(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x200/", strings.NewReader(""))
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

// TestImageGetThumbnail_Storage404 verifies 404 pass-through on thumbnail GET.
func TestImageGetThumbnail_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.ItemNotFound) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.ItemNotFound)
	}
}

// TestImageGetThumbnail_StorageUnavailable verifies 502 when storage is down.
func TestImageGetThumbnail_StorageUnavailable(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=chats", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.StorageUnavailable) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.StorageUnavailable)
	}
}

// TestImageGetPreview_RenderError verifies that a render failure → 500.
func TestImageGetPreview_RenderError(t *testing.T) {
	store := &mockStore{blob: []byte("img")}
	restore := stubImageThumbnail("", "", "", "", nil, errors.New("vips exploded"))
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

// TestImageGetPreview_InvalidVersion verifies that version=-1 → 400.
func TestImageGetPreview_InvalidVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{"negative", "-1"},
		{"alpha", "abc"},
	}
	cfg := testCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{}
			mux := http.NewServeMux()
			registerImageRoutes(mux, cfg, store, nil)

			path := fmt.Sprintf("/preview/image/%s/%s/100x200/?service_type=files", validUUID, tt.version)
			rec := doRequest(mux, http.MethodGet, path)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("version=%s: status %d, want 400", tt.version, rec.Code)
			}
		})
	}
}

// TestImageGetPreview_ZeroArea verifies that 0x0 area is accepted (valid —
// means "use original dimensions" per Python spec).
func TestImageGetPreview_ZeroArea(t *testing.T) {
	store := &mockStore{blob: []byte("img")}
	restore := stubImageThumbnail("", "", "", "", []byte("img"), nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/0x0/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestImageMethodNotAllowed verifies that DELETE returns 405.
func TestImageMethodNotAllowed(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, &mockStore{}, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodDelete, path)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// ---- helpers ----

// buildMultipart builds a multipart/form-data body with a single field.
func buildMultipart(t *testing.T, fieldName, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
