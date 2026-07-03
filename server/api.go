// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/zextras/carbonio-preview-ce/v2/cache"
	"github.com/zextras/carbonio-preview-ce/v2/config"
	"github.com/zextras/carbonio-preview-ce/v2/db"
	"github.com/zextras/carbonio-preview-ce/v2/render"
	"github.com/zextras/carbonio-preview-ce/v2/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v2/storage"
	"github.com/zextras/carbonio-preview-ce/v2/video"
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
	// Sem is the shared render semaphore (image/PDF/document ops), sized by the
	// render.max-concurrent-operations key.
	Sem chan struct{}
	// VideoSem is the DEDICATED video semaphore (capacity =
	// video.max-concurrent-extractions, default NumCPU). Separate from Sem so a
	// flood of video jobs cannot starve image previews. nil = unlimited (tests).
	VideoSem chan struct{}
	// DB is the video_preview database store.  nil means the video DB layer is
	// disabled (spec-only gendocs mode or unit tests that do not exercise video
	// endpoints). The video worker and video HTTP handlers must check for nil and
	// either skip or return an appropriate error.
	//
	// Prefer Deps.videoStore() over reading DB directly: when a DBGate is
	// attached (production) it is the authoritative, request-time readiness
	// signal; DB is the static fallback used by tests.
	DB *db.Store
	// DBGate, when non-nil, is the request-time readiness signal for the video
	// DB. Its store may start nil and flip to ready after boot (background init),
	// re-enabling video with no restart. When nil, handlers fall back to DB.
	DBGate *videoGate
}

// ---------------------------------------------------------------------------
// RegisterOperations registers all huma-managed operations onto api.
// It is called by both the live server and cmd/gendocs.
// ---------------------------------------------------------------------------

// RegisterOperations registers all huma-managed operations onto api.
// It is called by both the live server and cmd/gendocs.
//
// The video worker is ALWAYS constructed (it is gate-aware: it no-ops each tick
// while the DB is not ready) and returned so the caller can start it once the DB
// is up. It is NOT started here — the server owns the worker lifecycle via
// Server.EnableVideoDB / StartVideoWorker so a transient DB outage at boot never
// blocks registration. Returns nil only in pure spec-only gendocs mode (no cfg).
func RegisterOperations(api huma.API, deps Deps) *VideoWorker {
	semMW := semaphoreMiddleware(api, deps.Sem)

	apispec.RegisterImageOps(api,
		buildGetImagePreview(deps), buildGetImageThumbnail(deps),
		buildPostImagePreview(deps), buildPostImageThumbnail(deps),
		semMW)
	apispec.RegisterHealthOps(api,
		buildGetHealthLive(), buildGetHealthReady(deps), buildGetHealth(deps))
	apispec.RegisterPDFOps(api,
		buildGetPDFPreview(deps), buildGetPDFThumbnail(deps),
		buildPostPDFPreview(deps), buildPostPDFThumbnail(deps),
		semMW)
	apispec.RegisterDocumentOps(api,
		buildGetDocumentPreview(deps), buildGetDocumentThumbnail(deps),
		buildPostDocumentPreview(deps), buildPostDocumentThumbnail(deps),
		semMW)

	// Video GET / DELETE / copy endpoints (Q5: generate endpoint removed).
	// Build the worker whenever a config is present (spec-only gendocs has none).
	// The worker resolves its store through deps.videoStore() at tick time, so it
	// is safe to construct before the DB is ready; it processes rows only once the
	// gate flips.
	var w *VideoWorker
	if deps.Cfg != nil {
		w = NewVideoWorker(deps)
	}
	apispec.RegisterVideoOps(api,
		buildGetVideoPreview(deps, w),
		buildGetVideoThumbnail(deps, w),
		buildDeleteVideoPreview(deps),
		buildCopyVideoPreview(deps),
	)
	return w
}

