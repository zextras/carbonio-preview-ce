// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"strings"
	"testing"
)

// The route-level behaviour of the huma-served spec/docs endpoints is covered
// exhaustively (and cgo-free) in server/apispec/api_test.go. These tests cover
// the wiring this package owns: that config.Config.OpenAPIEnabled — the
// "openapi.enabled" application key — actually reaches buildMux, in BOTH
// states, through the real Server constructor.

// buildMuxWithOpenAPI builds the real production mux for a server whose
// openapi.enabled flag is set to enabled.
func buildMuxWithOpenAPI(enabled bool) *http.ServeMux {
	cfg := testCfg()
	cfg.OpenAPIEnabled = enabled
	return New(cfg, &mockStore{}, nil).buildMux(nil)
}

// TestBuildMux_OpenAPIDisabledByDefault verifies that a Config built the normal
// way (testCfg never sets OpenAPIEnabled, exactly as an unconfigured install
// resolves it) exposes no spec or docs endpoint. This is the shipped default.
func TestBuildMux_OpenAPIDisabledByDefault(t *testing.T) {
	if cfg := testCfg(); cfg.OpenAPIEnabled {
		t.Fatal("testCfg() has OpenAPIEnabled=true; the default-off case is not being tested")
	}
	mux := buildMuxWithOpenAPI(false)

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/openapi-3.0.json", "/openapi-3.0.yaml", "/docs"} {
		if rec := doRequest(mux, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404 (spec endpoints are default-off)", path, rec.Code)
		}
	}
}

// TestBuildMux_OpenAPIEnabled verifies that flipping the key on makes the real
// mux serve the spec generated from the live registrations, plus Swagger UI.
func TestBuildMux_OpenAPIEnabled(t *testing.T) {
	mux := buildMuxWithOpenAPI(true)

	rec := doRequest(mux, http.MethodGet, "/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json: status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/openapi+json" {
		t.Errorf("Content-Type = %q, want application/openapi+json", ct)
	}
	// The spec must describe the operations the server actually routes.
	for _, opID := range []string{"getImagePreview", "getPdfPreview", "getDocumentPreview", "getVideoPreview", "getHealthLive"} {
		if !strings.Contains(rec.Body.String(), opID) {
			t.Errorf("served spec does not mention operationId %q", opID)
		}
	}

	if rec := doRequest(mux, http.MethodGet, "/docs"); rec.Code != http.StatusOK {
		t.Errorf("GET /docs: status = %d, want 200", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/openapi.json"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /openapi.json: status = %d, want 405", rec.Code)
	}
}

// TestBuildMux_RedocGone documents an intentional behaviour change: the
// hand-rolled /redoc page is removed. huma serves one UI (Swagger, pinned +
// SRI + CSP); ReDoc was a second unpinned CDN page with neither.
func TestBuildMux_RedocGone(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := buildMuxWithOpenAPI(enabled)
		if rec := doRequest(mux, http.MethodGet, "/redoc"); rec.Code != http.StatusNotFound {
			t.Errorf("openapi.enabled=%v: GET /redoc: status = %d, want 404", enabled, rec.Code)
		}
	}
}
