// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// buildDocsMux creates a mux with only the doc routes registered.
func buildDocsMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerDocRoutes(mux)
	return mux
}

// TestOpenAPIJSON_Status200 verifies GET /openapi.json returns 200 + application/json.
func TestOpenAPIJSON_Status200(t *testing.T) {
	mux := buildDocsMux()
	rec := doRequest(mux, http.MethodGet, "/openapi.json")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
}

// TestOpenAPIJSON_ValidJSON verifies the body is valid JSON with an "openapi" key.
func TestOpenAPIJSON_ValidJSON(t *testing.T) {
	mux := buildDocsMux()
	rec := doRequest(mux, http.MethodGet, "/openapi.json")

	var v map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("JSON unmarshal: %v (body truncated: %q)", err, rec.Body.String()[:min(200, rec.Body.Len())])
	}
	if _, ok := v["openapi"]; !ok {
		t.Error("response JSON missing 'openapi' key")
	}
}

// TestSwaggerUI_Status200 verifies GET /docs returns 200 + text/html.
func TestSwaggerUI_Status200(t *testing.T) {
	mux := buildDocsMux()
	rec := doRequest(mux, http.MethodGet, "/docs")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "swagger-ui") {
		t.Error("expected body to reference swagger-ui")
	}
}

// TestReDocUI_Status200 verifies GET /redoc returns 200 + text/html.
func TestReDocUI_Status200(t *testing.T) {
	mux := buildDocsMux()
	rec := doRequest(mux, http.MethodGet, "/redoc")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "redoc") {
		t.Error("expected body to reference redoc")
	}
}

// TestOpenAPIJSON_MethodNotAllowed verifies POST /openapi.json → 405.
func TestOpenAPIJSON_MethodNotAllowed(t *testing.T) {
	mux := buildDocsMux()
	rec := doRequest(mux, http.MethodPost, "/openapi.json")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// min returns the smaller of a or b (for body truncation in error messages).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
