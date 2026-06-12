// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	_ "embed"
	"log"
	"net/http"
)

// openAPIJSON holds the embedded OpenAPI spec JSON, generated from docs/openapi.yaml.
// The file is copied into server/static/ at build time by the Makefile / CI step
// (go:embed does not allow ../ paths, so the canonical copy lives in docs/openapi.json
// and a generated copy lives in server/static/openapi.json).
//
//go:embed static/openapi.json
var openAPIJSON []byte

// registerDocRoutes adds the three documentation endpoints to mux:
//
//	GET /openapi.json  — raw OpenAPI spec (application/json)
//	GET /docs          — Swagger UI (CDN, matching FastAPI defaults)
//	GET /redoc         — ReDoc UI (CDN, matching FastAPI defaults)
func registerDocRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/openapi.json", handleOpenAPIJSON)
	mux.HandleFunc("/docs", handleSwaggerUI)
	mux.HandleFunc("/redoc", handleReDocUI)
}

// handleOpenAPIJSON serves the embedded openapi.json.
func handleOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(openAPIJSON); err != nil {
		log.Printf("handleOpenAPIJSON write: %v", err)
	}
}

// handleSwaggerUI serves an HTML page that loads Swagger UI from the jsdelivr CDN.
// This mirrors FastAPI's default /docs page (cdn.jsdelivr.net/npm/swagger-ui-dist).
func handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	const html = `<!DOCTYPE html>
<html>
<head>
  <title>preview - Swagger UI</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" type="text/css" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist/swagger-ui.css" >
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist/swagger-ui-bundle.js"> </script>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist/swagger-ui-standalone-preset.js"> </script>
<script>
window.onload = function() {
  const ui = SwaggerUIBundle({
    url: "/openapi.json",
    dom_id: '#swagger-ui',
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    layout: "StandaloneLayout"
  })
  window.ui = ui
}
</script>
</body>
</html>`
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("handleSwaggerUI write: %v", err)
	}
}

// handleReDocUI serves an HTML page that loads ReDoc from the jsdelivr CDN.
// This mirrors FastAPI's default /redoc page.
func handleReDocUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	const html = `<!DOCTYPE html>
<html>
<head>
  <title>preview - ReDoc</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">
  <style>
    body { margin: 0; padding: 0; }
  </style>
</head>
<body>
  <redoc spec-url='/openapi.json'></redoc>
  <script src="https://cdn.jsdelivr.net/npm/redoc/bundles/redoc.standalone.js"> </script>
</body>
</html>`
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("handleReDocUI write: %v", err)
	}
}
