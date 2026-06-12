// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
)

// registerHealthRoutes registers the /health/* endpoints onto mux.
// The prefix is "/{health_name}" where health_name defaults to "health".
func registerHealthRoutes(mux *http.ServeMux, cfg *config.Config) {
	base := "/" + cfg.ServiceHealthName

	// GET /health/live/ — always 200, no deps checked.
	mux.HandleFunc(base+"/live/", healthLive)

	// GET /health/ready/ — 200 if docs up (or docs disabled); 429 otherwise.
	mux.HandleFunc(base+"/ready/", func(w http.ResponseWriter, r *http.Request) {
		healthReady(w, r, cfg)
	})

	// GET /health/ — JSON summary.
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		healthFull(w, r, cfg)
	})
}

// healthLive handles GET /health/live/
// Always returns HTTP 200. No body required but we follow the Python response pattern.
func healthLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// healthReady handles GET /health/ready/
// Returns 200 if docs-editor is reachable (or docs are disabled).
// Returns 429 + DOCS_EDITOR_UNAVAILABLE_STRING if docs are enabled but down.
func healthReady(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// If neither document preview nor thumbnail is enabled, skip the check.
	if !cfg.AreDocsEnabled {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Check that docs-editor is reachable.
	if isDependencyUp(cfg.DocumentConversionFullServiceAddress) {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests) // 429
	if _, err := fmt.Fprint(w, config.Msg.DocsEditorUnavailable); err != nil {
		log.Printf("healthReady write: %v", err)
	}
}

// healthDependency is the JSON object for a single dependency in /health/.
// Shape mirrors the old FastAPI implementation (health.py:50-66).
type healthDependency struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	Live  bool   `json:"live"`
	Type  string `json:"type"`
}

// healthResponse is the JSON body for GET /health/.
type healthResponse struct {
	Ready        bool               `json:"ready"`
	Dependencies []healthDependency `json:"dependencies"`
}

// healthFull handles GET /health/
// Always returns HTTP 200 with a JSON dict of dependency statuses.
func healthFull(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Only respond to the exact /health/ path, not sub-paths.
	if r.URL.Path != "/"+cfg.ServiceHealthName+"/" {
		writeJSON(w, http.StatusNotFound, stringDetailBody{Detail: "Not Found"})
		return
	}

	storageHealthURL := cfg.StorageFullAddress + "/" + cfg.StorageHealthCheck
	docsHealthURL := cfg.DocumentConversionFullServiceAddress

	storageUp := isDependencyUp(storageHealthURL)
	docsUp := isDependencyUp(docsHealthURL)

	resp := healthResponse{
		Ready: true,
		Dependencies: []healthDependency{
			{Name: "carbonio-storages", Ready: storageUp, Live: storageUp, Type: "OPTIONAL"},
			{Name: "carbonio-docs-editor", Ready: docsUp, Live: docsUp, Type: "OPTIONAL"},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("healthFull encode: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(data); werr != nil {
		log.Printf("healthFull write: %v", werr)
	}
}

// isDependencyUp performs a GET to url with a 5-second timeout.
// Returns true if the response is HTTP 2xx.
func isDependencyUp(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
