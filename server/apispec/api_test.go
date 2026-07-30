// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package apispec_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v3/server/apispec"
)

// These tests replace the ones that used to cover the hand-rolled
// server/docs.go handlers (which served an embedded, duplicated copy of the
// generated spec). The spec is now served by huma from the SAME operation
// registrations the binary routes traffic through, so it cannot go stale.
//
// They deliberately live in the cgo-free apispec package: package server is
// cgo-tainted through render (libvips/pdfium), while apispec is not, so this
// coverage runs anywhere.

// buildMux returns a mux with all operations registered via stubs and huma's
// spec/docs routes toggled by serveDocs.
func buildMux(serveDocs bool) *http.ServeMux {
	mux := http.NewServeMux()
	api := apispec.NewAPI(mux, serveDocs)
	// Registered AFTER NewAPI on purpose: huma marshals the spec lazily on the
	// first request, so these operations must still show up in it.
	apispec.RegisterStubs(api)
	return mux
}

func do(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// specRoutes are the four spec documents huma derives from the single
// OpenAPIPath base, plus their expected Content-Type.
var specRoutes = []struct {
	path        string
	contentType string
}{
	{"/openapi.json", "application/openapi+json"},
	{"/openapi.yaml", "application/openapi+yaml"},
	{"/openapi-3.0.json", "application/openapi+json"},
	{"/openapi-3.0.yaml", "application/openapi+yaml"},
}

// ─── enabled ─────────────────────────────────────────────────────────────────

// TestSpecRoutes_EnabledServeTheSpec verifies that with serveDocs=true every
// spec route returns 200 with the right Content-Type and a body describing a
// known operation.
func TestSpecRoutes_EnabledServeTheSpec(t *testing.T) {
	mux := buildMux(true)

	for _, r := range specRoutes {
		t.Run(r.path, func(t *testing.T) {
			rec := do(mux, http.MethodGet, r.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != r.contentType {
				t.Errorf("Content-Type = %q, want %q", ct, r.contentType)
			}
			// The spec must describe operations registered AFTER NewAPI.
			if !strings.Contains(rec.Body.String(), "getImagePreview") {
				t.Error("spec body does not mention operationId getImagePreview")
			}
		})
	}
}

// TestSpecRoutes_EnabledOASVersions verifies /openapi.json is OAS 3.1 and
// /openapi-3.0.json is the 3.0 downgrade — the same downgrade cmd/gendocs
// writes into docs/.
func TestSpecRoutes_EnabledOASVersions(t *testing.T) {
	mux := buildMux(true)

	for _, tc := range []struct{ path, wantPrefix string }{
		{"/openapi.json", "3.1"},
		{"/openapi-3.0.json", "3.0"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := do(mux, http.MethodGet, tc.path)
			var doc struct {
				OpenAPI string `json:"openapi"`
				Paths   map[string]struct {
					Get *struct {
						OperationID string `json:"operationId"`
					} `json:"get"`
				} `json:"paths"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("JSON unmarshal: %v", err)
			}
			if !strings.HasPrefix(doc.OpenAPI, tc.wantPrefix) {
				t.Errorf("openapi = %q, want prefix %q", doc.OpenAPI, tc.wantPrefix)
			}
			const wantPath = "/preview/image/{id}/{version}/{area}/"
			op, ok := doc.Paths[wantPath]
			if !ok {
				t.Fatalf("spec has no path %q", wantPath)
			}
			if op.Get == nil || op.Get.OperationID != "getImagePreview" {
				t.Errorf("GET %s operationId = %+v, want getImagePreview", wantPath, op.Get)
			}
		})
	}
}

// TestSpecRoutes_EnabledDoNotSelfDocument verifies the spec/docs routes are
// absent FROM the spec. huma registers them with adapter.Handle rather than
// huma.Register, which is why enabling them cannot change docs/openapi.yaml —
// and therefore cannot change the Java SDK generated from it.
func TestSpecRoutes_EnabledDoNotSelfDocument(t *testing.T) {
	mux := buildMux(true)
	rec := do(mux, http.MethodGet, "/openapi.json")

	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	for _, p := range []string{"/openapi", "/openapi.json", "/openapi.yaml", "/docs"} {
		if _, found := doc.Paths[p]; found {
			t.Errorf("spec documents its own route %q — it must not", p)
		}
	}
}

// TestDocsRoute_EnabledServesSwaggerUI verifies GET /docs returns the pinned
// Swagger UI page with a Content-Security-Policy header.
func TestDocsRoute_EnabledServesSwaggerUI(t *testing.T) {
	mux := buildMux(true)
	rec := do(mux, http.MethodGet, "/docs")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "swagger-ui") {
		t.Error("body does not reference swagger-ui")
	}
	// huma pins the swagger-ui-dist release and ships SRI hashes; the old
	// hand-rolled page loaded an unpinned CDN bundle with neither.
	if !strings.Contains(body, "integrity=") {
		t.Error("Swagger UI assets are loaded without subresource integrity")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
		t.Error("missing Content-Security-Policy header on the docs page")
	}
}

// TestSpecRoutes_EnabledRejectNonGET verifies the spec/docs routes are
// GET-only: huma registers them as "GET <path>" patterns, so Go's ServeMux
// answers any other method with 405 (behaviour retained from the old handlers).
func TestSpecRoutes_EnabledRejectNonGET(t *testing.T) {
	mux := buildMux(true)

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/openapi-3.0.json", "/openapi-3.0.yaml", "/docs"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			rec := do(mux, method, path)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, path, rec.Code)
			}
		}
	}
}

// ─── disabled (the production default) ───────────────────────────────────────

// TestSpecRoutes_DisabledAreNotRegistered verifies that with serveDocs=false —
// the default-off production state — every spec/docs route 404s.
func TestSpecRoutes_DisabledAreNotRegistered(t *testing.T) {
	mux := buildMux(false)

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/openapi-3.0.json", "/openapi-3.0.yaml", "/docs"} {
		t.Run(path, func(t *testing.T) {
			if rec := do(mux, http.MethodGet, path); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s: status = %d, want 404", path, rec.Code)
			}
			// Not merely method-gated: the pattern must not exist at all, so a
			// non-GET is a 404 too (a registered GET-only route would give 405).
			if rec := do(mux, http.MethodPost, path); rec.Code != http.StatusNotFound {
				t.Errorf("POST %s: status = %d, want 404", path, rec.Code)
			}
		})
	}
}

// TestSpecRoutes_DisabledLeaveOperationsRouted verifies the toggle gates ONLY
// the doc routes: the real API operations are registered either way.
func TestSpecRoutes_DisabledLeaveOperationsRouted(t *testing.T) {
	for _, serveDocs := range []bool{false, true} {
		rec := do(buildMux(serveDocs), http.MethodGet, "/health/live/")
		if rec.Code != http.StatusOK {
			t.Errorf("serveDocs=%v: GET /health/live/ status = %d, want 200", serveDocs, rec.Code)
		}
	}
}

// TestSchemasPath_NeverRegistered verifies huma's /schemas route stays off in
// both states: this API installs no SchemaLinkTransformer, so there is nothing
// for it to point at.
func TestSchemasPath_NeverRegistered(t *testing.T) {
	for _, serveDocs := range []bool{false, true} {
		rec := do(buildMux(serveDocs), http.MethodGet, "/schemas/ErrorModel.json")
		if rec.Code != http.StatusNotFound {
			t.Errorf("serveDocs=%v: GET /schemas/...: status = %d, want 404", serveDocs, rec.Code)
		}
	}
}
