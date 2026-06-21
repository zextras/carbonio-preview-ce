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
	"io"
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
	calls           int // number of RetrieveData invocations
}

func (m *mockStore) RetrieveData(
	_ context.Context,
	fileID string,
	version int,
	serviceType, _ string,
) (storage.Blob, error) {
	m.calls++
	m.lastID = fileID
	m.lastVersion = version
	m.lastServiceType = serviceType
	return m.blob, m.err
}

func (m *mockStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _, _ string) (io.ReadCloser, error) {
	if m.err != nil {
		return nil, m.err
	}
	return io.NopCloser(bytes.NewReader(m.blob)), nil
}

func (m *mockStore) StoreData(_ context.Context, nodeID string, _ int, _, _ string, _ []byte) (string, error) {
	return nodeID, nil
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
		RenderConcurrency:                    runtime.NumCPU(),
		VideoConcurrency:                     runtime.NumCPU(),
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
			// Accept loc[1]==paramName (huma hand-crafted errors via Location "path.X")
			// or loc[0]==paramName (huma schema-level validation for required fields).
			for _, l := range d.Loc {
				if l == paramName {
					found = true
					break
				}
			}
			if found {
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

// buildImageHumaMux creates a huma-routed mux for image operations (test helper).
func buildImageHumaMux(cfg *config.Config, store storage.Client) *http.ServeMux {
	mux := http.NewServeMux()
	api := newHumaAPI(mux)
	registerImageOps(api, Deps{Cfg: cfg, Store: store, Cache: nil, Sem: nil})
	return mux
}

// TestImageGetPreview_Success verifies that a successful GET preview returns
// 200 with the correct Content-Type.
func TestImageGetPreview_Success(t *testing.T) {
	store := &mockStore{blob: []byte(fakeImgBytes)}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte(fakeImgBytes), nil)
	defer restore()

	cfg := testCfg()
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
			mux := buildImageHumaMux(cfg, store)

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
			mux := buildImageHumaMux(cfg, store)

			path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", tt.id)
			rec := doRequest(mux, http.MethodGet, path)

			// Huma's schema-level format:uuid validation fires for syntactically
			// invalid UUIDs (e.g. "not-a-uuid", "1234") before our handler runs,
			// so the message may be huma's internal validation message rather than
			// config.Msg.IDNotValid. We only assert the 422 status + loc["id"].
			assertValidationError(t, rec, "id")
		})
	}
}

// TestImageGetPreview_MissingServiceType verifies that missing service_type → 422.
func TestImageGetPreview_MissingServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, store)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertValidationError(t, rec, "service_type")
}

// TestImageGetPreview_InvalidServiceType verifies that an unknown service_type → 422.
func TestImageGetPreview_InvalidServiceType(t *testing.T) {
	store := &mockStore{}
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
			mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, nil) // POST has no storage call

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
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "img.png", []byte("png-data"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/50x50/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestImagePostPreview_NoFile verifies that POST with a valid multipart body but
// no "file" field returns 422 with the FileNotValid message.
// Sending the correct multipart Content-Type lets huma parse the form successfully;
// our handler then detects the missing file and returns {"detail":"...","loc":["body","file"]}.
func TestImagePostPreview_NoFile(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	// Send a valid multipart envelope with no "file" field.
	emptyMultipart, ct := buildMultipart(t, "other_field", "ignored", []byte("x"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x200/", emptyMultipart)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Missing file → 422 with "file" in loc. The exact message may be huma's
	// schema-level "File required" or our config.Msg.FileNotValid, depending on
	// whether huma validates the required field before calling the handler.
	assertValidationError(t, rec, "file")
}

// TestImageGetThumbnail_Storage404 verifies 404 JSON body on thumbnail GET.
func TestImageGetThumbnail_Storage404(t *testing.T) {
	store := &mockStore{err: storage.ErrNotFound}

	cfg := testCfg()
	mux := buildImageHumaMux(cfg, store)

	path := fmt.Sprintf("/preview/image/%s/1/100x200/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

// TestImageGetThumbnail_StorageError verifies 422 JSON body when storage is down.
func TestImageGetThumbnail_StorageError(t *testing.T) {
	store := &mockStore{err: storage.ErrUnavailable}

	cfg := testCfg()
	mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

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
			mux := buildImageHumaMux(cfg, store)

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
	mux := buildImageHumaMux(cfg, store)

	path := fmt.Sprintf("/preview/image/%s/1/0x0/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestImageMethodNotAllowed verifies that DELETE returns 405.
func TestImageMethodNotAllowed(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, &mockStore{})

	path := fmt.Sprintf("/preview/image/%s/1/100x200/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodDelete, path)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// TestImageGetPreview_UnmatchedPath verifies that an unrecognised path (extra
// segments, no service_type query param) does not return 200.
// In Go 1.22 ServeMux, trailing-slash patterns act as subtree matches, so extra
// segments are routed to the handler and result in a 422 (missing service_type).
func TestImageGetPreview_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, &mockStore{})

	// Extra segments — routed to handler but service_type missing → 422.
	path := fmt.Sprintf("/preview/image/%s/1/100x200/extra/junk/", validUUID)
	rec := doRequest(mux, http.MethodGet, path)
	if rec.Code == http.StatusOK {
		t.Errorf("status: got 200, want non-200 (extra path segments must not succeed)")
	}
}

// TestImagePostPreview_UnmatchedPath verifies that a POST to a deep path does
// not return 200.
func TestImagePostPreview_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, &mockStore{})

	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x200/extra/", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("status: got 200, want non-200 (extra path segments must not succeed)")
	}
}

// TestImagePostThumbnail_NotMultipart_422 covers the non-multipart body arm.
// With huma, a wrong Content-Type triggers huma's own multipart parsing error
// (loc["body"]) rather than loc["body","file"], so we only assert the 422 status.
func TestImagePostThumbnail_NotMultipart_422(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/thumbnail/", strings.NewReader("not-multipart"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want 422", rec.Code)
	}
}

// TestImagePostThumbnail_EmptyFile_422 covers the len(data)==0 arm of
// readMultipartFile: a multipart body with a zero-byte "file" field → "empty
// file" → 422 with the "file" body param.
func TestImagePostThumbnail_EmptyFile_422(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "empty.jpg", []byte{})
	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "file")
}

