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

	// ---- image preview ----
	t.Run("image_preview", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.jpeg, "image/jpeg")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/image/"+validUUID+"/1/100x100/?service_type=files")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "image/jpeg" {
			t.Errorf("Content-Type=%q, want image/jpeg", ct)
		}
		if len(body) == 0 {
			t.Error("empty rendered body")
		}
	})

	// ---- image thumbnail (PNG output) ----
	t.Run("image_thumbnail_png", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.png, "image/png")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/image/"+validUUID+"/1/50x50/thumbnail/?service_type=files&output_format=png")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "image/png" {
			t.Errorf("Content-Type=%q, want image/png", ct)
		}
		if len(body) == 0 {
			t.Error("empty rendered body")
		}
	})

	t.Run("image_preview_full_size_gif_is_original", func(t *testing.T) {
		original := animatedGIFFixture(t)
		storages := testsupport.NewStoragesMock("download", original, "image/gif")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/image/"+validUUID+"/1/0x0/?service_type=files&quality=high&output_format=gif")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "image/gif" {
			t.Errorf("Content-Type=%q, want image/gif", ct)
		}
		if !bytes.Equal(body, original) {
			t.Fatalf("body differs from the stored GIF (len %d vs %d, prefix=%X)", len(body), len(original), firstN(body, 8))
		}
		anim, err := gif.DecodeAll(bytes.NewReader(body))
		if err != nil || len(anim.Image) != 5 {
			t.Fatalf("decoded frames: %v err=%v, want 5", anim, err)
		}
	})

	t.Run("image_thumbnail_gif_stays_static", func(t *testing.T) {
		original := animatedGIFFixture(t)
		storages := testsupport.NewStoragesMock("download", original, "image/gif")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, _, body := getBody(t, ts.URL+"/preview/image/"+validUUID+"/1/80x80/thumbnail/?service_type=chats&quality=high&output_format=gif&shape=rectangular")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if bytes.Equal(body, original) {
			t.Fatal("thumbnail returned the original GIF")
		}
		c, _, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || c.Width != 80 || c.Height != 80 {
			t.Fatalf("decoded %+v err=%v, want 80x80", c, err)
		}
		if anim, err := gif.DecodeAll(bytes.NewReader(body)); err == nil && len(anim.Image) != 1 {
			t.Errorf("thumbnail frames=%d, want a static image", len(anim.Image))
		}
	})

	// ---- pdf preview ----
	t.Run("pdf_preview", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.pdf, "application/pdf")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/pdf/"+validUUID+"/1/?service_type=files")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "application/pdf" {
			t.Errorf("Content-Type=%q, want application/pdf", ct)
		}
		if len(body) == 0 {
			t.Error("empty sliced PDF body")
		}
		if !bytes.HasPrefix(body, []byte("%PDF")) {
			t.Errorf("body does not look like a PDF (prefix=%q)", firstN(body, 8))
		}
	})

	// ---- pdf thumbnail ----
	t.Run("pdf_thumbnail", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.pdf, "application/pdf")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/pdf/"+validUUID+"/1/100x100/thumbnail/?service_type=files")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "image/jpeg" {
			t.Errorf("Content-Type=%q, want image/jpeg (default output_format)", ct)
		}
		if len(body) == 0 {
			t.Error("empty rasterized thumbnail body")
		}
	})

	// ---- document preview (real Collabora mock → real PDF render) ----
	t.Run("document_preview", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.doc, "application/octet-stream")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/document/"+validUUID+"/1/?service_type=files")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "application/pdf" {
			t.Errorf("Content-Type=%q, want application/pdf", ct)
		}
		if len(body) == 0 {
			t.Error("empty document-preview body")
		}
		if collabora.Hits() != 1 {
			t.Errorf("collabora hits=%d, want 1", collabora.Hits())
		}
	})

	// ---- document thumbnail (real Collabora mock → real PDF rasterize) ----
	t.Run("document_thumbnail", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.doc, "application/octet-stream")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		collabora.PNG = fx.png
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		code, ct, body := getBody(t, ts.URL+"/preview/document/"+validUUID+"/1/100x100/thumbnail/?service_type=files")
		if code != http.StatusOK {
			t.Fatalf("status=%d, want 200", code)
		}
		if ct != "image/jpeg" {
			t.Errorf("Content-Type=%q, want image/jpeg (default output_format)", ct)
		}
		if len(body) == 0 {
			t.Error("empty document-thumbnail body")
		}
		cfgImg, format, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || format != "jpeg" || cfgImg.Width != 80 || cfgImg.Height != 80 {
			t.Errorf("thumbnail decode: cfg=%+v format=%q err=%v, want 80x80 jpeg", cfgImg, format, err)
		}
	})

	// ---- cache-hit full flow: image (same GET twice → storages hit once) ----
	t.Run("cache_hit_image", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.jpeg, "image/jpeg")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(8<<20))

		url := ts.URL + "/preview/image/" + validUUID + "/1/100x100/?service_type=files"
		c1, ct1, b1 := getBody(t, url)
		c2, ct2, b2 := getBody(t, url)
		if c1 != http.StatusOK || c2 != http.StatusOK {
			t.Fatalf("status: first=%d second=%d, want 200/200", c1, c2)
		}
		if storages.Hits() != 1 {
			t.Errorf("storages hits=%d, want 1 (second served from real cache)", storages.Hits())
		}
		if ct1 != ct2 {
			t.Errorf("content-type differs: %q vs %q", ct1, ct2)
		}
		if !bytes.Equal(b1, b2) {
			t.Error("cached body differs from first render")
		}
	})

	// ---- cache-hit full flow: pdf (same GET twice → storages hit once) ----
	t.Run("cache_hit_pdf", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.pdf, "application/pdf")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(8<<20))

		url := ts.URL + "/preview/pdf/" + validUUID + "/1/?service_type=files"
		c1, _, b1 := getBody(t, url)
		c2, _, b2 := getBody(t, url)
		if c1 != http.StatusOK || c2 != http.StatusOK {
			t.Fatalf("status: first=%d second=%d, want 200/200", c1, c2)
		}
		if storages.Hits() != 1 {
			t.Errorf("storages hits=%d, want 1 (second served from real cache)", storages.Hits())
		}
		if !bytes.Equal(b1, b2) {
			t.Error("cached PDF body differs from first slice")
		}
	})

	// ---- error: storages 404 → handler not-found (404) ----
	t.Run("error_storages_404", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", nil, "")
		storages.Status = http.StatusNotFound
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		// Verified against image.go:149-151 and pdf.go:141-143: storage
		// ErrNotFound maps to errNotFound → HTTP 404 with {"detail": ItemNotFound}.
		code, ct, body := getBody(t, ts.URL+"/preview/image/"+validUUID+"/1/100x100/?service_type=files")
		if code != http.StatusNotFound {
			t.Fatalf("status=%d, want 404", code)
		}
		if !hasPrefix(ct, "application/json") {
			t.Errorf("Content-Type=%q, want application/json", ct)
		}
		if !bytes.Contains(body, []byte(config.Msg.ItemNotFound)) {
			t.Errorf("body=%q does not contain ItemNotFound message %q", body, config.Msg.ItemNotFound)
		}
	})

	// ---- error: Collabora 5xx → 502 ----
	t.Run("error_collabora_5xx", func(t *testing.T) {
		storages := testsupport.NewStoragesMock("download", fx.doc, "application/octet-stream")
		t.Cleanup(storages.Close)
		collabora := testsupport.NewCollaboraMock(fx.pdf)
		collabora.Status = http.StatusInternalServerError
		t.Cleanup(collabora.Close)
		ts := buildFullFlowServer(t, storages, collabora, cache.New(0))

		// Verified against document.go:167-172: a Collabora failure maps to
		// errDetail(StatusBadGateway) → HTTP 502.
		code, ct, _ := getBody(t, ts.URL+"/preview/document/"+validUUID+"/1/?service_type=files")
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
