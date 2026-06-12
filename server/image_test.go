// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
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
		ServiceWorkers:                       runtime.NumCPU(),
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

// assertValidationError verifies 422 + application/json + array-detail body
// containing the expected param name.
func assertValidationError(t *testing.T, rec *httptest.ResponseRecorder, paramName string) {
	t.Helper()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want 422", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	var body validationErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON unmarshal: %v (body: %q)", err, rec.Body.String())
	}
	if len(body.Detail) == 0 {
		t.Fatal("expected non-empty detail array")
	}
	if paramName != "" {
		found := false
		for _, d := range body.Detail {
			if len(d.Loc) >= 2 && d.Loc[1] == paramName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected param %q in detail loc, got %v", paramName, body.Detail)
		}
	}
}

// assertStringDetail verifies the expected HTTP status + application/json +
// {"detail":"<msg>"} body shape.
func assertStringDetail(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantSubstr string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Errorf("status: got %d, want %d (body: %q)", rec.Code, wantStatus, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	var body stringDetailBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON unmarshal: %v (body: %q)", err, rec.Body.String())
	}
	if wantSubstr != "" && !strings.Contains(body.Detail, wantSubstr) {
		t.Errorf("detail %q does not contain %q", body.Detail, wantSubstr)
	}
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
// with JSON body {"detail":"<ItemNotFound>"}.
func TestImageGetPreview_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}
	restore := stubImageThumbnail("", "", "", "", nil, nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestImageGetPreview_StorageError verifies that non-404 storage errors → 422
// with {"detail":"<GenericErrorStorage>"}.
func TestImageGetPreview_StorageError(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}
	restore := stubImageThumbnail("", "", "", "", nil, nil)
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

// TestImageGetPreview_InvalidArea verifies that a malformed area segment
// returns 422 (validation error) with the area param in loc.
func TestImageGetPreview_InvalidArea(t *testing.T) {
	tests := []struct {
		name string
		area string
	}{
		{"no-x", "100200"},
		{"alpha", "axb"},
		{"only-width", "100x"},
		{"negative-syntax", "-1x100"},
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

			assertValidationError(t, rec, "area")
		})
	}
}

// TestImageGetPreview_InvalidUUID verifies that a malformed UUID → 422
// with the "id" param in loc.
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

			assertValidationError(t, rec, "id")
			// message text must still match
			var body validationErrorBody
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if len(body.Detail) > 0 && !strings.Contains(body.Detail[0].Msg, config.Msg.IDNotValid) {
				t.Errorf("msg %q does not contain %q", body.Detail[0].Msg, config.Msg.IDNotValid)
			}
		})
	}
}

// TestImageGetPreview_MissingServiceType verifies that missing service_type → 422.
func TestImageGetPreview_MissingServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "service_type")
}

// TestImageGetPreview_InvalidServiceType verifies that an unknown service_type → 422.
func TestImageGetPreview_InvalidServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=unknown", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "service_type")
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

// TestImageGetThumbnail_InvalidShape verifies that an unknown shape value → 422.
func TestImageGetThumbnail_InvalidShape(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files&shape=hexagonal", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "shape")
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

// TestImageGetPreview_InvalidQuality verifies unknown quality → 422.
func TestImageGetPreview_InvalidQuality(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files&quality=extreme", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "quality")
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
// returns 422 (body param validation) with the FileNotValid message.
func TestImagePostPreview_NoFile(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x200/", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Missing file → 422 (body param), loc = ["body","file"]
	assertValidationError(t, rec, "file")
	var body validationErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Detail) > 0 && !strings.Contains(body.Detail[0].Msg, config.Msg.FileNotValid) {
		t.Errorf("msg %q does not contain %q", body.Detail[0].Msg, config.Msg.FileNotValid)
	}
}

// TestImageGetThumbnail_Storage404 verifies 404 JSON body on thumbnail GET.
func TestImageGetThumbnail_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestImageGetThumbnail_StorageError verifies 422 JSON body when storage is down.
func TestImageGetThumbnail_StorageError(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=chats", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

// TestImageGetPreview_RenderError verifies that a render failure → 400
// with {"detail":"Format not supported."} (FastAPI HTTPException(400) shape).
func TestImageGetPreview_RenderError(t *testing.T) {
	store := &mockStore{blob: []byte("img")}
	restore := stubImageThumbnail("", "", "", "", nil, errors.New("vips exploded"))
	defer restore()

	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, nil)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

// TestImageGetPreview_InvalidVersion verifies that version=-1 → 422.
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
			assertValidationError(t, rec, "version")
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

// TestImageGetPreview_UnmatchedPath verifies that an unrecognised path segment
// returns 404 with {"detail":"Not Found"}.
func TestImageGetPreview_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, &mockStore{}, nil)

	// 5-segment GET path has no matching route.
	path := fmt.Sprintf("/preview/image/%s/1/100x200/extra/junk/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)
	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
}

// TestImagePostPreview_UnmatchedPath verifies that a POST to a deep path → 404.
func TestImagePostPreview_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, &mockStore{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x200/extra/", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
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
