// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command gendocs synchronises the OpenAPI specification artefacts.
//
// During the pilot phase it performs two tasks:
//
//  1. Generates the huma code-first spec (image + health ops) via
//     RegisterOperations → DowngradeYAML and writes it to
//     docs/openapi.huma-pilot.yaml for inspection.
//     This file does NOT overwrite the authoritative docs/openapi.yaml.
//
//  2. Reads the existing docs/openapi.json (the authoritative JSON copy,
//     last generated from docs/openapi.yaml) and re-emits it to
//     server/static/openapi.json so that the go:embed directive in
//     server/docs.go picks up the latest canonical spec without requiring
//     python3 or PyYAML.
//
// After full migration (all ops under huma) task 2 will be replaced by a
// direct DowngradeYAML/Downgrade write to docs/openapi.yaml, docs/openapi.json,
// and server/static/openapi.json.
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

	"github.com/zextras/carbonio-preview-ce/server"
)

func main() {
	root := repoRoot()

	pilotYAMLPath := filepath.Join(root, "docs", "openapi.huma-pilot.yaml")
	canonicalJSONPath := filepath.Join(root, "docs", "openapi.json")
	staticJSONPath := filepath.Join(root, "server", "static", "openapi.json")

	// ── Task 1: generate huma pilot spec ────────────────────────────────────
	pilotYAML := generatePilotSpec()
	if err := os.WriteFile(pilotYAMLPath, pilotYAML, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", pilotYAMLPath, err)
	}
	log.Printf("gendocs: wrote pilot spec to %s", pilotYAMLPath)

	// ── Task 2: copy canonical JSON → server/static (no python3 needed) ─────
	canonicalJSON, err := os.ReadFile(canonicalJSONPath)
	if err != nil {
		log.Fatalf("gendocs: read %s: %v (run python3 yamlToJSON first or commit docs/openapi.json)", canonicalJSONPath, err)
	}
	pretty := prettyJSON(canonicalJSON)
	if err := os.MkdirAll(filepath.Dir(staticJSONPath), 0o755); err != nil {
		log.Fatalf("gendocs: mkdir %s: %v", filepath.Dir(staticJSONPath), err)
	}
	if err := os.WriteFile(staticJSONPath, pretty, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", staticJSONPath, err)
	}
	log.Printf("gendocs: wrote %s", staticJSONPath)
}

// generatePilotSpec builds a throwaway huma API, registers image + health
// operations (the pilot scope), and returns the OAS 3.0.3 YAML bytes.
// The config is built from scratch to avoid the SchemaLinkTransformer that
// huma.DefaultConfig installs — it would inject $schema fields into responses.
func generatePilotSpec() []byte {
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
	api := humago.New(mux, cfg)

	// Pass nil Deps — handlers are never invoked in gendocs mode.
	server.RegisterOperations(api, server.Deps{})

	yamlBytes, err := api.OpenAPI().DowngradeYAML()
	if err != nil {
		log.Fatalf("gendocs: DowngradeYAML: %v", err)
	}
	return yamlBytes
}

// prettyJSON re-encodes raw JSON bytes with indentation for stable diffs.
func prettyJSON(raw []byte) []byte {
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
