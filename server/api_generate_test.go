// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"testing"

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
	"github.com/zextras/carbonio-preview-ce/video"
)

// genFakeStore records StoreData calls and serves a canned video stream. A
// non-nil retrieveErr makes RetrieveDataStreaming fail.
type genFakeStore struct {
	stored      map[string][]byte
	video       []byte
	storedID    string
	retrieveErr error
}

func (f *genFakeStore) RetrieveData(context.Context, string, int, string, string) (storage.Blob, error) {
	return f.video, f.retrieveErr
}
func (f *genFakeStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, error) {
	if f.retrieveErr != nil {
		return nil, f.retrieveErr
	}
	return io.NopCloser(bytes.NewReader(f.video)), nil
}
func (f *genFakeStore) StoreData(_ context.Context, nodeID string, _ int, _ string, _ string, data []byte) (string, error) {
	if f.stored == nil {
		f.stored = map[string][]byte{}
	}
	f.stored[nodeID] = data
	f.storedID = nodeID
	return nodeID, nil
}

// buildGenerateHumaMux creates a huma-routed mux for the generate operation.
func buildGenerateHumaMux(cfg *config.Config, store storage.Client, c *cache.Cache, vsem chan struct{}) *http.ServeMux {
	mux := http.NewServeMux()
	api := newHumaAPI(mux)
	registerGenerateOps(api, Deps{Cfg: cfg, Store: store, Cache: c, VideoSem: vsem})
	return mux
}

const (
	genSourceUUID = "11111111-1111-1111-1111-111111111111"
	genTargetUUID = "22222222-2222-2222-2222-222222222222"
)

func generateURL(sourceID, version, target string) string {
	return fmt.Sprintf("/preview/video/generate/%s/%s/?service_type=chats&target=%s", sourceID, version, target)
}

// tinyPNG returns a minimal valid PNG so the JPEG re-encode step has real input.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestGenerate_StoresJPEGFrameUnderCallerUUID verifies the helper extracts a
// frame, JPEG-encodes it, and stores it under the caller-supplied UUID,
// echoing that id.
func TestGenerate_StoresJPEGFrameUnderCallerUUID(t *testing.T) {
	pngBytes := tinyPNG(t)
	orig := videoFirstFrameFunc
	videoFirstFrameFunc = func(context.Context, io.Reader) ([]byte, error) { return pngBytes, nil }
	defer func() { videoFirstFrameFunc = orig }()

	fs := &genFakeStore{video: []byte("RIFFfakevideo")}

	id, err := generateFirstFrameJPEG(context.Background(), fs, "src-node", 0, "chats", "owner-1", genTargetUUID)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if id != genTargetUUID {
		t.Fatalf("expected echoed caller UUID %q, got %q", genTargetUUID, id)
	}
	stored, ok := fs.stored[genTargetUUID]
	if !ok {
		t.Fatalf("frame was not stored under %q", genTargetUUID)
	}
	// stored bytes must be JPEG (SOI marker 0xFFD8), not PNG.
	if len(stored) < 2 || stored[0] != 0xFF || stored[1] != 0xD8 {
		n := 2
		if len(stored) < n {
			n = len(stored)
		}
		t.Fatalf("expected JPEG bytes, got prefix %x", stored[:n])
	}
}

// TestGenerate_HTTP_OK verifies the endpoint returns 200 + {"preview_id":target}.
func TestGenerate_HTTP_OK(t *testing.T) {
	pngBytes := tinyPNG(t)
	orig := videoFirstFrameFunc
	videoFirstFrameFunc = func(context.Context, io.Reader) ([]byte, error) { return pngBytes, nil }
	defer func() { videoFirstFrameFunc = orig }()

	fs := &genFakeStore{video: []byte("RIFFfakevideo")}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodPost, generateURL(genSourceUUID, "0", genTargetUUID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf(`{"preview_id":"%s"}`, genTargetUUID)
	if got := rec.Body.String(); !bytes.Contains([]byte(got), []byte(want)) {
		t.Fatalf("body = %q, want it to contain %q", got, want)
	}
	if fs.storedID != genTargetUUID {
		t.Fatalf("stored under %q, want %q", fs.storedID, genTargetUUID)
	}
}

// TestGenerate_HTTP_Busy429 verifies a full video semaphore yields 429 + Retry-After
// without invoking the extractor.
func TestGenerate_HTTP_Busy429(t *testing.T) {
	orig := videoFirstFrameFunc
	called := false
	videoFirstFrameFunc = func(context.Context, io.Reader) ([]byte, error) {
		called = true
		return tinyPNG(t), nil
	}
	defer func() { videoFirstFrameFunc = orig }()

	// Capacity-1 semaphore, pre-filled so try-acquire fails immediately.
	vsem := make(chan struct{}, 1)
	vsem <- struct{}{}

	fs := &genFakeStore{video: []byte("RIFFfakevideo")}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), vsem)

	rec := doRequest(mux, http.MethodPost, generateURL(genSourceUUID, "0", genTargetUUID))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header on 429")
	}
	if called {
		t.Errorf("extractor must not be called when the semaphore is full")
	}
}

// TestGenerate_HTTP_NotFound maps storage.ErrNotFound to 404.
func TestGenerate_HTTP_NotFound(t *testing.T) {
	fs := &genFakeStore{retrieveErr: storage.ErrNotFound}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodPost, generateURL(genSourceUUID, "0", genTargetUUID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestGenerate_HTTP_ExtractFailed maps video.ErrExtractFailed (AV1/corrupt) to 422.
func TestGenerate_HTTP_ExtractFailed(t *testing.T) {
	orig := videoFirstFrameFunc
	videoFirstFrameFunc = func(context.Context, io.Reader) ([]byte, error) { return nil, video.ErrExtractFailed }
	defer func() { videoFirstFrameFunc = orig }()

	fs := &genFakeStore{video: []byte("RIFFfakevideo")}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodPost, generateURL(genSourceUUID, "0", genTargetUUID))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
}

// TestGenerate_HTTP_StorageError maps a generic storage error to 502.
func TestGenerate_HTTP_StorageError(t *testing.T) {
	fs := &genFakeStore{retrieveErr: storage.ErrUnavailable}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodPost, generateURL(genSourceUUID, "0", genTargetUUID))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
}

// TestGenerate_HTTP_InvalidTarget rejects a non-UUID target with 422.
func TestGenerate_HTTP_InvalidTarget(t *testing.T) {
	fs := &genFakeStore{video: []byte("RIFFfakevideo")}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodPost, generateURL(genSourceUUID, "0", "not-a-uuid"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
}

// TestGenerate_Route_MethodNotAllowed verifies GET on the generate path is rejected.
func TestGenerate_Route_MethodNotAllowed(t *testing.T) {
	fs := &genFakeStore{}
	mux := buildGenerateHumaMux(testCfg(), fs, cache.New(1<<20), make(chan struct{}, 1))

	rec := doRequest(mux, http.MethodGet, generateURL(genSourceUUID, "0", genTargetUUID))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}
