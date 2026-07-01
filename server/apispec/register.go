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

	GetVideoPreview    func(context.Context, *VideoGetPreviewInput) (*BinOut, error)
	GetVideoThumbnail  func(context.Context, *VideoGetThumbnailInput) (*BinOut, error)
	DeleteVideoPreview func(context.Context, *VideoDeleteInput) (*struct{}, error)
	CopyVideoPreview   func(context.Context, *VideoCopyInput) (*VideoCopyOutput, error)
}

// Register registers all huma-managed operations onto api using the provided
// Handlers for the actual request logic. Middleware functions (semaphores) are
// passed explicitly so this package stays free of runtime concerns.
func Register(api huma.API, h Handlers, semMW, videoSemMW func(huma.Context, func(huma.Context))) {
	RegisterImageOps(api, h.GetImagePreview, h.GetImageThumbnail, h.PostImagePreview, h.PostImageThumbnail, semMW)
	RegisterHealthOps(api, h.GetHealthLive, h.GetHealthReady, h.GetHealth)
	RegisterPDFOps(api, h.GetPDFPreview, h.GetPDFThumbnail, h.PostPDFPreview, h.PostPDFThumbnail, semMW)
	RegisterDocumentOps(api, h.GetDocumentPreview, h.GetDocumentThumbnail, h.PostDocumentPreview, h.PostDocumentThumbnail, semMW)
	RegisterVideoOps(api, h.GetVideoPreview, h.GetVideoThumbnail, h.DeleteVideoPreview, h.CopyVideoPreview)
	// NOTE: RegisterGenerateOps is intentionally NOT called here.
	// The public POST /preview/video/generate/... endpoint has been removed (Q5).
	// Generation runs only via the internal worker. The videoSemMW arg is kept in
	// the signature for backward-compatibility with callers; it is unused.
	_ = videoSemMW
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

		GetVideoPreview:    func(_ context.Context, _ *VideoGetPreviewInput) (*BinOut, error) { return nil, nil },
		GetVideoThumbnail:  func(_ context.Context, _ *VideoGetThumbnailInput) (*BinOut, error) { return nil, nil },
		DeleteVideoPreview: func(_ context.Context, _ *VideoDeleteInput) (*struct{}, error) { return nil, nil },
		CopyVideoPreview:   func(_ context.Context, _ *VideoCopyInput) (*VideoCopyOutput, error) { return nil, nil },
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
// Video generate operation (REMOVED — kept for internal generate path only)
// ---------------------------------------------------------------------------

// RegisterGenerateOps is retained for package-API stability but is NO LONGER
// called from Register or RegisterStubs. The public POST /preview/video/generate/
// endpoint has been removed (spec Q5): generation runs only via the internal
// worker / resolve() fast-path. The videoSemMW arg is kept in the signature
// so any Advanced-edition callers that still reference this symbol compile.
func RegisterGenerateOps(
	api huma.API,
	generatePreview func(context.Context, *GenerateVideoInput) (*GenerateVideoOutput, error),
	videoSemMW func(huma.Context, func(huma.Context)),
) {
	// Intentionally empty: the public generate endpoint is removed.
	// The internal generateFirstFrameJPEG function is still used by the worker.
	_, _ = api, generatePreview
	_ = videoSemMW
}

// ---------------------------------------------------------------------------
// Video GET / DELETE / copy operations
// ---------------------------------------------------------------------------

// RegisterVideoOps registers the four video HTTP endpoints onto api:
//
//	GET  /preview/video/{id}/{version}/{area}/
//	GET  /preview/video/{id}/{version}/{area}/thumbnail/
//	DELETE /preview/video/{id}/{version}/
//	POST   /preview/video/{id}/{version}/copy/
func RegisterVideoOps(
	api huma.API,
	getPreview func(context.Context, *VideoGetPreviewInput) (*BinOut, error),
	getThumbnail func(context.Context, *VideoGetThumbnailInput) (*BinOut, error),
	deletePreview func(context.Context, *VideoDeleteInput) (*struct{}, error),
	copyPreview func(context.Context, *VideoCopyInput) (*VideoCopyOutput, error),
) {
	// GET /preview/video/{id}/{version}/{area}/
	huma.Register(api, huma.Operation{
		OperationID: "getVideoPreview",
		Method:      http.MethodGet,
		Path:        "/preview/video/{id}/{version}/{area}/",
		Summary:     "Get Video Preview",
		Description: "Serves the stored first-frame preview of a video attachment. Returns 202 while generation is in progress, 415 for unsupported formats, 422 if generation has permanently failed, 424 when the video-preview database dependency is unavailable (preview core stays healthy; retry once the DB is back).",
		Tags:        []string{"video"},
		Errors:      []int{202, 400, 404, 415, 422, 424, 502, 503},
		Responses: map[string]*huma.Response{
			"200": ImageBinaryResponse,
		},
	}, getPreview)

	// GET /preview/video/{id}/{version}/{area}/thumbnail/
	huma.Register(api, huma.Operation{
		OperationID: "getVideoThumbnail",
		Method:      http.MethodGet,
		Path:        "/preview/video/{id}/{version}/{area}/thumbnail/",
		Summary:     "Get Video Thumbnail",
		Description: "Serves a thumbnail of the stored first-frame preview of a video attachment. Returns 424 when the video-preview database dependency is unavailable (preview core stays healthy; retry once the DB is back).",
		Tags:        []string{"video"},
		Errors:      []int{202, 400, 404, 415, 422, 424, 502, 503},
		Responses: map[string]*huma.Response{
			"200": ImageBinaryResponse,
		},
	}, getThumbnail)

	// DELETE /preview/video/{id}/{version}/
	huma.Register(api, huma.Operation{
		OperationID:   "deleteVideoPreview",
		Method:        http.MethodDelete,
		Path:          "/preview/video/{id}/{version}/",
		Summary:       "Delete Video Preview",
		Description:   "Deletes the stored first-frame preview blob and the video_preview job row. Idempotent: deleting a non-existent preview returns 204. When the video-preview database is unavailable this is a no-op that still returns 204 (safe for fire-and-forget callers); 424 is returned only on a non-connection database error.",
		Tags:          []string{"video"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{422, 424},
	}, deletePreview)

	// POST /preview/video/{id}/{version}/copy/
	huma.Register(api, huma.Operation{
		OperationID: "copyVideoPreview",
		Method:      http.MethodPost,
		Path:        "/preview/video/{id}/{version}/copy/",
		Summary:     "Copy Video Preview",
		Description: "Copies the stored first-frame preview from source to target attachment. Preview mints a new blob UUID for the copy and returns it. Returns 404 if the source preview is not READY. When the video-preview database is unavailable this is a no-op that still returns 200 with an empty body (safe for fire-and-forget callers); 424 is returned only on a non-connection database error.",
		Tags:        []string{"video"},
		Errors:      []int{404, 422, 424, 502},
	}, copyPreview)
}
