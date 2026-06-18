// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
	"github.com/zextras/carbonio-preview-ce/video"
)

// fakeStore returns canned bytes for both retrieve methods.
type fakeStore struct {
	body []byte
	err  error
}

func (f fakeStore) RetrieveData(_ context.Context, _ string, _ int, _, _ string) (storage.Blob, error) {
	return f.body, f.err
}
func (f fakeStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _, _ string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func TestVideoGetThumbnail_OK(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	origRender := imageThumbnailFunc
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return []byte("\x89PNGframe0"), nil
	}
	imageThumbnailFunc = func(_ chan struct{}, data []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		if string(data) != "\x89PNGframe0" {
			t.Fatalf("renderer did not receive the extracted frame, got %q", data)
		}
		return []byte("rendered-jpeg"), nil
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract; imageThumbnailFunc = origRender })

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "rendered-jpeg" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestVideoGetPreview_OK(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	origRender := imageThumbnailFunc
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return []byte("\x89PNGframe0"), nil
	}
	imageThumbnailFunc = func(_ chan struct{}, data []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		if string(data) != "\x89PNGframe0" {
			t.Fatalf("renderer did not receive the extracted frame, got %q", data)
		}
		return []byte("rendered-jpeg"), nil
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract; imageThumbnailFunc = origRender })

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/?service_type=files&crop=true", nil)

	videoGetPreview(rr, req, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "rendered-jpeg" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestVideoGetThumbnail_Storage404(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{err: storage.ErrNotFound}, c, sem)

	assertStringDetail(t, rr, http.StatusNotFound, config.Msg.ItemNotFound)
}

func TestVideoGetThumbnail_StorageError(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{err: storage.ErrUnavailable}, c, sem)

	assertStringDetail(t, rr, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

func TestVideoGetThumbnail_ExtractError(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return nil, errors.New("ffmpeg failed")
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract })

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	assertStringDetail(t, rr, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

func TestVideoGetThumbnail_RenderError(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	origRender := imageThumbnailFunc
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return []byte("\x89PNGframe0"), nil
	}
	imageThumbnailFunc = func(_ chan struct{}, _ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return nil, errors.New("vips exploded")
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract; imageThumbnailFunc = origRender })

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	assertStringDetail(t, rr, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

func TestVideoGetThumbnail_InvalidID(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/preview/video/bad-id/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, "bad-id", "1", "320x240", cfg, fakeStore{}, c, sem)

	assertValidationError(t, rr, "id")
}

func TestVideoGetThumbnail_InvalidArea(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)
	id := "11111111-1111-1111-1111-111111111111"

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/preview/video/%s/1/badarea/thumbnail/?service_type=files", id), nil)

	videoGetThumbnail(rr, req, id, "1", "badarea", cfg, fakeStore{}, c, sem)

	assertValidationError(t, rr, "area")
}

func TestVideoRoute_MethodNotAllowed(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerVideoRoutes(mux, cfg, fakeStore{}, cache.New(1<<20), make(chan struct{}, 1))

	id := "11111111-1111-1111-1111-111111111111"
	path := fmt.Sprintf("/preview/video/%s/1/320x240/thumbnail/?service_type=files", id)
	rec := doRequest(mux, http.MethodPost, path)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

func TestVideoRoute_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	mux := http.NewServeMux()
	registerVideoRoutes(mux, cfg, fakeStore{}, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodGet, "/preview/video/extra/junk/segments/here/too/")
	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
}

func TestVideoGetThumbnail_ErrTooLarge(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return nil, video.ErrTooLarge
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract })

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	req := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	assertStringDetail(t, rr, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

func TestVideoGetThumbnail_CancelledContext(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return nil, context.Canceled
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract })

	rr := httptest.NewRecorder()
	id := "11111111-1111-1111-1111-111111111111"
	baseReq := httptest.NewRequest(http.MethodGet,
		"/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)
	ctx, cancel := context.WithCancel(baseReq.Context())
	cancel() // cancel immediately so r.Context().Err() != nil
	req := baseReq.WithContext(ctx)

	videoGetThumbnail(rr, req, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	// A cancelled client must NOT produce a 400 response and must not panic.
	// The recorder default code is 200 (not yet written), so we just assert it is not 400.
	if rr.Code == http.StatusBadRequest {
		t.Errorf("cancelled context: got 400, want silent return (no error response written)")
	}
	if rr.Body.Len() != 0 {
		t.Errorf("cancelled context: expected empty body, got %q", rr.Body.String())
	}
}

func TestVideoGetThumbnail_Cache(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	origExtract := videoFirstFrameFunc
	origRender := imageThumbnailFunc
	callCount := 0
	videoFirstFrameFunc = func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		callCount++
		_, _ = io.ReadAll(r)
		return []byte("\x89PNGframe0"), nil
	}
	imageThumbnailFunc = func(_ chan struct{}, _ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return []byte("cached-jpeg"), nil
	}
	t.Cleanup(func() { videoFirstFrameFunc = origExtract; imageThumbnailFunc = origRender })

	id := "11111111-1111-1111-1111-111111111111"

	// First request — populates cache.
	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)
	videoGetThumbnail(rr1, req1, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: status %d", rr1.Code)
	}

	// Second request — must be served from cache (extractor not called again).
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/preview/video/"+id+"/1/320x240/thumbnail/?service_type=files", nil)
	videoGetThumbnail(rr2, req2, id, "1", "320x240", cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second request: status %d", rr2.Code)
	}
	if callCount != 1 {
		t.Fatalf("extractor called %d times, want 1 (second should hit cache)", callCount)
	}
}
