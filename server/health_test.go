// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
)

// buildHealthMux registers health routes on a fresh mux using the provided cfg.
func buildHealthMux(cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	registerHealthRoutes(mux, cfg)
	return mux
}

// healthCfgWithDeps returns a test config pointing at the given storage and
// docs-editor URLs (used by health handlers).
func healthCfgWithDeps(storageAddr, docsAddr string) *config.Config {
	c := testCfg()
	c.StorageFullAddress = storageAddr
	c.StorageHealthCheck = "health/live"
	c.DocumentConversionFullServiceAddress = docsAddr
	c.AreDocsEnabled = true
	return c
}

// TestHealthLive_AlwaysOK verifies that GET /health/live/ always returns 200.
func TestHealthLive_AlwaysOK(t *testing.T) {
	cfg := testCfg()
	mux := buildHealthMux(cfg)

	rec := doRequest(mux, http.MethodGet, "/health/live/")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestHealthLive_MethodNotAllowed verifies POST /health/live/ → 405.
func TestHealthLive_MethodNotAllowed(t *testing.T) {
	cfg := testCfg()
	mux := buildHealthMux(cfg)

	rec := doRequest(mux, http.MethodPost, "/health/live/")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// TestHealthReady_DocsDisabled verifies that /health/ready/ returns 200 when
// AreDocsEnabled=false (no docs-editor check needed).
func TestHealthReady_DocsDisabled(t *testing.T) {
	c := testCfg()
	c.AreDocsEnabled = false
	mux := buildHealthMux(c)

	rec := doRequest(mux, http.MethodGet, "/health/ready/")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestHealthReady_DocsUp verifies that /health/ready/ returns 200 when
// docs-editor responds with 200.
func TestHealthReady_DocsUp(t *testing.T) {
	// Start a fake docs-editor that returns 200.
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer docs.Close()

	cfg := healthCfgWithDeps("http://127.0.0.1:20000", docs.URL+"/")
	mux := buildHealthMux(cfg)

	rec := doRequest(mux, http.MethodGet, "/health/ready/")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestHealthReady_DocsDown verifies that /health/ready/ returns 429 with the
// DocsEditorUnavailable message when docs-editor is unreachable.
func TestHealthReady_DocsDown(t *testing.T) {
	// Use a server that is immediately closed (connection refused).
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	docs.Close()

	cfg := healthCfgWithDeps("http://127.0.0.1:20000", docs.URL+"/")
	mux := buildHealthMux(cfg)

	rec := doRequest(mux, http.MethodGet, "/health/ready/")

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status: got %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), config.Msg.DocsEditorUnavailable) {
		t.Errorf("body %q does not contain %q", rec.Body.String(), config.Msg.DocsEditorUnavailable)
	}
}

// TestHealthFull_JSONStructure verifies GET /health/ returns 200 with the
// exact old-Python JSON shape:
//
//	{
//	  "ready": true,
//	  "dependencies": [
//	    {"name":"carbonio-storages","ready":true,"live":true,"type":"OPTIONAL"},
//	    ...
//	  ]
//	}
func TestHealthFull_JSONStructure(t *testing.T) {
	// Start fake storage and docs-editor servers.
	storageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer storageSrv.Close()

	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer docs.Close()

	cfg := healthCfgWithDeps(storageSrv.URL, docs.URL+"/")
	mux := buildHealthMux(cfg)

	rec := doRequest(mux, http.MethodGet, "/health/")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("JSON unmarshal: %v (body: %q)", err, rec.Body.String())
	}
	if len(resp.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(resp.Dependencies))
	}

	byName := map[string]healthDependency{}
	for _, dep := range resp.Dependencies {
		byName[dep.Name] = dep
	}

	// carbonio-storages
	storage, ok := byName["carbonio-storages"]
	if !ok {
		t.Error("missing dependency 'carbonio-storages'")
	} else {
		if !storage.Ready {
			t.Error("carbonio-storages: ready should be true")
		}
		if !storage.Live {
			t.Error("carbonio-storages: live should be true")
		}
		if storage.Type != "OPTIONAL" {
			t.Errorf("carbonio-storages: type got %q, want OPTIONAL", storage.Type)
		}
	}

	// carbonio-docs-editor
	docs2, ok := byName["carbonio-docs-editor"]
	if !ok {
		t.Error("missing dependency 'carbonio-docs-editor'")
	} else {
		if !docs2.Ready {
			t.Error("carbonio-docs-editor: ready should be true")
		}
		if !docs2.Live {
			t.Error("carbonio-docs-editor: live should be true")
		}
		if docs2.Type != "OPTIONAL" {
			t.Errorf("carbonio-docs-editor: type got %q, want OPTIONAL", docs2.Type)
		}
	}
}

