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

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/storage"
	"github.com/zextras/carbonio-preview-ce/video"
)

// ---------------------------------------------------------------------------
// Enum types (huma generates component schemas from these)
// ---------------------------------------------------------------------------

// enumStringSchema builds the inline JSON-Schema for a string enum type.
// Returning a SchemaProvider schema makes huma both (a) validate that the
// incoming value is one of the listed members — returning a 422 otherwise —
// and (b) emit the enum constraint into the generated OpenAPI. The schema is
// inlined (not a named $ref) because huma's mapRegistry only refs struct
// kinds; per the plan this is acceptable (the SDK is decoupled from component
// names).
func enumStringSchema(description string, values ...string) *huma.Schema {
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	s := &huma.Schema{
		Type:        huma.TypeString,
		Enum:        enum,
		Description: description,
	}
	s.PrecomputeMessages()
	return s
}

// ServiceType represents the service that owns the stored resource.
type ServiceType string

const (
	ServiceTypeFiles ServiceType = "files"
	ServiceTypeChats ServiceType = "chats"
)

// Schema implements huma.SchemaProvider so the value is validated against the
// allowed members and the enum is documented.
func (ServiceType) Schema(huma.Registry) *huma.Schema {
	return enumStringSchema("Service that owns the resource",
		string(ServiceTypeFiles), string(ServiceTypeChats))
}

// Quality represents the image rendering quality.
type Quality string

const (
	QualityLowest  Quality = "lowest"
	QualityLow     Quality = "low"
	QualityMedium  Quality = "medium"
	QualityHigh    Quality = "high"
	QualityHighest Quality = "highest"
)

// Schema implements huma.SchemaProvider.
func (Quality) Schema(huma.Registry) *huma.Schema {
	return enumStringSchema("Output image quality",
		string(QualityLowest), string(QualityLow), string(QualityMedium),
		string(QualityHigh), string(QualityHighest))
}

// Shape represents the image thumbnail border shape.
type Shape string

const (
	ShapeRounded     Shape = "rounded"
	ShapeRectangular Shape = "rectangular"
)

// Schema implements huma.SchemaProvider.
func (Shape) Schema(huma.Registry) *huma.Schema {
	return enumStringSchema("Thumbnail border shape",
		string(ShapeRounded), string(ShapeRectangular))
}

// ImageType represents the output image format.
type ImageType string

const (
	ImageTypeJPEG ImageType = "jpeg"
	ImageTypePNG  ImageType = "png"
	ImageTypeGIF  ImageType = "gif"
)

// Schema implements huma.SchemaProvider.
func (ImageType) Schema(huma.Registry) *huma.Schema {
	return enumStringSchema("Output image format",
		string(ImageTypeJPEG), string(ImageTypePNG), string(ImageTypeGIF))
}

// ---------------------------------------------------------------------------
// Binary output type shared by all image operations
// ---------------------------------------------------------------------------

// BinOut is the output struct for operations that return binary image data.
// The ContentType header is set per-request by the handler.
type BinOut struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// ---------------------------------------------------------------------------
// Input structs — image GET
// ---------------------------------------------------------------------------

// imageGetPreviewInput holds all path + query params for GET image preview.
// Fields are listed inline (not embedded) to avoid huma's embedded struct skipping.
type imageGetPreviewInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the image (UUID1–UUID4)"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version of the image (non-negative integer)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Crop         bool        `query:"crop"          default:"false"             doc:"Crop to fill (true) or scale to fit (false)"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID for Advanced storage routing" required:"false"`
}

// imageGetThumbnailInput holds all path + query params for GET image thumbnail.
type imageGetThumbnailInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the image (UUID1–UUID4)"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version of the image (non-negative integer)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape       `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID for Advanced storage routing" required:"false"`
}

// ---------------------------------------------------------------------------
// Input structs — image POST (multipart)
// ---------------------------------------------------------------------------

