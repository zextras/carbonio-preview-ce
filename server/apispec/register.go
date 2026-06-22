// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ---------------------------------------------------------------------------
// Handlers — injected handler functions, one per huma operation
// ---------------------------------------------------------------------------

// Handlers holds one typed function per API operation. The live server fills
// each field with a closure that calls the real render/video/storage code.
// RegisterStubs uses nil-returning no-ops so that huma can register and
// introspect all operations without executing any rendering.
type Handlers struct {
	GetImagePreview    func(context.Context, *ImageGetPreviewInput) (*BinOut, error)
	GetImageThumbnail  func(context.Context, *ImageGetThumbnailInput) (*BinOut, error)
	PostImagePreview   func(context.Context, *ImagePostPreviewInput) (*BinOut, error)
	PostImageThumbnail func(context.Context, *ImagePostThumbnailInput) (*BinOut, error)

	GetHealthLive  func(context.Context, *struct{}) (*HealthLiveOutput, error)
	GetHealthReady func(context.Context, *struct{}) (*HealthReadyOutput, error)
	GetHealth      func(context.Context, *struct{}) (*HealthFullOutput, error)

	GetPDFPreview    func(context.Context, *PDFGetPreviewInput) (*BinOut, error)
	GetPDFThumbnail  func(context.Context, *PDFGetThumbnailInput) (*BinOut, error)
	PostPDFPreview   func(context.Context, *PDFPostPreviewInput) (*BinOut, error)
	PostPDFThumbnail func(context.Context, *PDFPostThumbnailInput) (*BinOut, error)

	GetDocumentPreview    func(context.Context, *DocGetPreviewInput) (*BinOut, error)
	GetDocumentThumbnail  func(context.Context, *DocGetThumbnailInput) (*BinOut, error)
	PostDocumentPreview   func(context.Context, *DocPostPreviewInput) (*BinOut, error)
	PostDocumentThumbnail func(context.Context, *DocPostThumbnailInput) (*BinOut, error)

	GenerateVideoPreview func(context.Context, *GenerateVideoInput) (*GenerateVideoOutput, error)
}

// Register registers all huma-managed operations onto api using the provided
// Handlers for the actual request logic. Middleware functions (semaphores) are
// passed explicitly so this package stays free of runtime concerns.
func Register(api huma.API, h Handlers, semMW, videoSemMW func(huma.Context, func(huma.Context))) {
	RegisterImageOps(api, h.GetImagePreview, h.GetImageThumbnail, h.PostImagePreview, h.PostImageThumbnail, semMW)
	RegisterHealthOps(api, h.GetHealthLive, h.GetHealthReady, h.GetHealth)
	RegisterPDFOps(api, h.GetPDFPreview, h.GetPDFThumbnail, h.PostPDFPreview, h.PostPDFThumbnail, semMW)
	RegisterDocumentOps(api, h.GetDocumentPreview, h.GetDocumentThumbnail, h.PostDocumentPreview, h.PostDocumentThumbnail, semMW)
	RegisterGenerateOps(api, h.GenerateVideoPreview, videoSemMW)
}

// RegisterStubs registers all operations with no-op stub handlers.
// The stubs return zero values and no error; handler bodies are never executed.
// Use this in cmd/gendocs or any context that only needs the OpenAPI spec.
func RegisterStubs(api huma.API) {
	h := Handlers{
		GetImagePreview:    func(_ context.Context, _ *ImageGetPreviewInput) (*BinOut, error) { return nil, nil },
		GetImageThumbnail:  func(_ context.Context, _ *ImageGetThumbnailInput) (*BinOut, error) { return nil, nil },
		PostImagePreview:   func(_ context.Context, _ *ImagePostPreviewInput) (*BinOut, error) { return nil, nil },
		PostImageThumbnail: func(_ context.Context, _ *ImagePostThumbnailInput) (*BinOut, error) { return nil, nil },

		GetHealthLive:  func(_ context.Context, _ *struct{}) (*HealthLiveOutput, error) { return &HealthLiveOutput{}, nil },
		GetHealthReady: func(_ context.Context, _ *struct{}) (*HealthReadyOutput, error) { return &HealthReadyOutput{}, nil },
		GetHealth: func(_ context.Context, _ *struct{}) (*HealthFullOutput, error) {
			return &HealthFullOutput{Body: &HealthResponse{Ready: true}}, nil
		},

		GetPDFPreview:    func(_ context.Context, _ *PDFGetPreviewInput) (*BinOut, error) { return nil, nil },
		GetPDFThumbnail:  func(_ context.Context, _ *PDFGetThumbnailInput) (*BinOut, error) { return nil, nil },
		PostPDFPreview:   func(_ context.Context, _ *PDFPostPreviewInput) (*BinOut, error) { return nil, nil },
		PostPDFThumbnail: func(_ context.Context, _ *PDFPostThumbnailInput) (*BinOut, error) { return nil, nil },

		GetDocumentPreview:    func(_ context.Context, _ *DocGetPreviewInput) (*BinOut, error) { return nil, nil },
		GetDocumentThumbnail:  func(_ context.Context, _ *DocGetThumbnailInput) (*BinOut, error) { return nil, nil },
		PostDocumentPreview:   func(_ context.Context, _ *DocPostPreviewInput) (*BinOut, error) { return nil, nil },
		PostDocumentThumbnail: func(_ context.Context, _ *DocPostThumbnailInput) (*BinOut, error) { return nil, nil },

		GenerateVideoPreview: func(_ context.Context, _ *GenerateVideoInput) (*GenerateVideoOutput, error) { return nil, nil },
	}
	// RegisterStubs does not need semaphore middleware — stubs never block.
	noopMW := func(hctx huma.Context, next func(huma.Context)) { next(hctx) }
	Register(api, h, noopMW, noopMW)
}

