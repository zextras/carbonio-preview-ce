// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// The committed OpenAPI artefacts under docs/ are generated FROM the huma
// handler registrations reachable from this file (RegisterOperations →
// server/apispec). Nothing in the code reads them back. To regenerate them
// after changing an operation, run:
//
//	go generate ./server/...
//
//go:generate go run ../cmd/gendocs

package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

// ---------------------------------------------------------------------------
// Deps bundles the server dependencies needed by huma handlers.
// ---------------------------------------------------------------------------

// Deps bundles the runtime dependencies injected into huma handlers.
// For gendocs (spec-only mode) all fields may be nil; handlers are never invoked.
type Deps struct {
	Cfg   *config.Config
	Store storage.Client
	Cache *cache.Cache
	// Limits bounds render (CPU) work and coalesces identical GETs; nil = unlimited (tests).
	Limits *limits
	// AsyncSem is the DEDICATED async semaphore (capacity =
	// async.max-concurrent-jobs, default NumCPU) bounding ffmpeg work
	// (codec probe and first-frame extraction) and Collabora conversions of async jobs. nil = unlimited (tests).
	AsyncSem chan struct{}
	// DB is the thumbnail database store.  nil means the thumbnail DB layer is
	// disabled (spec-only gendocs mode or unit tests that do not exercise the
	// asynchronous thumbnails). The async worker and thumbnail HTTP handlers must check for nil and
	// either skip or return an appropriate error.
	//
	// Prefer Deps.dbStore() over reading DB directly: when a DBGate is
	// attached (production) it is the authoritative, request-time readiness
	// signal; DB is the static fallback used by tests.
	DB *db.Store
	// DBGate, when non-nil, is the request-time readiness signal for the thumbnail
	// DB. Its store may start nil and flip to ready after boot (background init),
	// re-enabling asynchronous thumbnails with no restart. When nil, handlers fall back to DB.
	DBGate *dbGate
}

// ---------------------------------------------------------------------------
// RegisterOperations registers all huma-managed operations onto api.
// It is called by both the live server and cmd/gendocs.
// ---------------------------------------------------------------------------

// RegisterOperations registers all huma-managed operations onto api.
// It is called by both the live server and cmd/gendocs.
//
// The async worker is ALWAYS constructed (it is gate-aware: it no-ops each tick
// while the DB is not ready) and returned so the caller can start it once the DB
// is up. It is NOT started here — the server owns the worker lifecycle via
// Server.EnableDB / StartAsyncWorker so a transient DB outage at boot never
// blocks registration. Returns nil only in pure spec-only gendocs mode (no cfg).
func RegisterOperations(api huma.API, deps Deps) *AsyncWorker {
	apispec.RegisterHealthOps(api,
		buildGetHealthLive(), buildGetHealthReady(deps), buildGetHealth(deps))
	apispec.RegisterPreviewOps(api, buildGetPreview(deps), buildPostPreview(deps))

	// Build the worker whenever a config is present (spec-only gendocs has none).
	// The worker resolves its store through deps.dbStore() at tick time, so it
	// is safe to construct before the DB is ready; it processes rows only once the
	// gate flips.
	var w *AsyncWorker
	if deps.Cfg != nil {
		w = NewAsyncWorker(deps)
	}
	apispec.RegisterThumbnailOps(api, buildGetThumbnail(deps, w), buildPostThumbnail(deps), buildDeleteThumbnails(deps))
	return w
}

// newHumaAPI constructs a huma API over the given mux with all Carbonio
// settings, delegating to apispec.NewAPI — the single shared constructor also
// used by cmd/gendocs, so the committed docs/ artefacts are built from the same
// huma.Config the server runs on. It registers no spec or docs routes.
func newHumaAPI(mux *http.ServeMux) huma.API {
	return apispec.NewAPI(mux)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validateUUID checks that the UUID string is non-nil (all-zeros rejected).
// huma's format:uuid tag validates the UUID format; nil UUID is additionally rejected here.
func validateUUID(id string) (string, error) {
	// parseID already handles nil-UUID rejection via uuid.Nil check.
	return parseID(id)
}

// ---------------------------------------------------------------------------
// Health handler builders
// ---------------------------------------------------------------------------

func buildGetHealthLive() func(context.Context, *struct{}) (*apispec.HealthLiveOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*apispec.HealthLiveOutput, error) {
		return &apispec.HealthLiveOutput{}, nil
	}
}

func buildGetHealthReady(deps Deps) func(context.Context, *struct{}) (*apispec.HealthReadyOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*apispec.HealthReadyOutput, error) {
		// Readiness reports only whether this service itself is up; it must
		// not probe dependencies. carbonio-docs-editor and carbonio-storages
		// are OPTIONAL and reported for information by GET /health/. This
		// mirrors GET /health/live/ and the carbonio-tasks health pattern
		// (gate on liveness; never fail health on a dependency being down).
		return &apispec.HealthReadyOutput{}, nil
	}
}

func buildGetHealth(deps Deps) func(context.Context, *struct{}) (*apispec.HealthFullOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*apispec.HealthFullOutput, error) {
		cfg := deps.Cfg
		if cfg == nil {
			// gendocs mode — return empty response
			return &apispec.HealthFullOutput{Body: &apispec.HealthResponse{Ready: true}}, nil
		}
		docsUp := isDependencyUp(cfg.DocumentConversionFullServiceAddress)
		var dependencies []apispec.HealthDependency
		if hc, ok := deps.Store.(storage.HealthChecker); ok {
			name, url := hc.HealthTarget()
			up := isDependencyUp(url)
			dependencies = append(dependencies, apispec.HealthDependency{Name: name, Ready: up, Live: up, Type: "OPTIONAL"})
		}
		dependencies = append(dependencies, apispec.HealthDependency{Name: "carbonio-docs-editor", Ready: docsUp, Live: docsUp, Type: "OPTIONAL"})
		resp := &apispec.HealthResponse{Ready: true, Dependencies: dependencies}
		return &apispec.HealthFullOutput{Body: resp}, nil
	}
}