// newHumaAPI constructs a huma API over the given mux with all Carbonio settings.
// We build the config from scratch (not huma.DefaultConfig) to avoid the
// SchemaLinkTransformer that DefaultConfig installs via CreateHooks — it
// injects $schema fields into every response body, which conflicts with our
// FastAPI-compatible error model (no $schema, no Link headers).
func newHumaAPI(mux *http.ServeMux) huma.API {
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
		// All three doc/schema paths are "" → huma registers no built-in routes.
		OpenAPIPath:   "",
		DocsPath:      "",
		SchemasPath:   "",
		Formats:       huma.DefaultFormats,
		DefaultFormat: "application/json",
		// No CreateHooks → no SchemaLinkTransformer → no $schema injection.
	}
	return humago.New(mux, cfg)
}

// ---------------------------------------------------------------------------
// Semaphore middleware (image render ops only)
// ---------------------------------------------------------------------------

// semBusyWait is the maximum time a request will wait to acquire a render slot
// before failing fast with 503. It is intentionally short: a request that
// cannot get a slot quickly is better off being told to retry than blocking
// until the client's read-timeout (the WSC client abandons at ~15s). The
// request context deadline still bounds the wait if it is shorter than this.
const semBusyWait = 2 * time.Second

// semaphoreMiddleware returns a huma middleware that acquires the render
// semaphore before the handler and releases it AFTER, on every exit path.
// Pass sem=nil for unlimited concurrency (tests / gendocs).
//
// This is the SINGLE acquisition point for the shared render concurrency
// limiter. The underlying render functions (render.ImageThumbnail,
// render.PDFSlice, render.PDFRasterize) must be called with a nil semaphore so
// they do NOT re-acquire — acquiring the same slot twice per request (here AND
// inside the render call) deadlocks the limiter under burst load and wedges the
// service until restart.
//
// Acquisition is context-aware and bounded:
//   - If the request context is already cancelled, we do NOT acquire and return
//     503 immediately (an abandoned request must not take a slot it cannot use).
//   - If no slot is free within semBusyWait (or the ctx is cancelled first), we
//     fail fast with 503 "server busy" WITHOUT acquiring, instead of blocking
//     until the client's read-timeout.
//   - On success the slot is released via defer, so it is returned on every
//     normal return, error return, ctx-cancel, and panic.
func semaphoreMiddleware(api huma.API, sem chan struct{}) func(huma.Context, func(huma.Context)) {
	return func(hctx huma.Context, next func(huma.Context)) {
		if sem == nil {
			next(hctx)
			return
		}

		ctx := hctx.Context()
		// Reject a request whose context is already cancelled before competing
		// for a slot.
		if err := ctx.Err(); err != nil {
			_ = huma.WriteErr(api, hctx, http.StatusServiceUnavailable, "server busy, retry")
			return
		}

		timer := time.NewTimer(semBusyWait)
		defer timer.Stop()

		select {
		case sem <- struct{}{}:
			// Acquired: release unconditionally on every exit path.
			defer func() { <-sem }()
			next(hctx)
		case <-ctx.Done():
			// Client disconnected / deadline exceeded while waiting — never
			// acquired, so nothing to release.
			_ = huma.WriteErr(api, hctx, http.StatusServiceUnavailable, "server busy, retry")
		case <-timer.C:
			// All slots busy and none freed in time — fail fast, never acquired.
			_ = huma.WriteErr(api, hctx, http.StatusServiceUnavailable, "server busy, retry")
		}
	}
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

// readHumaFile reads bytes from a huma multipart file field, enforcing non-empty.
// Returns (nil, error) if the file is absent or empty — the caller maps this to 422.
func readHumaFile(data *apispec.ImagePostFiles) ([]byte, error) {
	if data == nil || !data.File.IsSet {
		return nil, errors.New("file required")
	}
	defer data.File.Close()
	b, err := io.ReadAll(data.File)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("empty file")
	}
	return b, nil
}