// imagePostFiles is the multipart body schema for POST image operations.
type imagePostFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"Image file to process"`
}

// imagePostPreviewInput holds path + query params + multipart body for POST preview.
type imagePostPreviewInput struct {
	Area         string    `path:"area" pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	Quality      Quality   `query:"quality"       default:"medium" doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"   doc:"Output image format"`
	Crop         bool      `query:"crop"          default:"false"  doc:"Crop to fill (true) or scale to fit (false)"`
	RawBody      huma.MultipartFormFiles[imagePostFiles]
}

// imagePostThumbnailInput holds path + query params + multipart body for POST thumbnail.
type imagePostThumbnailInput struct {
	Area         string    `path:"area" pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	Quality      Quality   `query:"quality"       default:"medium" doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"   doc:"Output image format"`
	Shape        Shape     `query:"shape"         default:"rectangular" doc:"Thumbnail border shape"`
	RawBody      huma.MultipartFormFiles[imagePostFiles]
}

// ---------------------------------------------------------------------------
// Health output structs
// ---------------------------------------------------------------------------

// healthLiveOutput is returned by GET /health/live/.
type healthLiveOutput struct{}

// healthReadyOutput is returned by GET /health/ready/ (empty body on success).
type healthReadyOutput struct{}

// healthFullOutput wraps the JSON health response.
type healthFullOutput struct {
	Body *healthResponse
}

// ---------------------------------------------------------------------------
// Binary response schema helper
// ---------------------------------------------------------------------------

var imageBinaryResponseSchema = &huma.Schema{Type: "string", Format: "binary"}

// imageBinaryResponse defines the 200 response for image operations with
// multiple possible content types.
var imageBinaryResponse = &huma.Response{
	Description: "Successful Response — binary image data",
	Content: map[string]*huma.MediaType{
		"image/jpeg": {Schema: imageBinaryResponseSchema},
		"image/png":  {Schema: imageBinaryResponseSchema},
		"image/gif":  {Schema: imageBinaryResponseSchema},
	},
}

// ---------------------------------------------------------------------------
// Deps bundles the server dependencies needed by huma handlers.
// ---------------------------------------------------------------------------

// Deps bundles the runtime dependencies injected into huma handlers.
// For gendocs (spec-only mode) all fields may be nil; handlers are never invoked.
type Deps struct {
	Cfg   *config.Config
	Store storage.Client
	Cache *cache.Cache
	// Sem is the shared render-concurrency semaphore (image/PDF/document ops).
	Sem chan struct{}
	// VideoSem is the DEDICATED video-generate semaphore (capacity =
	// video-concurrency, default NumCPU). Separate from Sem so a flood of
	// generate calls cannot starve image previews. nil = unlimited (tests).
	VideoSem chan struct{}
}

// ---------------------------------------------------------------------------
// RegisterOperations registers all huma-managed operations onto api.
// It is called by both the live server and cmd/gendocs.
// ---------------------------------------------------------------------------

