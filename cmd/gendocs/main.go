// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command gendocs generates the authoritative OpenAPI specification artefacts
// directly from the huma handler registrations in server/api.go.
//
// It writes three files in one pass:
//
//  1. docs/openapi.yaml  — OAS 3.0.3 YAML (human-readable, for diffs)
//  2. docs/openapi.json  — OAS 3.0.3 JSON (for SDK generators)
//  3. server/static/openapi.json — copy of the JSON picked up by go:embed in server/docs.go
//
// No Python, no PyYAML, no pre-existing JSON file required.
//
// Invoke via go:generate in server/docs.go:
//
//	//go:generate go run ../cmd/gendocs
//
// Or directly from the repo root:
//
//	go run ./cmd/gendocs
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/zextras/carbonio-preview-ce/server/apispec"
)

func main() {
	root := repoRoot()

	yamlPath := filepath.Join(root, "docs", "openapi.yaml")
	jsonPath := filepath.Join(root, "docs", "openapi.json")
	staticJSONPath := filepath.Join(root, "server", "static", "openapi.json")

	// Build a throwaway huma API, register all operations via stubs (cgo-free),
	// then downgrade to OAS 3.0.3.
	api := buildAPI()
	apispec.RegisterStubs(api)

	// ── YAML ────────────────────────────────────────────────────────────────
	yamlBytes, err := api.OpenAPI().DowngradeYAML()
	if err != nil {
		log.Fatalf("gendocs: DowngradeYAML: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(yamlPath), 0o755); err != nil {
		log.Fatalf("gendocs: mkdir %s: %v", filepath.Dir(yamlPath), err)
	}
	if err := os.WriteFile(yamlPath, yamlBytes, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", yamlPath, err)
	}
	log.Printf("gendocs: wrote %s", yamlPath)

	// ── JSON (from Downgrade, not a YAML→JSON conversion) ───────────────────
	jsonBytes, err := api.OpenAPI().Downgrade()
	if err != nil {
		log.Fatalf("gendocs: Downgrade: %v", err)
	}
	prettyJSON := prettyIndent(jsonBytes)

	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		log.Fatalf("gendocs: mkdir %s: %v", filepath.Dir(jsonPath), err)
	}
	if err := os.WriteFile(jsonPath, prettyJSON, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", jsonPath, err)
	}
	log.Printf("gendocs: wrote %s", jsonPath)

	// ── server/static/openapi.json (same bytes, picked up by go:embed) ──────
	if err := os.MkdirAll(filepath.Dir(staticJSONPath), 0o755); err != nil {
		log.Fatalf("gendocs: mkdir %s: %v", filepath.Dir(staticJSONPath), err)
	}
	if err := os.WriteFile(staticJSONPath, prettyJSON, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", staticJSONPath, err)
	}
	log.Printf("gendocs: wrote %s", staticJSONPath)
}

// buildAPI constructs a throwaway huma API over a fresh ServeMux.
// The config is built from scratch (same as server.newHumaAPI) to avoid the
// SchemaLinkTransformer that huma.DefaultConfig installs.
func buildAPI() huma.API {
	mux := http.NewServeMux()
	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	cfg := huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:       "preview",
				Version:     "latest",
				Description: "Preview service.",
			},
			Components: &huma.Components{
				Schemas: registry,
			},
		},
		OpenAPIPath:   "",
		DocsPath:      "",
		SchemasPath:   "",
		Formats:       huma.DefaultFormats,
		DefaultFormat: "application/json",
	}
	return humago.New(mux, cfg)
}

// prettyIndent re-encodes raw JSON bytes with 2-space indentation for stable diffs.
func prettyIndent(raw []byte) []byte {
	var obj interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		log.Fatalf("gendocs: JSON unmarshal: %v", err)
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		log.Fatalf("gendocs: JSON marshal: %v", err)
	}
	return append(out, '\n')
}

// repoRoot returns the directory containing go.mod, searching upward from cwd.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("gendocs: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatalf("gendocs: could not locate repo root (go.mod not found)")
		}
		dir = parent
	}
}
