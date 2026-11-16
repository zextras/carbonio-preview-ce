//go:build cgo

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Full-flow in-process tests (Phase 2).
//
// These exercise the REAL preview server end to end: real handlers (via the
// public Server.Handler() over an httptest.Server), real config, real cache, and — the
// key difference from the Phase-1 handler tests — REAL render (cgo libvips +
// a real pdfium worker, NOT the render_hooks.go stubs). The only things mocked
// are the two direct dependencies, both in-process httptest.Server instances:
//
//   - carbonio-storages download  (testsupport.StoragesMock)
//   - docs-editor / Collabora convert  (testsupport.CollaboraMock)
//
// NO docker, NO testcontainers. The suite self-skips (mirroring
// render/pdf_e2e_test.go) when cgo / libpdfium / libvips / the pdfium worker
// is unavailable.
//
// Run model: a single top-level TestFullFlow initialises the real render pool
// ONCE (the pool is process-global) and runs every flow as a subtest, so the
// worker is built once and torn down once.

package server

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/server/testsupport"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

// fullFlowFixtures holds the real test bytes, loaded once.
type fullFlowFixtures struct {
	jpeg []byte
	png  []byte
	pdf  []byte
	doc  []byte
}

// buildFullFlowServer wires a REAL server (real handlers, real render hooks,
// real cache) pointed at the two in-process mocks, and returns a started
// httptest.Server over the real Server.Handler() plus the cache and store.
//
// downloadAPI must match between the cfg and the storages mock route; we use
// "download" throughout.
func buildFullFlowServer(
	t *testing.T,
	storages *testsupport.StoragesMock,
	collabora *testsupport.CollaboraMock,
	c *cache.Cache,
) *httptest.Server {
	t.Helper()

	cfg := testCfg()
	// Point the real config at the in-process mocks.
	cfg.StorageDownloadAPI = "download"
	cfg.StorageFullAddress = storages.BaseURL()
	cfg.DocumentConversionFullConvertAddress = collabora.ConvertAddress()

	store := storage.NewDirectClient(cfg.StorageFullAddress, cfg.StorageDownloadAPI, "upload", "delete", 10*time.Second)

	s := New(cfg, store, c)
	// Use the public Handler(): same handler chain Run serves (mux + body
	// inactivity deadline + logging middleware), without a real listener or
	// signal handling. Render init is done once by InitRealRender(t).
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// getBody performs a GET and returns status, content-type and body bytes.
func getBody(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func TestFullFlow(t *testing.T) {
	// Initialise REAL render ONCE for the whole suite. Skips cleanly if
	// cgo/libpdfium/libvips/the worker is unavailable.
	testsupport.InitRealRender(t)

	fx := fullFlowFixtures{
		jpeg: testsupport.LoadFixture(testsupport.FixtureJPEG),
		png:  testsupport.LoadFixture(testsupport.FixturePNG),
		pdf:  testsupport.LoadFixture(testsupport.FixturePDF),
		doc:  testsupport.LoadFixture(testsupport.FixtureDoc),
	}

	previewURL := func(ts *httptest.Server, mime string) string {
		return ts.URL + "/preview/" + validUUID + "/1?service_type=files&mime=" + mime
	}
	thumbURL := func(ts *httptest.Server, size, mime string) string {
		return ts.URL + "/thumbnail/" + validUUID + "/1/" + size + "?service_type=files&mime=" + mime
	}
	start := func(t *testing.T, blob []byte, contentType string, c *cache.Cache) (*httptest.Server, *testsupport.StoragesMock, *testsupport.CollaboraMock) {
		t.Helper()
		storages := testsupport.NewStoragesMock("download", blob, contentType)
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		return buildFullFlowServer(t, storages, collabora, c), storages, collabora
	}

	t.Run("image_preview_is_passthrough", func(t *testing.T) {
		ts, _, _ := start(t, fx.jpeg, "image/jpeg", cache.New(0))
		code, ct, body := getBody(t, previewURL(ts, "image/jpeg"))
		if code != http.StatusOK || ct != "image/jpeg" || !bytes.Equal(body, fx.jpeg) {
			t.Fatalf("status=%d ct=%q identical=%v, want 200 image/jpeg byte-identical", code, ct, bytes.Equal(body, fx.jpeg))
		}
	})

	t.Run("image_thumbnail_alpha_png_is_png", func(t *testing.T) {
		src := pngOf(t, 1000, 600, true)
		ts, _, _ := start(t, src, "image/png", cache.New(0))
		code, ct, body := getBody(t, thumbURL(ts, "s", "image/png"))
		if code != http.StatusOK || ct != "image/png" {
			t.Fatalf("status=%d ct=%q, want 200 image/png", code, ct)
		}
		if w, h, f := decodeInfo(t, body); f != "png" || max(w, h) > 320 {
			t.Fatalf("decoded %dx%d %s, want a PNG with long side <= 320", w, h, f)
		}
	})

	t.Run("gif_preview_is_passthrough", func(t *testing.T) {
		original := animatedGIFFixture(t)
		ts, _, _ := start(t, original, "image/gif", cache.New(0))
		code, ct, body := getBody(t, previewURL(ts, "image/gif"))
		if code != http.StatusOK || ct != "image/gif" {
			t.Fatalf("status=%d ct=%q, want 200 image/gif", code, ct)
		}
		if !bytes.Equal(body, original) {
			t.Fatalf("body differs from the stored GIF (len %d vs %d, prefix=%X)", len(body), len(original), firstN(body, 8))
		}
		anim, err := gif.DecodeAll(bytes.NewReader(body))
		if err != nil || len(anim.Image) != 5 {
			t.Fatalf("decoded frames: %v err=%v, want 5", anim, err)
		}
	})

	t.Run("gif_thumbnails", func(t *testing.T) {
		ts, _, _ := start(t, gifOf(t, 600, 400, 1), "image/gif", cache.New(0))
		code, ct, body := getBody(t, thumbURL(ts, "s", "image/gif"))
		if code != http.StatusOK || (ct != "image/jpeg" && ct != "image/png") {
			t.Fatalf("static GIF thumbnail: status=%d ct=%q, want 200 jpeg or png", code, ct)
		}
		if w, h, _ := decodeInfo(t, body); max(w, h) != 320 {
			t.Errorf("static GIF thumbnail %dx%d, want long side 320", w, h)
		}

		ts, _, _ = start(t, animatedGIFFixture(t), "image/gif", cache.New(0))
		if code, _, _ := getBody(t, thumbURL(ts, "m", "image/gif")+"&digest=x"); code != http.StatusFailedDependency {
			t.Errorf("animated GIF thumbnail without a DB: status=%d, want 424", code)
		}
	})

	t.Run("pdf_preview_is_passthrough", func(t *testing.T) {
		ts, _, _ := start(t, fx.pdf, "application/pdf", cache.New(0))
		code, ct, body := getBody(t, previewURL(ts, "application/pdf"))
		if code != http.StatusOK || ct != "application/pdf" || !bytes.Equal(body, fx.pdf) {
			t.Fatalf("status=%d ct=%q identical=%v, want 200 application/pdf byte-identical", code, ct, bytes.Equal(body, fx.pdf))
		}
	})

	t.Run("pdf_thumbnail", func(t *testing.T) {
		ts, _, _ := start(t, fx.pdf, "application/pdf", cache.New(0))
		code, ct, body := getBody(t, thumbURL(ts, "m", "application/pdf"))
		if code != http.StatusOK || ct != "image/jpeg" {
			t.Fatalf("status=%d ct=%q, want 200 image/jpeg", code, ct)
		}
		if w, h, _ := decodeInfo(t, body); max(w, h) != 1280 {
			t.Errorf("M thumbnail %dx%d, want long side 1280", w, h)
		}
	})

	t.Run("document_preview_is_pdf", func(t *testing.T) {
		ts, _, collabora := start(t, fx.doc, "application/octet-stream", cache.New(0))
		code, ct, body := getBody(t, previewURL(ts, docxMime))
		if code != http.StatusOK || ct != "application/pdf" || !bytes.HasPrefix(body, []byte("%PDF")) {
			t.Fatalf("status=%d ct=%q prefix=%q, want 200 application/pdf", code, ct, firstN(body, 8))
		}
		if collabora.Hits() != 1 {
			t.Errorf("collabora hits=%d, want 1", collabora.Hits())
		}
	})

	t.Run("cache_hit_thumbnail", func(t *testing.T) {
		ts, storages, _ := start(t, fx.pdf, "application/pdf", cache.New(8<<20))
		url := thumbURL(ts, "m", "application/pdf")
		c1, ct1, b1 := getBody(t, url)
		c2, ct2, b2 := getBody(t, url)
		if c1 != http.StatusOK || c2 != http.StatusOK {
			t.Fatalf("status: first=%d second=%d, want 200/200", c1, c2)
		}
		if storages.Hits() != 1 {
			t.Errorf("storages hits=%d, want 1 (second served from real cache)", storages.Hits())
		}
		if ct1 != ct2 || !bytes.Equal(b1, b2) {
			t.Error("cached thumbnail differs from the first")
		}
	})

	t.Run("previews_are_never_cached", func(t *testing.T) {
		ts, storages, _ := start(t, fx.pdf, "application/pdf", cache.New(8<<20))
		for i := 0; i < 2; i++ {
			if code, _, _ := getBody(t, previewURL(ts, "application/pdf")); code != http.StatusOK {
				t.Fatalf("GET %d: status=%d", i, code)
			}
		}
		if storages.Hits() != 2 {
			t.Errorf("storages hits=%d, want 2", storages.Hits())
		}
	})

	t.Run("error_storages_404", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", nil, "")
		storages.Status = http.StatusNotFound
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		for _, url := range []string{previewURL(ts, "image/jpeg"), thumbURL(ts, "m", "image/jpeg")} {
			code, ct, body := getBody(t, url)
			if code != http.StatusNotFound {
				t.Fatalf("%s: status=%d, want 404", url, code)
			}
			if !hasPrefix(ct, "application/json") {
				t.Errorf("Content-Type=%q, want application/json", ct)
			}
			if !bytes.Contains(body, []byte(config.Msg.ItemNotFound)) {
				t.Errorf("body=%q does not contain ItemNotFound message %q", body, config.Msg.ItemNotFound)
			}
		}
	})

	t.Run("error_collabora_5xx", func(t *testing.T) {
		ts, _, collabora := start(t, fx.doc, "application/octet-stream", cache.New(0))
		collabora.Status = http.StatusInternalServerError

		code, ct, _ := getBody(t, previewURL(ts, docxMime))
		if code != http.StatusBadGateway {
			t.Fatalf("status=%d, want 502", code)
		}
		if !hasPrefix(ct, "application/json") {
			t.Errorf("Content-Type=%q, want application/json", ct)
		}
	})
}

func animatedGIFFixture(t *testing.T) []byte {
	t.Helper()
	colors := []color.RGBA{
		{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255}, {R: 255, G: 255, A: 255}, {R: 255, B: 255, A: 255},
	}
	anim := &gif.GIF{}
	for _, c := range colors {
		anim.Image = append(anim.Image, image.NewPaletted(image.Rect(0, 0, 200, 150), color.Palette{c}))
		anim.Delay = append(anim.Delay, 20)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		t.Fatalf("gif.EncodeAll: %v", err)
	}
	return buf.Bytes()
}

// firstN returns the first n bytes (or all of b if shorter), for diagnostics.
func firstN(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}

// hasPrefix is a tiny helper to avoid importing strings just for one call.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