func RegisterOperations(api huma.API, deps Deps) {
	registerImageOps(api, deps)
	registerHealthOps(api, deps)
	registerPDFOps(api, deps)
	registerDocumentOps(api, deps)
	registerGenerateOps(api, deps)
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
// Image operations
// ---------------------------------------------------------------------------

func registerImageOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(api, deps.Sem)

	// GET /preview/image/{id}/{version}/{area}/
	huma.Register(api, huma.Operation{
		OperationID: "getImagePreview",
		Method:      http.MethodGet,
		Path:        "/preview/image/{id}/{version}/{area}/",
		Summary:     "Get Image Preview",
		Tags:        []string{"image"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": imageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *imageGetPreviewInput) (*BinOut, error) {
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
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
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
		return &BinOut{ContentType: ct, Body: out}, nil
	})

	// GET /preview/image/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getImageThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/image/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get Image Thumbnail",
		Tags:        []string{"image"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": imageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *imageGetThumbnailInput) (*BinOut, error) {
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
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
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
		return &BinOut{ContentType: ct, Body: out}, nil
	})

	// POST /preview/image/{area}/
	huma.Register(api, huma.Operation{
		OperationID: "postImagePreview",
		Method:      http.MethodPost,
		Path:        "/preview/image/{area}/",
		Summary:     "Post Image Preview",
		Tags:        []string{"image"},
		Errors:      []int{400, 422},
		Responses: map[string]*huma.Response{
			"200": imageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *imagePostPreviewInput) (*BinOut, error) {
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

		return &BinOut{ContentType: contentTypeForFormat(outputFormat), Body: out}, nil
	})

	// POST /preview/image/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "postImageThumbnail",
		Method:      http.MethodPost,
		Path:        "/preview/image/{area}/thumbnail/",
		Summary:     "Post Image Thumbnail",
		Tags:        []string{"image"},
		Errors:      []int{400, 422},
		Responses: map[string]*huma.Response{
			"200": imageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *imagePostThumbnailInput) (*BinOut, error) {
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

		return &BinOut{ContentType: contentTypeForFormat(outputFormat), Body: out}, nil
	})
}

// ---------------------------------------------------------------------------
// Health operations
// ---------------------------------------------------------------------------

