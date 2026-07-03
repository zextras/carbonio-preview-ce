// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v3/cache"
)

// Run is intentionally NOT unit-tested here: it calls render.InitVips, requires
// the carbonio-preview-pdfium-worker binary, and blocks on OS signals. It is
// exercised by the Phase 2 in-process full-flow tests that boot the real
// binary. This file covers everything around it: New, buildMux,
// loggingMiddleware and responseWriter.WriteHeader, driven through a real
// httptest.Server over the real buildMux.

// TestBuildMux_RoutesWired constructs a Server with New, builds the real mux,
// wraps it in loggingMiddleware, and serves it over an httptest.Server. It
// asserts that an image-preview route (with stubbed render + a mockStore) and
// the health/live route are both wired and reachable. A nil semaphore is
// allowed: the render stub ignores it.
func TestBuildMux_RoutesWired(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte("rendered-out"), nil)
	defer restore()

	s := New(testCfg(), store, cache.New(8<<20))
	mux := s.buildMux(nil)
	ts := httptest.NewServer(loggingMiddleware(mux))
	defer ts.Close()

	// Image preview route (drives the real handler + cache.Put through buildMux).
	resp, err := http.Get(ts.URL + "/preview/image/" + validUUID + "/1/100x100/?service_type=files")
	if err != nil {
		t.Fatalf("GET image preview: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("image preview status=%d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("image preview Content-Type=%q, want image/jpeg", ct)
	}
	if string(body) != "rendered-out" {
		t.Errorf("image preview body=%q, want rendered-out", string(body))
	}

	// Health live route is wired and always returns 200.
	hresp, err := http.Get(ts.URL + "/health/live/")
	if err != nil {
		t.Fatalf("GET health live: %v", err)
	}
	hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		t.Errorf("health live status=%d, want 200", hresp.StatusCode)
	}
}

// TestNew_ConstructsServer verifies New wires cfg/store/cache onto the Server.
func TestNew_ConstructsServer(t *testing.T) {
	cfg := testCfg()
	store := &mockStore{}
	c := cache.New(1 << 20)
	s := New(cfg, store, c)

	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.cfg != cfg {
		t.Error("cfg not stored")
	}
	if s.store != store {
		t.Error("store not stored")
	}
	if s.cache != c {
		t.Error("cache not stored")
	}
}

// TestLoggingMiddleware_CapturesNon200 drives a method-not-allowed path through
// the wrapped mux. This forces responseWriter.WriteHeader to record a non-200
// status (405) and exercises the loggingMiddleware wrapping for an error
// response.
func TestLoggingMiddleware_CapturesNon200(t *testing.T) {
	store := &mockStore{}
	s := New(testCfg(), store, nil)
	mux := s.buildMux(nil)
	ts := httptest.NewServer(loggingMiddleware(mux))
	defer ts.Close()

	// DELETE on an image route → 405 (the image router writes 405 directly).
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/preview/image/"+validUUID+"/1/100x100/?service_type=files", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", resp.StatusCode)
	}
}

// TestLoggingMiddleware_Captures404 drives an unmatched path through the
// wrapper, covering the WriteHeader status-capture for a 404 response.
// Note: huma registers trailing-slash patterns as subtree matches in Go 1.22
// ServeMux, so extra segments under /preview/image/... now route to the handler.
// We use a path prefix that has no registered route at all.
func TestLoggingMiddleware_Captures404(t *testing.T) {
	store := &mockStore{}
	s := New(testCfg(), store, nil)
	ts := httptest.NewServer(loggingMiddleware(s.buildMux(nil)))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/completely/unknown/path/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}