// readHumaPDFFile reads bytes from a huma multipart file field for PDF operations.
// Returns (nil, error) if the file is absent or empty — the caller maps this to 422.
func readHumaPDFFile(data *apispec.PDFPostFiles) ([]byte, error) {
	if data == nil || !data.File.IsSet {
		return nil, errors.New("file required")
	}
	defer data.File.Close()
	b, err := io.ReadAll(data.File)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("empty file")
	}
	return b, nil
}

// readHumaDocFile reads bytes from a huma multipart file field for document operations.
// Returns (nil, error) if the file is absent or empty — the caller maps this to 422.
func readHumaDocFile(data *apispec.DocPostFiles) ([]byte, error) {
	if data == nil || !data.File.IsSet {
		return nil, errors.New("file required")
	}
	defer data.File.Close()
	b, err := io.ReadAll(data.File)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("empty file")
	}
	return b, nil
}

// validatePages checks the cross-field page combination rule, mirroring the
// legacy parsePages behaviour exactly:
//   - first_page must be >= 1
//   - last_page must be >= 0
//   - if last_page != 0, first_page must be <= last_page
//
// All violations return the same string-detail 422 (config.Msg.NumberOfPagesNotValid)
// with no *huma.ErrorDetail args, so the body is {"detail":"Pages must be at least 1."}
// — identical to the legacy errDetail(w, 422, NumberOfPagesNotValid) shape.
func validatePages(firstPage, lastPage int) error {
	if firstPage < 1 || lastPage < 0 || (lastPage != 0 && firstPage > lastPage) {
		return huma.NewError(http.StatusUnprocessableEntity, config.Msg.NumberOfPagesNotValid)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Image handler builders
// ---------------------------------------------------------------------------

func buildGetImagePreview(deps Deps) func(context.Context, *apispec.ImageGetPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.ImageGetPreviewInput) (*apispec.BinOut, error) {
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		serviceType := string(input.ServiceType)
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		crop := input.Crop

		ownerHeader := input.FileOwnerID
		key := cacheKey("img-preview", id, input.Version, serviceType, width, height, quality, outputFormat, crop, "rectangular", 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}

		cropMode := "none"
		if crop {
			cropMode = "center"
		}
		out, rerr := imageThumbnailFunc(nil, data, width, height, outputFormat, quality, "rectangular", cropMode)
		if rerr != nil {
			log.Printf("getImagePreview render: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		ct := contentTypeForFormat(outputFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &apispec.BinOut{ContentType: ct, Body: out}, nil
	}
}

func buildGetImageThumbnail(deps Deps) func(context.Context, *apispec.ImageGetThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.ImageGetThumbnailInput) (*apispec.BinOut, error) {
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		serviceType := string(input.ServiceType)
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)

		ownerHeader := input.FileOwnerID
		key := cacheKey("img-thumb", id, input.Version, serviceType, width, height, quality, outputFormat, true, shape, 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}

		out, rerr := imageThumbnailFunc(nil, data, width, height, outputFormat, quality, shape, "center")
		if rerr != nil {
			log.Printf("getImageThumbnail render: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		ct := contentTypeForFormat(outputFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &apispec.BinOut{ContentType: ct, Body: out}, nil
	}
}

func buildPostImagePreview(deps Deps) func(context.Context, *apispec.ImagePostPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.ImagePostPreviewInput) (*apispec.BinOut, error) {
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		crop := input.Crop

		fileData, ferr := readHumaFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		cropMode := "none"
		if crop {
			cropMode = "center"
		}
		out, rerr := imageThumbnailFunc(nil, fileData, width, height, outputFormat, quality, "rectangular", cropMode)
		if rerr != nil {
			log.Printf("postImagePreview render: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		return &apispec.BinOut{ContentType: contentTypeForFormat(outputFormat), Body: out}, nil
	}
}

func buildPostImageThumbnail(deps Deps) func(context.Context, *apispec.ImagePostThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.ImagePostThumbnailInput) (*apispec.BinOut, error) {
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)

		fileData, ferr := readHumaFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		out, rerr := imageThumbnailFunc(nil, fileData, width, height, outputFormat, quality, shape, "center")
		if rerr != nil {
			log.Printf("postImageThumbnail render: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		return &apispec.BinOut{ContentType: contentTypeForFormat(outputFormat), Body: out}, nil
	}
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
		cfg := deps.Cfg
		if cfg == nil || !cfg.AreDocsEnabled {
			return &apispec.HealthReadyOutput{}, nil
		}
		if isDependencyUp(cfg.DocumentConversionFullServiceAddress) {
			return &apispec.HealthReadyOutput{}, nil
		}
		// Return 429 via huma error — huma will write the status; the body
		// will be plain text matching the legacy handler.
		return nil, huma.NewError(http.StatusTooManyRequests, config.Msg.DocsEditorUnavailable)
	}
}

func buildGetHealth(deps Deps) func(context.Context, *struct{}) (*apispec.HealthFullOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*apispec.HealthFullOutput, error) {
		cfg := deps.Cfg
		if cfg == nil {
			// gendocs mode — return empty response
			return &apispec.HealthFullOutput{Body: &apispec.HealthResponse{Ready: true}}, nil
		}
		storageHealthURL := cfg.StorageFullAddress + "/" + cfg.StorageHealthCheck
		docsHealthURL := cfg.DocumentConversionFullServiceAddress
		storageUp := isDependencyUp(storageHealthURL)
		docsUp := isDependencyUp(docsHealthURL)
		resp := &apispec.HealthResponse{
			Ready: true,
			Dependencies: []apispec.HealthDependency{
				{Name: "carbonio-storages", Ready: storageUp, Live: storageUp, Type: "OPTIONAL"},
				{Name: "carbonio-docs-editor", Ready: docsUp, Live: docsUp, Type: "OPTIONAL"},
			},
		}
		return &apispec.HealthFullOutput{Body: resp}, nil
	}
}

// ---------------------------------------------------------------------------
// PDF handler builders
// ---------------------------------------------------------------------------

func buildGetPDFPreview(deps Deps) func(context.Context, *apispec.PDFGetPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.PDFGetPreviewInput) (*apispec.BinOut, error) {
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		if err := validatePages(input.FirstPage, input.LastPage); err != nil {
			return nil, err
		}

		serviceType := string(input.ServiceType)
		ownerHeader := input.FileOwnerID
		key := cacheKey("pdf-preview", id, input.Version, serviceType, 0, 0, "medium", "jpeg", false, "rectangular", input.FirstPage, input.LastPage, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}

		sliced, rerr := pdfSliceFunc(nil, data, input.FirstPage, input.LastPage)
		if rerr != nil {
			log.Printf("getPdfPreview: PDFSlice: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		deps.Cache.Put(key, cache.Entry{Body: sliced, ContentType: "application/pdf"})
		return &apispec.BinOut{ContentType: "application/pdf", Body: sliced}, nil
	}
}

func buildGetPDFThumbnail(deps Deps) func(context.Context, *apispec.PDFGetThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.PDFGetThumbnailInput) (*apispec.BinOut, error) {
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		serviceType := string(input.ServiceType)
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)
		ownerHeader := input.FileOwnerID

		key := cacheKey("pdf-thumb", id, input.Version, serviceType, width, height, quality, outputFormat, false, shape, 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}

		out, rerr := pdfRasterizeFunc(nil, data, 0, width, height, outputFormat, quality, shape)
		if rerr != nil {
			log.Printf("getPdfThumbnail: PDFRasterize: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		// rounded shape forces PNG
		actualFormat := outputFormat
		if shape == "rounded" {
			actualFormat = "png"
		}
		ct := contentTypeForFormat(actualFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &apispec.BinOut{ContentType: ct, Body: out}, nil
	}
}

func buildPostPDFPreview(deps Deps) func(context.Context, *apispec.PDFPostPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.PDFPostPreviewInput) (*apispec.BinOut, error) {
		if err := validatePages(input.FirstPage, input.LastPage); err != nil {
			return nil, err
		}

		fileData, ferr := readHumaPDFFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		sliced, rerr := pdfSliceFunc(nil, fileData, input.FirstPage, input.LastPage)
		if rerr != nil {
			log.Printf("postPdfPreview: PDFSlice: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		return &apispec.BinOut{ContentType: "application/pdf", Body: sliced}, nil
	}
}

func buildPostPDFThumbnail(deps Deps) func(context.Context, *apispec.PDFPostThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.PDFPostThumbnailInput) (*apispec.BinOut, error) {
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)

		fileData, ferr := readHumaPDFFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		out, rerr := pdfRasterizeFunc(nil, fileData, 0, width, height, outputFormat, quality, shape)
		if rerr != nil {
			log.Printf("postPdfThumbnail: PDFRasterize: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		actualFormat := outputFormat
		if shape == "rounded" {
			actualFormat = "png"
		}
		return &apispec.BinOut{ContentType: contentTypeForFormat(actualFormat), Body: out}, nil
	}
}

// ---------------------------------------------------------------------------
// Document handler builders
// ---------------------------------------------------------------------------

func buildGetDocumentPreview(deps Deps) func(context.Context, *apispec.DocGetPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.DocGetPreviewInput) (*apispec.BinOut, error) {
		// Document-enable gate (#17)
		if deps.Cfg != nil && !deps.Cfg.ServiceEnableDocumentPreview {
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
		}
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		if err := validatePages(input.FirstPage, input.LastPage); err != nil {
			return nil, err
		}
		serviceType := string(input.ServiceType)
		langTag := input.LangTag
		if langTag == "" {
			langTag = "en-US"
		}
		ownerHeader := input.FileOwnerID

		key := cacheKey("doc-preview", id, input.Version, serviceType, 0, 0, "medium", "jpeg", false, "rectangular", input.FirstPage, input.LastPage, langTag, ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}

		docsTimeout := time.Duration(deps.Cfg.ServiceDocsTimeout) * time.Second
		docsURL := deps.Cfg.DocumentConversionFullConvertAddress + "/pdf"
		pdfBytes, rerr := collaboraConvertFunc(ctx, data, langTag, docsURL, docsTimeout)
		if rerr != nil {
			log.Printf("getDocumentPreview: convert: %v", rerr)
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
		}

		sliced, rerr := pdfSliceFunc(nil, pdfBytes, input.FirstPage, input.LastPage)
		if rerr != nil {
			log.Printf("getDocumentPreview: PDFSlice: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		deps.Cache.Put(key, cache.Entry{Body: sliced, ContentType: "application/pdf"})
		return &apispec.BinOut{ContentType: "application/pdf", Body: sliced}, nil
	}
}

func buildGetDocumentThumbnail(deps Deps) func(context.Context, *apispec.DocGetThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.DocGetThumbnailInput) (*apispec.BinOut, error) {
		// Document-enable gate (#17)
		if deps.Cfg != nil && !deps.Cfg.ServiceEnableDocumentThumbnail {
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.DocumentThumbnailDisabled)
		}
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		serviceType := string(input.ServiceType)
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)
		langTag := input.LangTag
		if langTag == "" {
			langTag = "en-US"
		}
		ownerHeader := input.FileOwnerID

		key := cacheKey("doc-thumb", id, input.Version, serviceType, width, height, quality, outputFormat, false, shape, 1, 0, langTag, ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}

		docsTimeout := time.Duration(deps.Cfg.ServiceDocsTimeout) * time.Second
		docsURL := deps.Cfg.DocumentConversionFullConvertAddress + "/pdf"
		pdfBytes, rerr := collaboraConvertFunc(ctx, data, langTag, docsURL, docsTimeout)
		if rerr != nil {
			log.Printf("getDocumentThumbnail: convert: %v", rerr)
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
		}

		out, rerr := pdfRasterizeFunc(nil, pdfBytes, 0, width, height, outputFormat, quality, shape)
		if rerr != nil {
			log.Printf("getDocumentThumbnail: PDFRasterize: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		actualFormat := outputFormat
		if shape == "rounded" {
			actualFormat = "png"
		}
		ct := contentTypeForFormat(actualFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &apispec.BinOut{ContentType: ct, Body: out}, nil
	}
}

func buildPostDocumentPreview(deps Deps) func(context.Context, *apispec.DocPostPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.DocPostPreviewInput) (*apispec.BinOut, error) {
		// Document-enable gate: POST preview uses ServiceEnableDocumentPreview
		if deps.Cfg != nil && !deps.Cfg.ServiceEnableDocumentPreview {
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
		}
		if err := validatePages(input.FirstPage, input.LastPage); err != nil {
			return nil, err
		}
		langTag := input.LangTag
		if langTag == "" {
			langTag = "en-US"
		}

		fileData, ferr := readHumaDocFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		docsTimeout := time.Duration(deps.Cfg.ServiceDocsTimeout) * time.Second
		docsURL := deps.Cfg.DocumentConversionFullConvertAddress + "/pdf"
		pdfBytes, rerr := collaboraConvertFunc(ctx, fileData, langTag, docsURL, docsTimeout)
		if rerr != nil {
			log.Printf("postDocumentPreview: convert: %v", rerr)
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
		}

		sliced, rerr := pdfSliceFunc(nil, pdfBytes, input.FirstPage, input.LastPage)
		if rerr != nil {
			log.Printf("postDocumentPreview: PDFSlice: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		return &apispec.BinOut{ContentType: "application/pdf", Body: sliced}, nil
	}
}

func buildPostDocumentThumbnail(deps Deps) func(context.Context, *apispec.DocPostThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.DocPostThumbnailInput) (*apispec.BinOut, error) {
		// Document-enable gate: POST thumbnail uses ServiceEnableDocumentThumbnail
		if deps.Cfg != nil && !deps.Cfg.ServiceEnableDocumentThumbnail {
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.DocumentThumbnailDisabled)
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)
		langTag := input.LangTag
		if langTag == "" {
			langTag = "en-US"
		}

		fileData, ferr := readHumaDocFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		docsTimeout := time.Duration(deps.Cfg.ServiceDocsTimeout) * time.Second
		docsURL := deps.Cfg.DocumentConversionFullConvertAddress + "/pdf"
		pdfBytes, rerr := collaboraConvertFunc(ctx, fileData, langTag, docsURL, docsTimeout)
		if rerr != nil {
			log.Printf("postDocumentThumbnail: convert: %v", rerr)
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
		}

		out, rerr := pdfRasterizeFunc(nil, pdfBytes, 0, width, height, outputFormat, quality, shape)
		if rerr != nil {
			log.Printf("postDocumentThumbnail: PDFRasterize: %v", rerr)
			if errors.Is(rerr, render.ErrRenderUnavailable) {
				return nil, huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
			}
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.InputError)
		}

		actualFormat := outputFormat
		if shape == "rounded" {
			actualFormat = "png"
		}
		return &apispec.BinOut{ContentType: contentTypeForFormat(actualFormat), Body: out}, nil
	}
}

// ---------------------------------------------------------------------------
// Video operations — generate (extract first frame → JPEG → store)
// ---------------------------------------------------------------------------

// JPEGQuality is the JPEG encoding quality used when re-encoding the extracted
// first frame. Internal constant — not a config layer, not env-overridable.
const JPEGQuality = 90

// generateFirstFrameJPEG streams the source video, extracts the first frame
// (PNG via the existing extractor), re-encodes it to JPEG q90 at full resolution
// (no resize), and stores it under the CALLER-SUPPLIED target node id (a UUID
// minted by WSC). Returns that id (echoed). Errors are returned raw; the handler
// maps them to HTTP status codes via mapStorageOrExtractError.
func generateFirstFrameJPEG(
	ctx context.Context,
	store storage.Client,
	sourceNode string,
	version int,
	serviceType string,
	ownerID string,
	targetNodeID string,
) (string, error) {
	rc, err := store.RetrieveDataStreaming(ctx, sourceNode, version, serviceType, ownerID)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	// AV1/corrupt → video.ErrExtractFailed; deadline → video.ErrExtractTimeout.
	// No internal semaphore (removed): concurrency is bounded at the handler
	// middleware (the single video semaphore).
	pngBytes, err := videoFirstFrameFunc(ctx, rc)
	if err != nil {
		return "", err
	}

	return encodePNGToJPEGAndStore(ctx, store, pngBytes, version, serviceType, ownerID, targetNodeID)
}

// encodePNGToJPEGAndStore re-encodes pngBytes to JPEG and stores them.
// Extracted so that attempt() can reuse it after a probe-first download.
func encodePNGToJPEGAndStore(
	ctx context.Context,
	store storage.Client,
	pngBytes []byte,
	version int,
	serviceType string,
	ownerID string,
	targetNodeID string,
) (string, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return "", err
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return "", err
	}

	storedID, err := store.StoreData(ctx, targetNodeID, version, serviceType, ownerID, jpg.Bytes())
	if err != nil {
		// Best-effort cleanup: fire a DELETE for the partially-written node so it
		// does not linger as an orphan in storage. Use a detached context with a
		// short timeout — the request ctx is about to be cancelled when the handler
		// returns and must NOT be used here (it would kill the delete immediately).
		// Swallow any delete error; this is defence-in-depth only.
		go func() {
			dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = store.Delete(dctx, targetNodeID, version, serviceType, ownerID)
		}()
		return "", err
	}
	return storedID, nil
}

// mapStorageOrExtractError maps a generate-pipeline error to the correct HTTP
// status:
//   - context deadline exceeded            → 504
//   - video.ErrExtractFailed (AV1/corrupt) → 422
//   - video.ErrExtractTimeout              → 504
//   - storage.ErrNotFound                  → 404
//   - everything else                      → 502
func mapStorageOrExtractError(ctx context.Context, err error) error {
	if ctx.Err() == context.DeadlineExceeded || errors.Is(err, video.ErrExtractTimeout) {
		return huma.NewError(http.StatusGatewayTimeout, "preview request timed out")
	}
	switch {
	case errors.Is(err, video.ErrExtractFailed):
		return huma.NewError(http.StatusUnprocessableEntity, config.Msg.FormatNotSupported)
	case errors.Is(err, storage.ErrNotFound):
		return huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
	default:
		return huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
	}
}

func buildGenerateVideoPreview(deps Deps) func(context.Context, *apispec.GenerateVideoInput) (*apispec.GenerateVideoOutput, error) {
	return func(ctx context.Context, input *apispec.GenerateVideoInput) (*apispec.GenerateVideoOutput, error) {
		// Single authoritative deadline: governs the FULL lifecycle of this
		// request (storage download + ffmpeg first-frame + JPEG encode + store).
		ctx, cancel := context.WithTimeout(ctx, time.Duration(deps.Cfg.ServiceTimeoutInSeconds)*time.Second)
		defer cancel()

		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		target, err := validateUUID(input.Target)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "query.target", Value: input.Target})
		}

		storedID, gerr := generateFirstFrameJPEG(
			ctx, deps.Store, id, input.Version, string(input.ServiceType), input.FileOwnerID, target,
		)
		if gerr != nil {
			if ctx.Err() == context.Canceled {
				return nil, nil // client disconnected — nothing to write
			}
			log.Printf("generateVideoPreview: %v", gerr)
			return nil, mapStorageOrExtractError(ctx, gerr)
		}

		out := &apispec.GenerateVideoOutput{}
		out.Body.PreviewID = storedID
		return out, nil
	}
}