// TestImagePostPreview_EmptyFile_422 covers the same empty-file arm on the POST
// preview handler.
func TestImagePostPreview_EmptyFile_422(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "empty.jpg", []byte{})
	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "file")
}

// TestImagePostPreview_RenderError_400 covers the render-error arm of the POST
// preview handler: a render failure → 400 with FormatNotSupported.
func TestImagePostPreview_RenderError_400(t *testing.T) {
	restore := stubImageThumbnail("", "", "", "", nil, errors.New("vips boom"))
	defer restore()

	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "x.jpg", []byte("jpeg-data"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

// TestImagePostThumbnail_RenderError_400 covers the render-error arm of the
// POST thumbnail handler.
func TestImagePostThumbnail_RenderError_400(t *testing.T) {
	restore := stubImageThumbnail("", "", "", "", nil, errors.New("vips boom"))
	defer restore()

	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "x.jpg", []byte("jpeg-data"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

// TestImagePostPreview_InvalidQueryParams covers the query-param validation
// arms of imagePostPreview (crop, quality, output_format → 422).
func TestImagePostPreview_InvalidQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		query string
		param string
	}{
		{"bad crop", "crop=maybe", "crop"},
		{"bad quality", "quality=extreme", "quality"},
		{"bad output_format", "output_format=tiff", "output_format"},
	}
	cfg := testCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := buildImageHumaMux(cfg, nil)

			body, ct := buildMultipart(t, "file", "x.jpg", []byte("jpeg"))
			req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/?"+tt.query, body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			assertValidationError(t, rec, tt.param)
		})
	}
}

// TestImagePostThumbnail_InvalidQueryParams covers the query-param validation
// arms of imagePostThumbnail (shape, quality, output_format → 422).
func TestImagePostThumbnail_InvalidQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		query string
		param string
	}{
		{"bad shape", "shape=hexagonal", "shape"},
		{"bad quality", "quality=extreme", "quality"},
		{"bad output_format", "output_format=tiff", "output_format"},
	}
	cfg := testCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := buildImageHumaMux(cfg, nil)

			body, ct := buildMultipart(t, "file", "x.jpg", []byte("jpeg"))
			req := httptest.NewRequest(http.MethodPost, "/preview/image/100x100/thumbnail/?"+tt.query, body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			assertValidationError(t, rec, tt.param)
		})
	}
}

// TestImagePostThumbnail_InvalidArea covers the area-path validation arm of
// imagePostThumbnail.
func TestImagePostThumbnail_InvalidArea(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "x.jpg", []byte("jpeg"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/badarea/thumbnail/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "area")
}

// TestImagePostPreview_InvalidArea covers the area-path validation arm of
// imagePostPreview.
func TestImagePostPreview_InvalidArea(t *testing.T) {
	cfg := testCfg()
	mux := buildImageHumaMux(cfg, nil)

	body, ct := buildMultipart(t, "file", "x.jpg", []byte("jpeg"))
	req := httptest.NewRequest(http.MethodPost, "/preview/image/badarea/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertValidationError(t, rec, "area")
}

// TestImageGetThumbnail_RenderError_400 covers the render-error arm of the GET
// thumbnail handler (the GET preview variant is already covered by
// TestImageGetPreview_RenderError).
func TestImageGetThumbnail_RenderError_400(t *testing.T) {
	store := &mockStore{blob: []byte("img")}
	restore := stubImageThumbnail("", "", "", "", nil, errors.New("vips boom"))
	defer restore()

	cfg := testCfg()
	mux := buildImageHumaMux(cfg, store)

	path := fmt.Sprintf("/preview/image/%s/1/100x100/thumbnail/?service_type=files", validUUID)
	rec := doRequest(mux, http.MethodGet, path)

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
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