// ---------------------------------------------------------------------------
// Image operations
// ---------------------------------------------------------------------------

// RegisterImageOps registers all image-related huma operations.
func RegisterImageOps(
	api huma.API,
	getPreview func(context.Context, *ImageGetPreviewInput) (*BinOut, error),
	getThumbnail func(context.Context, *ImageGetThumbnailInput) (*BinOut, error),
	postPreview func(context.Context, *ImagePostPreviewInput) (*BinOut, error),
	postThumbnail func(context.Context, *ImagePostThumbnailInput) (*BinOut, error),
	semMW func(huma.Context, func(huma.Context)),
) {
	// GET /preview/image/{id}/{version}/{area}/
	huma.Register(api, huma.Operation{
		OperationID: "getImagePreview",
		Method:      http.MethodGet,
		Path:        "/preview/image/{id}/{version}/{area}/",
		Summary:     "Get Image Preview",
		Tags:        []string{"image"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": ImageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, getPreview)

	// GET /preview/image/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getImageThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/image/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get Image Thumbnail",
		Tags:        []string{"image"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": ImageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, getThumbnail)

	// POST /preview/image/{area}/
	huma.Register(api, huma.Operation{
		OperationID: "postImagePreview",
		Method:      http.MethodPost,
		Path:        "/preview/image/{area}/",
		Summary:     "Post Image Preview",
		Tags:        []string{"image"},
		Errors:      []int{400, 422},
		Responses: map[string]*huma.Response{
			"200": ImageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, postPreview)

	// POST /preview/image/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "postImageThumbnail",
		Method:      http.MethodPost,
		Path:        "/preview/image/{area}/thumbnail/",
		Summary:     "Post Image Thumbnail",
		Tags:        []string{"image"},
		Errors:      []int{400, 422},
		Responses: map[string]*huma.Response{
			"200": ImageBinaryResponse,
		},
		Middlewares: huma.Middlewares{semMW},
	}, postThumbnail)
}

// ---------------------------------------------------------------------------
// Health operations
// ---------------------------------------------------------------------------

// RegisterHealthOps registers all health-related huma operations.
func RegisterHealthOps(
	api huma.API,
	getLive func(context.Context, *struct{}) (*HealthLiveOutput, error),
	getReady func(context.Context, *struct{}) (*HealthReadyOutput, error),
	getHealth func(context.Context, *struct{}) (*HealthFullOutput, error),
) {
	// GET /health/live/
	huma.Register(api, huma.Operation{
		OperationID:   "getHealthLive",
		Method:        http.MethodGet,
		Path:          "/health/live/",
		Summary:       "Liveness Probe",
		Tags:          []string{"health"},
		DefaultStatus: http.StatusOK,
	}, getLive)

	// GET /health/ready/
	huma.Register(api, huma.Operation{
		OperationID:   "getHealthReady",
		Method:        http.MethodGet,
		Path:          "/health/ready/",
		Summary:       "Readiness Probe",
		Tags:          []string{"health"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{429},
	}, getReady)

	// GET /health/
	huma.Register(api, huma.Operation{
		OperationID:   "getHealth",
		Method:        http.MethodGet,
		Path:          "/health/",
		Summary:       "Health Summary",
		Tags:          []string{"health"},
		DefaultStatus: http.StatusOK,
	}, getHealth)
}

// ---------------------------------------------------------------------------
// PDF operations
// ---------------------------------------------------------------------------

// RegisterPDFOps registers all PDF-related huma operations.
func RegisterPDFOps(
	api huma.API,
	getPreview func(context.Context, *PDFGetPreviewInput) (*BinOut, error),
	getThumbnail func(context.Context, *PDFGetThumbnailInput) (*BinOut, error),
	postPreview func(context.Context, *PDFPostPreviewInput) (*BinOut, error),
	postThumbnail func(context.Context, *PDFPostThumbnailInput) (*BinOut, error),
	semMW func(huma.Context, func(huma.Context)),
) {
	// GET /preview/pdf/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID: "getPdfPreview",
		Method:      http.MethodGet,
		Path:        "/preview/pdf/{id}/{version}/",
		Summary:     "Get PDF Preview",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": PDFBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, getPreview)

	// GET /preview/pdf/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getPdfThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/pdf/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get PDF Thumbnail",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": ImageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, getThumbnail)

	// POST /preview/pdf/
	huma.Register(api, huma.Operation{
		OperationID: "postPdfPreview",
		Method:      http.MethodPost,
		Path:        "/preview/pdf/",
		Summary:     "Post PDF Preview",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 422, 503},
		Responses:   map[string]*huma.Response{"200": PDFBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, postPreview)

	// POST /preview/pdf/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "postPdfThumbnail",
		Method:      http.MethodPost,
		Path:        "/preview/pdf/{area}/thumbnail/",
		Summary:     "Post PDF Thumbnail",
		Tags:        []string{"pdf"},
		Errors:      []int{400, 422, 503},
		Responses:   map[string]*huma.Response{"200": ImageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, postThumbnail)
}

// ---------------------------------------------------------------------------
// Document operations
// ---------------------------------------------------------------------------

// RegisterDocumentOps registers all document-related huma operations.
func RegisterDocumentOps(
	api huma.API,
	getPreview func(context.Context, *DocGetPreviewInput) (*BinOut, error),
	getThumbnail func(context.Context, *DocGetThumbnailInput) (*BinOut, error),
	postPreview func(context.Context, *DocPostPreviewInput) (*BinOut, error),
	postThumbnail func(context.Context, *DocPostThumbnailInput) (*BinOut, error),
	semMW func(huma.Context, func(huma.Context)),
) {
	// GET /preview/document/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID: "getDocumentPreview",
		Method:      http.MethodGet,
		Path:        "/preview/document/{id}/{version}/",
		Summary:     "Get Document Preview",
		Tags:        []string{"document"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": PDFBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, getPreview)

	// GET /preview/document/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getDocumentThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/document/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get Document Thumbnail",
		Tags:        []string{"document"},
		Errors:      []int{400, 404, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": ImageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, getThumbnail)

	// POST /preview/document/
	huma.Register(api, huma.Operation{
		OperationID: "postDocumentPreview",
		Method:      http.MethodPost,
		Path:        "/preview/document/",
		Summary:     "Post Document Preview",
		Tags:        []string{"document"},
		Errors:      []int{400, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": PDFBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, postPreview)

	// POST /preview/document/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "postDocumentThumbnail",
		Method:      http.MethodPost,
		Path:        "/preview/document/{area}/thumbnail/",
		Summary:     "Post Document Thumbnail",
		Tags:        []string{"document"},
		Errors:      []int{400, 422, 502, 503},
		Responses:   map[string]*huma.Response{"200": ImageBinaryResponse},
		Middlewares: huma.Middlewares{semMW},
	}, postThumbnail)
}

// ---------------------------------------------------------------------------
// Video generate operation
// ---------------------------------------------------------------------------

// RegisterGenerateOps registers the video generate huma operation.
func RegisterGenerateOps(
	api huma.API,
	generatePreview func(context.Context, *GenerateVideoInput) (*GenerateVideoOutput, error),
	videoSemMW func(huma.Context, func(huma.Context)),
) {
	// POST /preview/video/generate/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID: "generateVideoPreview",
		Method:      http.MethodPost,
		Path:        "/preview/video/generate/{id}/{version}/",
		Summary:     "Generate (extract + JPEG-encode + store) a video first-frame preview",
		Tags:        []string{"video"},
		Errors:      []int{400, 404, 422, 429, 502, 504},
		Middlewares: huma.Middlewares{videoSemMW},
	}, generatePreview)
}