// TestHealthFull_StorageDown verifies that /health/ marks storage as
// unhealthy (ready=false, live=false) when storage is unreachable.
func TestHealthFull_StorageDown(t *testing.T) {
	storageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	storageSrv.Close()

	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer docs.Close()

	cfg := healthCfgWithDeps(storageSrv.URL, docs.URL+"/")
	mux := buildHealthMux(cfg)

	rec := doRequest(mux, http.MethodGet, "/health/")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (health endpoint always returns 200)", rec.Code)
	}

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	for _, dep := range resp.Dependencies {
		if dep.Name == "carbonio-storages" {
			if dep.Ready {
				t.Error("carbonio-storages should be ready=false when server is down")
			}
			if dep.Live {
				t.Error("carbonio-storages should be live=false when server is down")
			}
		}
	}
}

// TestHealthFull_AlwaysHTTP200 verifies that /health/ always returns HTTP 200
// regardless of dependency status (Python spec: "Always 200").
func TestHealthFull_AlwaysHTTP200(t *testing.T) {
	// All deps down.
	c := testCfg()
	c.StorageFullAddress = "http://127.0.0.1:1" // un-routable
	c.DocumentConversionFullServiceAddress = "http://127.0.0.1:1/"
	mux := buildHealthMux(c)

	rec := doRequest(mux, http.MethodGet, "/health/")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestHealthFull_ExactJSONDependencyNames verifies the JSON response uses the
// exact dependency names expected by monitoring agents.
func TestHealthFull_ExactJSONDependencyNames(t *testing.T) {
	c := testCfg()
	c.StorageFullAddress = "http://127.0.0.1:1"
	c.DocumentConversionFullServiceAddress = "http://127.0.0.1:1/"
	mux := buildHealthMux(c)

	rec := doRequest(mux, http.MethodGet, "/health/")

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	found := map[string]bool{}
	for _, dep := range resp.Dependencies {
		found[dep.Name] = true
		if dep.Type != "OPTIONAL" {
			t.Errorf("dep %q: type got %q, want OPTIONAL", dep.Name, dep.Type)
		}
	}

	if !found["carbonio-storages"] {
		t.Error("expected dependency named 'carbonio-storages'")
	}
	if !found["carbonio-docs-editor"] {
		t.Error("expected dependency named 'carbonio-docs-editor'")
	}
}

// TestHealthFull_SubPathReturns404 verifies that /health/unknown/ returns 404 JSON
// (the handler only responds to exactly /health/).
func TestHealthFull_SubPathReturns404(t *testing.T) {
	c := testCfg()
	mux := buildHealthMux(c)

	rec := doRequest(mux, http.MethodGet, "/health/unknown/")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	// Body must be JSON {"detail":"Not Found"}
	assertStringDetail(t, rec, http.StatusNotFound, "Not Found")
}

// TestHealthFull_DependencyTypeField verifies that "type":"OPTIONAL" is present
// in all dependency objects (new field vs old schema).
func TestHealthFull_DependencyTypeField(t *testing.T) {
	c := testCfg()
	c.StorageFullAddress = "http://127.0.0.1:1"
	c.DocumentConversionFullServiceAddress = "http://127.0.0.1:1/"
	mux := buildHealthMux(c)

	rec := doRequest(mux, http.MethodGet, "/health/")

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	for _, dep := range resp.Dependencies {
		if dep.Type != "OPTIONAL" {
			t.Errorf("dep %q: type=%q, want OPTIONAL", dep.Name, dep.Type)
		}
	}
}
