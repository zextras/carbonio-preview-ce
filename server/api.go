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
	"errors"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
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
	// VideoSem is the DEDICATED video semaphore (capacity =
	// video.max-concurrent-extractions, default NumCPU) bounding ffmpeg work
	// (codec probe and first-frame extraction) only. nil = unlimited (tests).
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
	apispec.RegisterImageOps(api,
		buildGetImagePreview(deps), buildGetImageThumbnail(deps),
		buildPostImagePreview(deps), buildPostImageThumbnail(deps))
	apispec.RegisterHealthOps(api,
		buildGetHealthLive(), buildGetHealthReady(deps), buildGetHealth(deps))
	apispec.RegisterPDFOps(api,
		buildGetPDFPreview(deps), buildGetPDFThumbnail(deps),
		buildPostPDFPreview(deps), buildPostPDFThumbnail(deps))
	apispec.RegisterDocumentOps(api,
		buildGetDocumentPreview(deps), buildGetDocumentThumbnail(deps),
		buildPostDocumentPreview(deps), buildPostDocumentThumbnail(deps))

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
	lim := deps.slotLimits()
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
		return served(lim.serve(ctx, deps.Cache, "getImagePreview", key, func(step runner) (cache.Entry, error) {
			data, err := step.fetch(deps.Store, id, input.Version, serviceType, ownerHeader)
			if err != nil {
				return cache.Entry{}, err
			}
			out, err := step.image(step.printfRenderErr, data, width, height, outputFormat, quality, "rectangular", cropModeFor(crop))
			if err != nil {
				return cache.Entry{}, err
			}
			return cache.Entry{Body: out, ContentType: contentTypeForFormat(outputFormat)}, nil
		}))
	}
}

func buildGetImageThumbnail(deps Deps) func(context.Context, *apispec.ImageGetThumbnailInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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
		return served(lim.serve(ctx, deps.Cache, "getImageThumbnail", key, func(step runner) (cache.Entry, error) {
			data, err := step.fetch(deps.Store, id, input.Version, serviceType, ownerHeader)
			if err != nil {
				return cache.Entry{}, err
			}
			out, err := step.image(step.printfRenderErr, data, width, height, outputFormat, quality, shape, "center")
			if err != nil {
				return cache.Entry{}, err
			}
			return cache.Entry{Body: out, ContentType: contentTypeForFormat(outputFormat)}, nil
		}))
	}
}

func buildPostImagePreview(deps Deps) func(context.Context, *apispec.ImagePostPreviewInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, input *apispec.ImagePostPreviewInput) (*apispec.BinOut, error) {
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)

		fileData, ferr := readHumaFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		step := lim.forCaller(ctx, "postImagePreview")
		out, err := step.image(step.printfRenderErr, fileData, width, height, outputFormat, quality, "rectangular", cropModeFor(input.Crop))
		if err != nil {
			return nil, err
		}
		return &apispec.BinOut{ContentType: contentTypeForFormat(outputFormat), Body: out}, nil
	}
}

func buildPostImageThumbnail(deps Deps) func(context.Context, *apispec.ImagePostThumbnailInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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

		step := lim.forCaller(ctx, "postImageThumbnail")
		out, err := step.image(step.printfRenderErr, fileData, width, height, outputFormat, quality, shape, "center")
		if err != nil {
			return nil, err
		}
		return &apispec.BinOut{ContentType: contentTypeForFormat(outputFormat), Body: out}, nil
	}
}

func cropModeFor(crop bool) string {
	if crop {
		return "center"
	}
	return "none"
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

// ---------------------------------------------------------------------------
// PDF handler builders
// ---------------------------------------------------------------------------

func buildGetPDFPreview(deps Deps) func(context.Context, *apispec.PDFGetPreviewInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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
		return served(lim.serve(ctx, deps.Cache, "getPdfPreview", key, func(step runner) (cache.Entry, error) {
			data, err := step.fetch(deps.Store, id, input.Version, serviceType, ownerHeader)
			if err != nil {
				return cache.Entry{}, err
			}
			sliced, err := step.pdfSlice(data, input.FirstPage, input.LastPage)
			if err != nil {
				return cache.Entry{}, err
			}
			return cache.Entry{Body: sliced, ContentType: "application/pdf"}, nil
		}))
	}
}

func buildGetPDFThumbnail(deps Deps) func(context.Context, *apispec.PDFGetThumbnailInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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
		return served(lim.serve(ctx, deps.Cache, "getPdfThumbnail", key, func(step runner) (cache.Entry, error) {
			data, err := step.fetch(deps.Store, id, input.Version, serviceType, ownerHeader)
			if err != nil {
				return cache.Entry{}, err
			}
			out, err := step.pdfRasterize(data, width, height, outputFormat, quality, shape)
			if err != nil {
				return cache.Entry{}, err
			}
			return cache.Entry{Body: out, ContentType: thumbnailContentType(outputFormat, shape)}, nil
		}))
	}
}

