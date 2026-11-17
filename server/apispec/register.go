// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// uploadReadTimeout turns off huma's per-operation body deadline; the server bounds uploads by inactivity.
const uploadReadTimeout time.Duration = -1

// ---------------------------------------------------------------------------
// Handlers — injected handler functions, one per huma operation
// ---------------------------------------------------------------------------

// Handlers holds one typed function per API operation. The live server fills
// each field with a closure that calls the real render/video/storage code.
// RegisterStubs uses nil-returning no-ops so that huma can register and
// introspect all operations without executing any rendering.
type Handlers struct {
	GetHealthLive  func(context.Context, *struct{}) (*HealthLiveOutput, error)
	GetHealthReady func(context.Context, *struct{}) (*HealthReadyOutput, error)
	GetHealth      func(context.Context, *struct{}) (*HealthFullOutput, error)

	GetThumbnail     func(context.Context, *ThumbnailGetInput) (*ThumbnailOutput, error)
	PostThumbnail    func(context.Context, *ThumbnailPostInput) (*ThumbnailOutput, error)
	DeleteThumbnails func(context.Context, *ThumbnailDeleteInput) (*struct{}, error)

	GetPreview  func(context.Context, *PreviewGetInput) (*PreviewOutput, error)
	PostPreview func(context.Context, *PreviewPostInput) (*PreviewOutput, error)
}

// Register registers all huma-managed operations onto api using the provided
// Handlers for the actual request logic.
func Register(api huma.API, h Handlers) {
	RegisterHealthOps(api, h.GetHealthLive, h.GetHealthReady, h.GetHealth)
	RegisterThumbnailOps(api, h.GetThumbnail, h.PostThumbnail, h.DeleteThumbnails)
	RegisterPreviewOps(api, h.GetPreview, h.PostPreview)
}

