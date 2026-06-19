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

// buildVideoHumaMux creates a huma-routed mux for video operations (test helper).
func buildVideoHumaMux(cfg *config.Config, store storage.Client, c *cache.Cache, sem chan struct{}) *http.ServeMux {
	mux := http.NewServeMux()
	api := newHumaAPI(mux)
	registerVideoOps(api, Deps{Cfg: cfg, Store: store, Cache: c, Sem: sem})
	return mux
}

// videoThumbnailURL builds the GET thumbnail URL for a given id/version/area.
func videoThumbnailURL(id, version, area string) string {
	return fmt.Sprintf("/preview/video/%s/%s/%s/thumbnail/?service_type=files", id, version, area)
}

// videoPreviewURL builds the GET preview URL for a given id/version/area.
func videoPreviewURL(id, version, area string) string {
	return fmt.Sprintf("/preview/video/%s/%s/%s/?service_type=files", id, version, area)
}

const videoTestID = "11111111-1111-1111-1111-111111111111"

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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	rec := doRequest(mux, http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "rendered-jpeg" {
		t.Fatalf("body = %q", rec.Body.String())
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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	url := fmt.Sprintf("/preview/video/%s/1/320x240/?service_type=files&crop=true", videoTestID)
	rec := doRequest(mux, http.MethodGet, url)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "rendered-jpeg" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestVideoGetThumbnail_Storage404(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	mux := buildVideoHumaMux(cfg, fakeStore{err: storage.ErrNotFound}, c, sem)
	rec := doRequest(mux, http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"))

	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

func TestVideoGetThumbnail_StorageError(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	mux := buildVideoHumaMux(cfg, fakeStore{err: storage.ErrUnavailable}, c, sem)
	rec := doRequest(mux, http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"))

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	rec := doRequest(mux, http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"))

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	rec := doRequest(mux, http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"))

	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

func TestVideoGetThumbnail_InvalidID(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	mux := buildVideoHumaMux(cfg, fakeStore{}, c, sem)
	rec := doRequest(mux, http.MethodGet, "/preview/video/bad-id/1/320x240/thumbnail/?service_type=files")

	assertValidationError(t, rec, "id")
}

func TestVideoGetThumbnail_InvalidArea(t *testing.T) {
	cfg := testCfg()
	c := cache.New(1 << 20)
	sem := make(chan struct{}, 1)

	mux := buildVideoHumaMux(cfg, fakeStore{}, c, sem)
	rec := doRequest(mux, http.MethodGet,
		fmt.Sprintf("/preview/video/%s/1/badarea/thumbnail/?service_type=files", videoTestID))

	assertValidationError(t, rec, "area")
}

func TestVideoRoute_MethodNotAllowed(t *testing.T) {
	cfg := testCfg()
	mux := buildVideoHumaMux(cfg, fakeStore{}, cache.New(1<<20), make(chan struct{}, 1))

	path := fmt.Sprintf("/preview/video/%s/1/320x240/thumbnail/?service_type=files", videoTestID)
	rec := doRequest(mux, http.MethodPost, path)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// TestVideoRoute_UnmatchedPath verifies that paths with extra segments do not
// return 200. In Go 1.22 ServeMux, trailing-slash patterns act as subtree
// matches, so extra segments may route to the handler (returning 422 for
// missing/invalid params) rather than 404.
func TestVideoRoute_UnmatchedPath(t *testing.T) {
	cfg := testCfg()
	mux := buildVideoHumaMux(cfg, fakeStore{}, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodGet, "/preview/video/extra/junk/segments/here/too/")
	if rec.Code == http.StatusOK {
		t.Errorf("status: got 200, want non-200 (extra path segments must not succeed)")
	}
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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	rec := doRequest(mux, http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"))

	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

// TestVideoGetThumbnail_CancelledContext verifies that a cancelled client does not
// produce a 400 response. With the huma adapter the handler may return a 503
// "request cancelled" error — what matters is that 400 is NOT returned and that
// the test does not panic.
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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)

	baseReq := httptest.NewRequest(http.MethodGet, videoThumbnailURL(videoTestID, "1", "320x240"), nil)
	ctx, cancel := context.WithCancel(baseReq.Context())
	cancel() // cancel immediately
	req := baseReq.WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// A cancelled client must NOT produce a 400 Bad Request.
	if rr.Code == http.StatusBadRequest {
		t.Errorf("cancelled context: got 400, want != 400 (no format-error response written)")
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

	mux := buildVideoHumaMux(cfg, fakeStore{body: []byte("video-bytes")}, c, sem)
	url := videoThumbnailURL(videoTestID, "1", "320x240")

	// First request — populates cache.
	rr1 := doRequest(mux, http.MethodGet, url)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: status %d", rr1.Code)
	}

	// Second request — must be served from cache (extractor not called again).
	rr2 := doRequest(mux, http.MethodGet, url)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second request: status %d", rr2.Code)
	}
	if callCount != 1 {
		t.Fatalf("extractor called %d times, want 1 (second should hit cache)", callCount)
	}
}