func registerHealthOps(api huma.API, deps Deps) {
	// GET /health/live/
	huma.Register(api, huma.Operation{
		OperationID:   "getHealthLive",
		Method:        http.MethodGet,
		Path:          "/health/live/",
		Summary:       "Liveness Probe",
		Tags:          []string{"health"},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, _ *struct{}) (*healthLiveOutput, error) {
		return &healthLiveOutput{}, nil
	})

	// GET /health/ready/
	huma.Register(api, huma.Operation{
		OperationID:   "getHealthReady",
		Method:        http.MethodGet,
		Path:          "/health/ready/",
		Summary:       "Readiness Probe",
		Tags:          []string{"health"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{429},
	}, func(ctx context.Context, _ *struct{}) (*healthReadyOutput, error) {
		cfg := deps.Cfg
		if cfg == nil || !cfg.AreDocsEnabled {
			return &healthReadyOutput{}, nil
		}
		if isDependencyUp(cfg.DocumentConversionFullServiceAddress) {
			return &healthReadyOutput{}, nil
		}
		// Return 429 via huma error — huma will write the status; the body
		// will be plain text matching the legacy handler.
		return nil, huma.NewError(http.StatusTooManyRequests, config.Msg.DocsEditorUnavailable)
	})

	// GET /health/
	huma.Register(api, huma.Operation{
		OperationID:   "getHealth",
		Method:        http.MethodGet,
		Path:          "/health/",
		Summary:       "Health Summary",
		Tags:          []string{"health"},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, _ *struct{}) (*healthFullOutput, error) {
		cfg := deps.Cfg
		if cfg == nil {
			// gendocs mode — return empty response
			return &healthFullOutput{Body: &healthResponse{Ready: true}}, nil
		}
		storageHealthURL := cfg.StorageFullAddress + "/" + cfg.StorageHealthCheck
		docsHealthURL := cfg.DocumentConversionFullServiceAddress
		storageUp := isDependencyUp(storageHealthURL)
		docsUp := isDependencyUp(docsHealthURL)
		resp := &healthResponse{
			Ready: true,
			Dependencies: []healthDependency{
				{Name: "carbonio-storages", Ready: storageUp, Live: storageUp, Type: "OPTIONAL"},
				{Name: "carbonio-docs-editor", Ready: docsUp, Live: docsUp, Type: "OPTIONAL"},
			},
		}
		return &healthFullOutput{Body: resp}, nil
	})
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
func readHumaFile(data *imagePostFiles) ([]byte, error) {
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
func readHumaPDFFile(data *pdfPostFiles) ([]byte, error) {
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
func readHumaDocFile(data *docPostFiles) ([]byte, error) {
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
// Input structs — PDF GET
// ---------------------------------------------------------------------------

// pdfGetPreviewInput holds all path + query params for GET PDF preview.
type pdfGetPreviewInput struct {
	ID          string      `path:"id"            format:"uuid"              doc:"UUID of the PDF"`
	Version     int         `path:"version"       minimum:"0"                doc:"Version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true"            doc:"Service that owns the resource"`
	FirstPage   int         `query:"first_page"   default:"1" doc:"First page (1-based, default 1)"`
	LastPage    int         `query:"last_page"    default:"0" doc:"Last page (0 = all, default 0)"`
	FileOwnerID string      `header:"fileownerid"             doc:"File owner ID" required:"false"`
}

// pdfGetThumbnailInput holds all path + query params for GET PDF thumbnail.
type pdfGetThumbnailInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the PDF"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version (non-negative)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape       `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID" required:"false"`
}

// ---------------------------------------------------------------------------
// Input structs — PDF POST
// ---------------------------------------------------------------------------

// pdfPostFiles is the multipart body schema for POST PDF operations.
type pdfPostFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"PDF file to process"`
}

// pdfPostPreviewInput holds query params + multipart body for POST PDF preview.
type pdfPostPreviewInput struct {
	FirstPage int `query:"first_page" default:"1" doc:"First page (1-based, default 1)"`
	LastPage  int `query:"last_page"  default:"0" doc:"Last page (0 = all, default 0)"`
	RawBody   huma.MultipartFormFiles[pdfPostFiles]
}

// pdfPostThumbnailInput holds path + query params + multipart body for POST PDF thumbnail.
type pdfPostThumbnailInput struct {
	Area         string    `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	Quality      Quality   `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape     `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	RawBody      huma.MultipartFormFiles[pdfPostFiles]
}

// ---------------------------------------------------------------------------
// Input structs — Document GET
// ---------------------------------------------------------------------------

// docGetPreviewInput holds all path + query params for GET document preview.
type docGetPreviewInput struct {
	ID          string      `path:"id"            format:"uuid"              doc:"UUID of the document"`
	Version     int         `path:"version"       minimum:"0"                doc:"Version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true"            doc:"Service that owns the resource"`
	FirstPage   int         `query:"first_page"   default:"1" doc:"First page (1-based, default 1)"`
	LastPage    int         `query:"last_page"    default:"0" doc:"Last page (0 = all, default 0)"`
	LangTag     string      `query:"lang_tag"     default:"en-US" doc:"Language tag for conversion (default en-US)"`
	FileOwnerID string      `header:"fileownerid"               doc:"File owner ID" required:"false"`
}

// docGetThumbnailInput holds all path + query params for GET document thumbnail.
type docGetThumbnailInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the document"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version (non-negative)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape       `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	LangTag      string      `query:"lang_tag"                      default:"en-US" doc:"Language tag for conversion"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID" required:"false"`
}

// ---------------------------------------------------------------------------
// Input structs — Document POST
// ---------------------------------------------------------------------------

// docPostFiles is the multipart body schema for POST document operations.
type docPostFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"Document file to process"`
}

// docPostPreviewInput holds query params + multipart body for POST document preview.
type docPostPreviewInput struct {
	FirstPage int    `query:"first_page" default:"1"     doc:"First page (1-based, default 1)"`
	LastPage  int    `query:"last_page"  default:"0"     doc:"Last page (0 = all, default 0)"`
	LangTag   string `query:"lang_tag"   default:"en-US" doc:"Language tag for conversion"`
	RawBody   huma.MultipartFormFiles[docPostFiles]
}

// docPostThumbnailInput holds path + query params + multipart body for POST document thumbnail.
type docPostThumbnailInput struct {
	Area         string    `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	Quality      Quality   `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape     `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	LangTag      string    `query:"lang_tag"                      default:"en-US" doc:"Language tag for conversion"`
	RawBody      huma.MultipartFormFiles[docPostFiles]
}

// ---------------------------------------------------------------------------
// Input/output structs — Video generate (POST)
// ---------------------------------------------------------------------------

// generateVideoInput holds the path + query params + owner header for the
// POST /preview/video/generate endpoint. The caller (WSC) supplies BOTH the
// source coordinates (id/version/service_type/owner) AND the target node id
// (a UUID it minted) under which the extracted first frame is stored.
type generateVideoInput struct {
	ID          string      `path:"id"            format:"uuid"   doc:"UUID of the SOURCE video node"`
	Version     int         `path:"version"       minimum:"0"     doc:"Source version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true" doc:"Service that owns the resource"`
	Target      string      `query:"target"       format:"uuid" required:"true" doc:"Caller-minted UUID for the stored frame"`
	FileOwnerID string      `header:"fileownerid"                doc:"File owner ID (PowerStore routing)" required:"false"`
}

// generateVideoOutput echoes the stored node id of the generated frame.
type generateVideoOutput struct {
	Body struct {
		PreviewID string `json:"preview_id" doc:"Storage node id of the stored first-frame image (echoes target)"`
	}
}

// ---------------------------------------------------------------------------
// Binary response vars — PDF
// ---------------------------------------------------------------------------

// pdfBinaryResponse defines the 200 response for PDF operations.
var pdfBinaryResponse = &huma.Response{
	Description: "Successful Response — PDF bytes",
	Content: map[string]*huma.MediaType{
		"application/pdf": {Schema: imageBinaryResponseSchema},
	},
}

// ---------------------------------------------------------------------------
// PDF operations
// ---------------------------------------------------------------------------

func registerPDFOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(api, deps.Sem)

	// GET /preview/pdf/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID: "getPdfPreview",
		Method:      http.MethodGet,
		Path:        "/preview/pdf/{id}/{version}/",
		Summary:     "Get PDF Preview",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": pdfBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *pdfGetPreviewInput) (*BinOut, error) {
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
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
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
		return &BinOut{ContentType: "application/pdf", Body: sliced}, nil
	})

	// GET /preview/pdf/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getPdfThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/pdf/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get PDF Thumbnail",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": imageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *pdfGetThumbnailInput) (*BinOut, error) {
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
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
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
		return &BinOut{ContentType: ct, Body: out}, nil
	})

	// POST /preview/pdf/
	huma.Register(api, huma.Operation{
		OperationID: "postPdfPreview",
		Method:      http.MethodPost,
		Path:        "/preview/pdf/",
		Summary:     "Post PDF Preview",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 422, 503},
		Responses:   map[string]*huma.Response{"200": pdfBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *pdfPostPreviewInput) (*BinOut, error) {
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

		return &BinOut{ContentType: "application/pdf", Body: sliced}, nil
	})

	// POST /preview/pdf/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "postPdfThumbnail",
		Method:      http.MethodPost,
		Path:        "/preview/pdf/{area}/thumbnail/",
		Summary:     "Post PDF Thumbnail",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 422, 503},
		Responses:   map[string]*huma.Response{"200": imageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *pdfPostThumbnailInput) (*BinOut, error) {
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
		return &BinOut{ContentType: contentTypeForFormat(actualFormat), Body: out}, nil
	})
}