// RegisterStubs registers all operations with no-op stub handlers.
// The stubs return zero values and no error; handler bodies are never executed.
// Use this in cmd/gendocs or any context that only needs the OpenAPI spec.
func RegisterStubs(api huma.API) {
	h := Handlers{
		GetHealthLive:  func(_ context.Context, _ *struct{}) (*HealthLiveOutput, error) { return &HealthLiveOutput{}, nil },
		GetHealthReady: func(_ context.Context, _ *struct{}) (*HealthReadyOutput, error) { return &HealthReadyOutput{}, nil },
		GetHealth: func(_ context.Context, _ *struct{}) (*HealthFullOutput, error) {
			return &HealthFullOutput{Body: &HealthResponse{Ready: true}}, nil
		},

		GetThumbnail:     func(_ context.Context, _ *ThumbnailGetInput) (*ThumbnailOutput, error) { return nil, nil },
		PostThumbnail:    func(_ context.Context, _ *ThumbnailPostInput) (*ThumbnailOutput, error) { return nil, nil },
		DeleteThumbnails: func(_ context.Context, _ *ThumbnailDeleteInput) (*struct{}, error) { return nil, nil },

		GetPreview:  func(_ context.Context, _ *PreviewGetInput) (*PreviewOutput, error) { return nil, nil },
		PostPreview: func(_ context.Context, _ *PreviewPostInput) (*PreviewOutput, error) { return nil, nil },
	}
	Register(api, h)
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
// Thumbnail operations
// ---------------------------------------------------------------------------

func RegisterThumbnailOps(
	api huma.API,
	getThumbnail func(context.Context, *ThumbnailGetInput) (*ThumbnailOutput, error),
	postThumbnail func(context.Context, *ThumbnailPostInput) (*ThumbnailOutput, error),
	deleteThumbnails func(context.Context, *ThumbnailDeleteInput) (*struct{}, error),
) {
	huma.Register(api, huma.Operation{
		OperationID: "getThumbnail",
		Method:      http.MethodGet,
		Path:        "/thumbnail/{id}/{version}/{size}",
		Summary:     "Get Thumbnail",
		Description: "Serves the thumbnail of a file version, stored per file, version and size (s = 320 px, m = 1280 px long side, never upscaled), always re-encoded: JPEG (PNG when alpha is used; Q50 for s, Q80 for m; metadata stripped; EXIF rotation applied) or, for animated GIFs, an animated GIF capped at 10 fps. Images and PDFs are generated synchronously. Video, office and animated GIF thumbnails are generated asynchronously: 202 with Retry-After while generating (an animated GIF answers with its static first frame as body), 415 for unsupported formats, 422 when generation permanently failed. Without a usable database nothing is stored and every thumbnail, asynchronous kinds included, is generated in the request and kept in memory only. A digest different from the stored one regenerates the thumbnail.",
		Tags:        []string{"thumbnail"},
		Errors:      []int{400, 404, 415, 422, 424, 502, 503},
		Responses: map[string]*huma.Response{
			"200": ThumbnailOK,
			"202": ThumbnailAccepted,
		},
	}, getThumbnail)

	huma.Register(api, huma.Operation{
		OperationID: "postThumbnail",
		Method:      http.MethodPost,
		Path:        "/thumbnail/{size}",
		Summary:     "Post Thumbnail",
		Description: "Generates the thumbnail of the uploaded file synchronously, for every supported MIME type. The result is never stored or cached and storages and the database are never touched.",
		Tags:        []string{"thumbnail"},
		Errors:      []int{400, 415, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": ThumbnailOK,
		},
		BodyReadTimeout: uploadReadTimeout,
	}, postThumbnail)

	huma.Register(api, huma.Operation{
		OperationID:   "deleteThumbnails",
		Method:        http.MethodDelete,
		Path:          "/thumbnail/{id}",
		Summary:       "Delete Thumbnails",
		Description:   "Deletes every stored thumbnail (all versions, both sizes) of a file: the blobs and the rows. Idempotent: deleting a file without thumbnails returns 204. When the thumbnail database is unavailable this is a no-op that still returns 204 (safe for fire-and-forget callers); 424 is returned only on a non-connection database error.",
		Tags:          []string{"thumbnail"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{422, 424},
	}, deleteThumbnails)
}

// ---------------------------------------------------------------------------
// Preview operations
// ---------------------------------------------------------------------------

func RegisterPreviewOps(
	api huma.API,
	getPreview func(context.Context, *PreviewGetInput) (*PreviewOutput, error),
	postPreview func(context.Context, *PreviewPostInput) (*PreviewOutput, error),
) {
	huma.Register(api, huma.Operation{
		OperationID: "getPreview",
		Method:      http.MethodGet,
		Path:        "/preview/{id}/{version}",
		Summary:     "Get Preview",
		Description: "Serves the preview of a file version synchronously, never stored or cached. Browser-native images (JPEG, PNG, GIF, WebP, AVIF, BMP), PDF and video are streamed from storages unchanged with the caller's MIME type as Content-Type. TIFF, HEIC/HEIF and SVG are converted to JPEG (PNG when transparency is used) at full resolution. Office documents are converted whole to PDF (400 when document.enable-preview is off). 415 for unsupported formats.",
		Tags:        []string{"preview"},
		Errors:      []int{400, 404, 415, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": PreviewOK,
		},
	}, getPreview)

	huma.Register(api, huma.Operation{
		OperationID: "postPreview",
		Method:      http.MethodPost,
		Path:        "/preview",
		Summary:     "Post Preview",
		Description: "Same as getPreview for the uploaded file: passthrough or conversion, never stored or cached, storages and the database are never touched.",
		Tags:        []string{"preview"},
		Errors:      []int{400, 415, 422, 502, 503},
		Responses: map[string]*huma.Response{
			"200": PreviewOK,
		},
		BodyReadTimeout: uploadReadTimeout,
	}, postPreview)
}
