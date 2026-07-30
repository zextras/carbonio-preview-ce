// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Spec/doc endpoint paths served by huma when NewAPI is called with
// serveDocs=true. They are constants, not operator knobs — only the on/off
// toggle is configurable (application key "openapi.enabled").
const (
	// OpenAPIPath is huma's spec-route BASE path. huma appends the format
	// suffix itself, so this single value produces four routes:
	//
	//	GET /openapi.json       — OAS 3.1.0 JSON
	//	GET /openapi.yaml       — OAS 3.1.0 YAML
	//	GET /openapi-3.0.json   — OAS 3.0.3 JSON (same downgrade cmd/gendocs writes)
	//	GET /openapi-3.0.yaml   — OAS 3.0.3 YAML (same downgrade cmd/gendocs writes)
	OpenAPIPath = "/openapi"

	// DocsPath is the Swagger UI page. huma serves it from a pinned
	// swagger-ui-dist release with subresource-integrity hashes and a
	// Content-Security-Policy header.
	DocsPath = "/docs"
)

// NewAPI constructs the huma API for this service over the given mux. It is the
// SINGLE place the huma.Config is built: both the live server (via
// server.newHumaAPI) and cmd/gendocs call it, so the spec the binary serves and
// the spec committed under docs/ can never drift from two hand-copied configs.
//
// The config is built from scratch rather than from huma.DefaultConfig because
// DefaultConfig installs the SchemaLinkTransformer via CreateHooks — it injects
// $schema fields into every response body, which conflicts with our
// FastAPI-compatible error model (no $schema, no Link headers). For the same
// reason SchemasPath stays empty: with no transformer there is nothing to point
// a /schemas route at.
//
// serveDocs selects whether huma registers its built-in spec/docs routes:
//
//   - false (the production default, and always for cmd/gendocs): OpenAPIPath,
//     DocsPath and SchemasPath are all "", which is exactly how huma is told to
//     register no built-in routes at all. The spec is then reachable only as the
//     build-time artefact under docs/.
//   - true: the four /openapi* spec routes plus the Swagger UI at /docs.
//
// Operations registered AFTER this call are still included in the served spec:
// huma marshals it lazily on the first request to a spec route.
func NewAPI(mux *http.ServeMux, serveDocs bool) huma.API {
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
		// Empty = huma registers no built-in route for that group.
		OpenAPIPath:   "",
		DocsPath:      "",
		SchemasPath:   "",
		Formats:       huma.DefaultFormats,
		DefaultFormat: "application/json",
		// No CreateHooks → no SchemaLinkTransformer → no $schema injection.
	}
	if serveDocs {
		cfg.OpenAPIPath = OpenAPIPath
		cfg.DocsPath = DocsPath
		cfg.DocsRenderer = huma.DocsRendererSwaggerUI
	}
	return humago.New(mux, cfg)
}
