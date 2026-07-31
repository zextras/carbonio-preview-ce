// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"testing"
)

// The route-level behaviour is covered exhaustively (and cgo-free) in
// server/apispec/api_test.go. These tests cover the wiring this package owns:
// that the REAL production mux, assembled through the real Server constructor,
// exposes no OpenAPI spec or documentation endpoint — with no way to turn one
// on. The spec lives in the repository (docs/openapi.yaml), generated from
// these same registrations; serving it would only echo what is already there.

// TestBuildMux_NoDocEndpoints verifies the production mux 404s on every spec
// and docs path huma could otherwise serve, and on the /redoc page the
// hand-rolled server/docs.go used to expose.
func TestBuildMux_NoDocEndpoints(t *testing.T) {
	mux := New(testCfg(), &mockStore{}, nil).buildMux(nil)

	for _, path := range []string{
		"/openapi.json",
		"/openapi.yaml",
		"/openapi-3.0.json",
		"/openapi-3.0.yaml",
		"/docs",
		// /redoc was a second, unpinned CDN documentation page; it is gone for
		// good rather than merely disabled.
		"/redoc",
	} {
		if rec := doRequest(mux, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", path, rec.Code)
		}
	}
}

// TestBuildMux_OperationsStillRouted verifies the production mux still routes
// the real operations — the doc endpoints are the only thing that went away.
func TestBuildMux_OperationsStillRouted(t *testing.T) {
	mux := New(testCfg(), &mockStore{}, nil).buildMux(nil)

	if rec := doRequest(mux, http.MethodGet, "/health/live/"); rec.Code != http.StatusOK {
		t.Errorf("GET /health/live/: status = %d, want 200", rec.Code)
	}
}