// ---------------------------------------------------------------------------
// Document operations
// ---------------------------------------------------------------------------

func registerDocumentOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(api, deps.Sem)

	// GET /preview/document/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID: "getDocumentPreview",
		Method:      http.MethodGet,
		Path:        "/preview/document/{id}/{version}/",
		Summary:     "Get Document Preview",
		Tags:        []string{"document"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": pdfBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *docGetPreviewInput) (*BinOut, error) {
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
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
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
		return &BinOut{ContentType: "application/pdf", Body: sliced}, nil
	})

	// GET /preview/document/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getDocumentThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/document/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get Document Thumbnail",
		Tags:        []string{"document"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": imageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *docGetThumbnailInput) (*BinOut, error) {
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
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
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
		return &BinOut{ContentType: ct, Body: out}, nil
	})

	// POST /preview/document/
	huma.Register(api, huma.Operation{
		OperationID: "postDocumentPreview",
		Method:      http.MethodPost,
		Path:        "/preview/document/",
		Summary:     "Post Document Preview",
		Tags:        []string{"document"},
		Errors:      []int{400, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": pdfBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *docPostPreviewInput) (*BinOut, error) {
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

		return &BinOut{ContentType: "application/pdf", Body: sliced}, nil
	})

	// POST /preview/document/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "postDocumentThumbnail",
		Method:      http.MethodPost,
		Path:        "/preview/document/{area}/thumbnail/",
		Summary:     "Post Document Thumbnail",
		Tags:        []string{"document"},
		Errors:      []int{400, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": imageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *docPostThumbnailInput) (*BinOut, error) {
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
		return &BinOut{ContentType: contentTypeForFormat(actualFormat), Body: out}, nil
	})
}