func buildPostPDFPreview(deps Deps) func(context.Context, *apispec.PDFPostPreviewInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, input *apispec.PDFPostPreviewInput) (*apispec.BinOut, error) {
		if err := validatePages(input.FirstPage, input.LastPage); err != nil {
			return nil, err
		}

		fileData, ferr := readHumaPDFFile(input.RawBody.Data())
		if ferr != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
				&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
		}

		step := lim.forCaller(ctx, "postPdfPreview")
		sliced, err := step.pdfSlice(fileData, input.FirstPage, input.LastPage)
		if err != nil {
			return nil, err
		}
		return &apispec.BinOut{ContentType: "application/pdf", Body: sliced}, nil
	}
}

func buildPostPDFThumbnail(deps Deps) func(context.Context, *apispec.PDFPostThumbnailInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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

		step := lim.forCaller(ctx, "postPdfThumbnail")
		out, err := step.pdfRasterize(fileData, width, height, outputFormat, quality, shape)
		if err != nil {
			return nil, err
		}
		return &apispec.BinOut{ContentType: thumbnailContentType(outputFormat, shape), Body: out}, nil
	}
}

func thumbnailContentType(outputFormat, shape string) string {
	if shape == "rounded" {
		return contentTypeForFormat("png")
	}
	return contentTypeForFormat(outputFormat)
}

// ---------------------------------------------------------------------------
// Document handler builders
// ---------------------------------------------------------------------------

func buildGetDocumentPreview(deps Deps) func(context.Context, *apispec.DocGetPreviewInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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
		return served(lim.serve(ctx, deps.Cache, "getDocumentPreview", key, func(step runner) (cache.Entry, error) {
			data, err := step.fetch(deps.Store, id, input.Version, serviceType, ownerHeader)
			if err != nil {
				return cache.Entry{}, err
			}
			pdfBytes, err := step.convert(deps.Cfg, data, langTag, "pdf")
			if err != nil {
				return cache.Entry{}, err
			}
			sliced, err := step.pdfSlice(pdfBytes, input.FirstPage, input.LastPage)
			if err != nil {
				return cache.Entry{}, err
			}
			return cache.Entry{Body: sliced, ContentType: "application/pdf"}, nil
		}))
	}
}

func buildGetDocumentThumbnail(deps Deps) func(context.Context, *apispec.DocGetThumbnailInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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
		return served(lim.serve(ctx, deps.Cache, "getDocumentThumbnail", key, func(step runner) (cache.Entry, error) {
			data, err := step.fetch(deps.Store, id, input.Version, serviceType, ownerHeader)
			if err != nil {
				return cache.Entry{}, err
			}
			pageImage, err := step.convert(deps.Cfg, data, langTag, render.SanitizeOutputExtension(outputFormat))
			if err != nil {
				return cache.Entry{}, err
			}
			out, err := step.image(step.printfRenderErr, pageImage, width, height, outputFormat, quality, shape, "center")
			if err != nil {
				return cache.Entry{}, err
			}
			return cache.Entry{Body: out, ContentType: thumbnailContentType(outputFormat, shape)}, nil
		}))
	}
}

func buildPostDocumentPreview(deps Deps) func(context.Context, *apispec.DocPostPreviewInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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

		step := lim.forCaller(ctx, "postDocumentPreview")
		pdfBytes, err := step.convert(deps.Cfg, fileData, langTag, "pdf")
		if err != nil {
			return nil, err
		}
		sliced, err := step.pdfSlice(pdfBytes, input.FirstPage, input.LastPage)
		if err != nil {
			return nil, err
		}
		return &apispec.BinOut{ContentType: "application/pdf", Body: sliced}, nil
	}
}

func buildPostDocumentThumbnail(deps Deps) func(context.Context, *apispec.DocPostThumbnailInput) (*apispec.BinOut, error) {
	lim := deps.slotLimits()
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

		step := lim.forCaller(ctx, "postDocumentThumbnail")
		pageImage, err := step.convert(deps.Cfg, fileData, langTag, render.SanitizeOutputExtension(outputFormat))
		if err != nil {
			return nil, err
		}
		out, err := step.image(step.printfRenderErr, pageImage, width, height, outputFormat, quality, shape, "center")
		if err != nil {
			return nil, err
		}
		return &apispec.BinOut{ContentType: thumbnailContentType(outputFormat, shape), Body: out}, nil
	}
}

// ---------------------------------------------------------------------------
// Video operations — generate (extract first frame → JPEG → store)
// ---------------------------------------------------------------------------

// storeFrame stores the JPEG frame produced by ffmpeg. On failure it fires a
// best-effort cleanup Delete of the partially-written node; the storage client's
// inactivity bound is the only time limit. When ctx ended (shutdown) the Delete
// runs before returning, bounded by releaseTimeout, so it is not lost.
func storeFrame(
	ctx context.Context,
	store storage.Client,
	jpg []byte,
	version int,
	serviceType string,
	ownerID string,
	targetNodeID string,
) (string, error) {
	storedID, err := store.StoreData(ctx, targetNodeID, version, serviceType, ownerID, jpg)
	if err != nil {
		if ctx.Err() != nil {
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
			defer cancel()
			_ = store.Delete(dctx, targetNodeID, version, serviceType, ownerID)
			return "", err
		}
		go func() {
			_ = store.Delete(context.Background(), targetNodeID, version, serviceType, ownerID)
		}()
		return "", err
	}
	return storedID, nil
}
