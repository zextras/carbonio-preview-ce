// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
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
	Sem   chan struct{}
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
	registerVideoOps(api, deps)
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

// semaphoreMiddleware returns a huma middleware that acquires the render
// semaphore before the handler and releases it after. Pass sem=nil for
// unlimited concurrency (tests / gendocs).
func semaphoreMiddleware(sem chan struct{}) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if sem != nil {
			sem <- struct{}{}
			defer func() { <-sem }()
		}
		next(ctx)
	}
}

// ---------------------------------------------------------------------------
// Image operations
// ---------------------------------------------------------------------------

func registerImageOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(deps.Sem)

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
		out, rerr := imageThumbnailFunc(deps.Sem, data, width, height, outputFormat, quality, "rectangular", cropMode)
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

		out, rerr := imageThumbnailFunc(deps.Sem, data, width, height, outputFormat, quality, shape, "center")
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
		out, rerr := imageThumbnailFunc(deps.Sem, fileData, width, height, outputFormat, quality, "rectangular", cropMode)
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

		out, rerr := imageThumbnailFunc(deps.Sem, fileData, width, height, outputFormat, quality, shape, "center")
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
// Input structs — Video GET
// ---------------------------------------------------------------------------

// videoGetPreviewInput holds all path + query params for GET video preview.
type videoGetPreviewInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the video"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version (non-negative)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Crop         bool        `query:"crop"          default:"false"             doc:"Crop to fill (true) or scale to fit (false)"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID" required:"false"`
}

// videoGetThumbnailInput holds all path + query params for GET video thumbnail.
type videoGetThumbnailInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the video"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version (non-negative)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape       `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID" required:"false"`
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
	semMW := semaphoreMiddleware(deps.Sem)

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

		sliced, rerr := pdfSliceFunc(deps.Sem, data, input.FirstPage, input.LastPage)
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

		out, rerr := pdfRasterizeFunc(deps.Sem, data, 0, width, height, outputFormat, quality, shape)
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

		sliced, rerr := pdfSliceFunc(deps.Sem, fileData, input.FirstPage, input.LastPage)
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

		out, rerr := pdfRasterizeFunc(deps.Sem, fileData, 0, width, height, outputFormat, quality, shape)
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
	semMW := semaphoreMiddleware(deps.Sem)

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

		sliced, rerr := pdfSliceFunc(deps.Sem, pdfBytes, input.FirstPage, input.LastPage)
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

		out, rerr := pdfRasterizeFunc(deps.Sem, pdfBytes, 0, width, height, outputFormat, quality, shape)
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

		sliced, rerr := pdfSliceFunc(deps.Sem, pdfBytes, input.FirstPage, input.LastPage)
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

		out, rerr := pdfRasterizeFunc(deps.Sem, pdfBytes, 0, width, height, outputFormat, quality, shape)
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
// Video operations
// ---------------------------------------------------------------------------

func registerVideoOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(deps.Sem)

	// GET /preview/video/{id}/{version}/{area}/
	huma.Register(api, huma.Operation{
		OperationID: "getVideoPreview",
		Method:      http.MethodGet,
		Path:        "/preview/video/{id}/{version}/{area}/",
		Summary:     "Get Video Preview",
		Tags:        []string{"video"},
		Errors:      []int{400, 404, 422, 502, 503, 504},
		Responses:   map[string]*huma.Response{"200": imageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *videoGetPreviewInput) (*BinOut, error) {
		// Single authoritative deadline: governs the FULL lifecycle of this
		// request (storage download + ffmpeg first-frame + libvips render).
		// No other time-based cap competes with this one.
		ctx, cancel := context.WithTimeout(ctx, time.Duration(deps.Cfg.ServiceTimeoutInSeconds)*time.Second)
		defer cancel()

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

		key := cacheKey("video-preview", id, input.Version, serviceType, width, height, quality, outputFormat, crop, "rectangular", 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		rc, rerr := deps.Store.RetrieveDataStreaming(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, huma.NewError(http.StatusGatewayTimeout, "preview request timed out")
			}
			if ctx.Err() == context.Canceled {
				return nil, nil // client disconnected — nothing to write
			}
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}
		defer rc.Close()

		frame, rerr := videoFirstFrameFunc(ctx, rc)
		if rerr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, huma.NewError(http.StatusGatewayTimeout, "preview request timed out")
			}
			if ctx.Err() == context.Canceled {
				return nil, nil // client disconnected — nothing to write
			}
			log.Printf("getVideoPreview: first-frame: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		cropMode := "none"
		if crop {
			cropMode = "center"
		}
		out, rerr := imageThumbnailFunc(deps.Sem, frame, width, height, outputFormat, quality, "rectangular", cropMode)
		if rerr != nil {
			log.Printf("getVideoPreview: render: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		ct := contentTypeForFormat(outputFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &BinOut{ContentType: ct, Body: out}, nil
	})

	// GET /preview/video/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getVideoThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/video/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get Video Thumbnail",
		Tags:        []string{"video"},
		Errors:      []int{400, 404, 422, 502, 503, 504},
		Responses:   map[string]*huma.Response{"200": imageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, func(ctx context.Context, input *videoGetThumbnailInput) (*BinOut, error) {
		// Single authoritative deadline: governs the FULL lifecycle of this
		// request (storage download + ffmpeg first-frame + libvips render).
		// No other time-based cap competes with this one.
		ctx, cancel := context.WithTimeout(ctx, time.Duration(deps.Cfg.ServiceTimeoutInSeconds)*time.Second)
		defer cancel()

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

		key := cacheKey("video-thumb", id, input.Version, serviceType, width, height, quality, outputFormat, true, shape, 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		rc, rerr := deps.Store.RetrieveDataStreaming(ctx, id, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, huma.NewError(http.StatusGatewayTimeout, "preview request timed out")
			}
			if ctx.Err() == context.Canceled {
				return nil, nil // client disconnected — nothing to write
			}
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
			}
			return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}
		defer rc.Close()

		frame, rerr := videoFirstFrameFunc(ctx, rc)
		if rerr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, huma.NewError(http.StatusGatewayTimeout, "preview request timed out")
			}
			if ctx.Err() == context.Canceled {
				return nil, nil // client disconnected — nothing to write
			}
			log.Printf("getVideoThumbnail: first-frame: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		out, rerr := imageThumbnailFunc(deps.Sem, frame, width, height, outputFormat, quality, shape, "center")
		if rerr != nil {
			log.Printf("getVideoThumbnail: render: %v", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		// rounded shape: use png content type (same as PDF thumbnail)
		actualFormat := outputFormat
		if shape == "rounded" {
			actualFormat = "png"
		}
		ct := contentTypeForFormat(actualFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &BinOut{ContentType: ct, Body: out}, nil
	})
}