// ---------------------------------------------------------------------------
// Video operations — generate (extract first frame → JPEG → store)
// ---------------------------------------------------------------------------

// JPEGQuality is the JPEG encoding quality used when re-encoding the extracted
// first frame. Internal constant — not a config layer, not env-overridable.
const JPEGQuality = 90

// videoRetryAfterSeconds is the Retry-After header value (seconds) returned with
// a 429 when the dedicated video semaphore is full.
const videoRetryAfterSeconds = "1"

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

	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return "", err
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return "", err
	}

	return store.StoreData(ctx, targetNodeID, version, serviceType, ownerID, jpg.Bytes())
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

// videoSemaphoreMiddleware bounds generate to cfg.VideoConcurrency (APPLICATION
// key video-concurrency, default NumCPU) using a DEDICATED semaphore — NOT the
// shared render semMW — so a flood of generate calls can never starve image
// previews. Try-acquire immediately; on a full semaphore return HTTP 429 with a
// Retry-After header (no waiting).
//
// 429 (not 503): 4xx = expected backpressure, so it does not inflate preview's
// 5xx error metrics or trip outage alerts; it clearly signals "retryable, back
// off". 503 is reserved for "preview genuinely down" and is retained only on the
// existing semMW (image/render). Pass vsem=nil for unlimited concurrency
// (tests / gendocs).
func videoSemaphoreMiddleware(api huma.API, vsem chan struct{}) func(huma.Context, func(huma.Context)) {
	return func(hctx huma.Context, next func(huma.Context)) {
		if vsem == nil {
			next(hctx)
			return
		}
		select {
		case vsem <- struct{}{}:
			defer func() { <-vsem }()
			next(hctx)
		default:
			hctx.SetHeader("Retry-After", videoRetryAfterSeconds)
			_ = huma.WriteErr(api, hctx, http.StatusTooManyRequests, "server busy, retry")
		}
	}
}

func registerGenerateOps(api huma.API, deps Deps) {
	vsemMW := videoSemaphoreMiddleware(api, deps.VideoSem) // dedicated video semaphore, NOT semMW

	// POST /preview/video/generate/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID: "generateVideoPreview",
		Method:      http.MethodPost,
		Path:        "/preview/video/generate/{id}/{version}/",
		Summary:     "Generate (extract + JPEG-encode + store) a video first-frame preview",
		Tags:        []string{"video"},
		Errors:      []int{400, 404, 422, 429, 502, 504},
		Middlewares: huma.Middlewares{vsemMW},
	}, func(ctx context.Context, input *generateVideoInput) (*generateVideoOutput, error) {
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

		out := &generateVideoOutput{}
		out.Body.PreviewID = storedID
		return out, nil
	})
}
