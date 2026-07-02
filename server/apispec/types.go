// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package apispec contains all huma request/response types, huma.Operation
// metadata, and the Register/RegisterStubs entry-points for the preview API.
//
// This package is deliberately cgo-free: it imports only huma and the stdlib.
// The rendering implementations (render, video) are injected by the caller via
// the Handlers struct.  cmd/gendocs uses RegisterStubs (no rendering needed);
// the live server calls Register with real handler functions built from its Deps.
package apispec

import (
	"github.com/danielgtaylor/huma/v2"
)

// ---------------------------------------------------------------------------
// Helper
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

// ---------------------------------------------------------------------------
// Enum types (huma generates component schemas from these)
// ---------------------------------------------------------------------------

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

// ImageGetPreviewInput holds all path + query params for GET image preview.
// Fields are listed inline (not embedded) to avoid huma's embedded struct skipping.
type ImageGetPreviewInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the image (UUID1–UUID4)"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version of the image (non-negative integer)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Crop         bool        `query:"crop"          default:"false"             doc:"Crop to fill (true) or scale to fit (false)"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID for Advanced storage routing" required:"false"`
}

// ImageGetThumbnailInput holds all path + query params for GET image thumbnail.
type ImageGetThumbnailInput struct {
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

// ImagePostFiles is the multipart body schema for POST image operations.
type ImagePostFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"Image file to process"`
}

// ImagePostPreviewInput holds path + query params + multipart body for POST preview.
type ImagePostPreviewInput struct {
	Area         string    `path:"area" pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	Quality      Quality   `query:"quality"       default:"medium" doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"   doc:"Output image format"`
	Crop         bool      `query:"crop"          default:"false"  doc:"Crop to fill (true) or scale to fit (false)"`
	RawBody      huma.MultipartFormFiles[ImagePostFiles]
}

// ImagePostThumbnailInput holds path + query params + multipart body for POST thumbnail.
type ImagePostThumbnailInput struct {
	Area         string    `path:"area" pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	Quality      Quality   `query:"quality"       default:"medium" doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"   doc:"Output image format"`
	Shape        Shape     `query:"shape"         default:"rectangular" doc:"Thumbnail border shape"`
	RawBody      huma.MultipartFormFiles[ImagePostFiles]
}

// ---------------------------------------------------------------------------
// Health output structs
// ---------------------------------------------------------------------------

// HealthLiveOutput is returned by GET /health/live/.
type HealthLiveOutput struct{}

// HealthReadyOutput is returned by GET /health/ready/ (empty body on success).
type HealthReadyOutput struct{}

// HealthDependency is the JSON object for a single dependency in /health/.
// Shape mirrors the old FastAPI implementation (health.py:50-66).
type HealthDependency struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	Live  bool   `json:"live"`
	Type  string `json:"type"`
}

// HealthResponse is the JSON body for GET /health/.
type HealthResponse struct {
	Ready        bool               `json:"ready"`
	Dependencies []HealthDependency `json:"dependencies"`
}

// HealthFullOutput wraps the JSON health response.
type HealthFullOutput struct {
	Body *HealthResponse
}

// ---------------------------------------------------------------------------
// Binary response schema helper
// ---------------------------------------------------------------------------

var imageBinaryResponseSchema = &huma.Schema{Type: "string", Format: "binary"}

// ImageBinaryResponse defines the 200 response for image operations with
// multiple possible content types.
var ImageBinaryResponse = &huma.Response{
	Description: "Successful Response — binary image data",
	Content: map[string]*huma.MediaType{
		"image/jpeg": {Schema: imageBinaryResponseSchema},
		"image/png":  {Schema: imageBinaryResponseSchema},
		"image/gif":  {Schema: imageBinaryResponseSchema},
	},
}

// PDFBinaryResponse defines the 200 response for PDF operations.
var PDFBinaryResponse = &huma.Response{
	Description: "Successful Response — PDF bytes",
	Content: map[string]*huma.MediaType{
		"application/pdf": {Schema: imageBinaryResponseSchema},
	},
}

// ---------------------------------------------------------------------------
// Input structs — PDF GET
// ---------------------------------------------------------------------------

// PDFGetPreviewInput holds all path + query params for GET PDF preview.
type PDFGetPreviewInput struct {
	ID          string      `path:"id"            format:"uuid"              doc:"UUID of the PDF"`
	Version     int         `path:"version"       minimum:"0"                doc:"Version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true"            doc:"Service that owns the resource"`
	FirstPage   int         `query:"first_page"   default:"1" doc:"First page (1-based, default 1)"`
	LastPage    int         `query:"last_page"    default:"0" doc:"Last page (0 = all, default 0)"`
	FileOwnerID string      `header:"fileownerid"             doc:"File owner ID" required:"false"`
}

// PDFGetThumbnailInput holds all path + query params for GET PDF thumbnail.
type PDFGetThumbnailInput struct {
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

// PDFPostFiles is the multipart body schema for POST PDF operations.
type PDFPostFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"PDF file to process"`
}

// PDFPostPreviewInput holds query params + multipart body for POST PDF preview.
type PDFPostPreviewInput struct {
	FirstPage int `query:"first_page" default:"1" doc:"First page (1-based, default 1)"`
	LastPage  int `query:"last_page"  default:"0" doc:"Last page (0 = all, default 0)"`
	RawBody   huma.MultipartFormFiles[PDFPostFiles]
}

// PDFPostThumbnailInput holds path + query params + multipart body for POST PDF thumbnail.
type PDFPostThumbnailInput struct {
	Area         string    `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	Quality      Quality   `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape     `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	RawBody      huma.MultipartFormFiles[PDFPostFiles]
}

// ---------------------------------------------------------------------------
// Input structs — Document GET
// ---------------------------------------------------------------------------

// DocGetPreviewInput holds all path + query params for GET document preview.
type DocGetPreviewInput struct {
	ID          string      `path:"id"            format:"uuid"              doc:"UUID of the document"`
	Version     int         `path:"version"       minimum:"0"                doc:"Version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true"            doc:"Service that owns the resource"`
	FirstPage   int         `query:"first_page"   default:"1" doc:"First page (1-based, default 1)"`
	LastPage    int         `query:"last_page"    default:"0" doc:"Last page (0 = all, default 0)"`
	LangTag     string      `query:"lang_tag"     default:"en-US" doc:"Language tag for conversion (default en-US)"`
	FileOwnerID string      `header:"fileownerid"               doc:"File owner ID" required:"false"`
}

// DocGetThumbnailInput holds all path + query params for GET document thumbnail.
type DocGetThumbnailInput struct {
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

// DocPostFiles is the multipart body schema for POST document operations.
type DocPostFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"Document file to process"`
}

// DocPostPreviewInput holds query params + multipart body for POST document preview.
type DocPostPreviewInput struct {
	FirstPage int    `query:"first_page" default:"1"     doc:"First page (1-based, default 1)"`
	LastPage  int    `query:"last_page"  default:"0"     doc:"Last page (0 = all, default 0)"`
	LangTag   string `query:"lang_tag"   default:"en-US" doc:"Language tag for conversion"`
	RawBody   huma.MultipartFormFiles[DocPostFiles]
}

// DocPostThumbnailInput holds path + query params + multipart body for POST document thumbnail.
type DocPostThumbnailInput struct {
	Area         string    `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels"`
	Quality      Quality   `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape     `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	LangTag      string    `query:"lang_tag"                      default:"en-US" doc:"Language tag for conversion"`
	RawBody      huma.MultipartFormFiles[DocPostFiles]
}

// ---------------------------------------------------------------------------
// Input/output structs — Video GET preview / thumbnail / delete / copy
// ---------------------------------------------------------------------------

// VideoGetPreviewInput holds all path + query params for GET video preview.
// Mirrors ImageGetPreviewInput, adding service_type (already present on image inputs).
type VideoGetPreviewInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the video attachment"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version of the video (non-negative integer)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Crop         bool        `query:"crop"          default:"false"             doc:"Crop to fill (true) or scale to fit (false)"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID for Advanced storage routing" required:"false"`
}

// VideoGetThumbnailInput holds all path + query params for GET video thumbnail.
// Mirrors ImageGetThumbnailInput.
type VideoGetThumbnailInput struct {
	ID           string      `path:"id"             format:"uuid"              doc:"UUID of the video attachment"`
	Version      int         `path:"version"        minimum:"0"                doc:"Version of the video (non-negative integer)"`
	Area         string      `path:"area"           pattern:"^[0-9]+x[0-9]+$" doc:"Width x height in pixels, e.g. 100x200"`
	ServiceType  ServiceType `query:"service_type"  required:"true"            doc:"Service that owns the resource"`
	Quality      Quality     `query:"quality"       default:"medium"            doc:"Output quality"`
	OutputFormat ImageType   `query:"output_format" default:"jpeg"              doc:"Output image format"`
	Shape        Shape       `query:"shape"         default:"rectangular"       doc:"Thumbnail border shape"`
	FileOwnerID  string      `header:"fileownerid"                             doc:"File owner ID for Advanced storage routing" required:"false"`
}

// VideoDeleteInput holds path + query params for DELETE video preview.
type VideoDeleteInput struct {
	ID          string      `path:"id"            format:"uuid"   doc:"UUID of the video attachment"`
	Version     int         `path:"version"       minimum:"0"     doc:"Version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true" doc:"Service that owns the resource"`
	FileOwnerID string      `header:"fileownerid"                doc:"File owner ID for Advanced storage routing" required:"false"`
}

// VideoCopyInput holds path + query params + headers for POST video copy.
// {id} is the SOURCE attachment file_id; target is the DESTINATION file_id.
type VideoCopyInput struct {
	ID            string      `path:"id"             format:"uuid"   doc:"UUID of the SOURCE video attachment"`
	Version       int         `path:"version"        minimum:"0"     doc:"Source version (non-negative)"`
	ServiceType   ServiceType `query:"service_type"  required:"true" doc:"Service that owns the resource"`
	Target        string      `query:"target"        format:"uuid" required:"true" doc:"UUID of the DESTINATION attachment (new file_id)"`
	FileOwnerID   string      `header:"fileownerid"                 doc:"Source file owner ID" required:"false"`
	TargetOwnerID string      `header:"targetownerid"               doc:"Destination file owner ID" required:"false"`
}

// VideoCopyOutput is the JSON body for a successful POST copy response.
type VideoCopyOutput struct {
	Body struct {
		PreviewID string `json:"preview_id" doc:"Storage node id of the copied preview frame"`
	}
}

// ---------------------------------------------------------------------------
// Input/output structs — Video generate (POST)
// ---------------------------------------------------------------------------

// GenerateVideoInput holds the path + query params + owner header for the
// POST /preview/video/generate endpoint. The caller (WSC) supplies BOTH the
// source coordinates (id/version/service_type/owner) AND the target node id
// (a UUID it minted) under which the extracted first frame is stored.
type GenerateVideoInput struct {
	ID          string      `path:"id"            format:"uuid"   doc:"UUID of the SOURCE video node"`
	Version     int         `path:"version"       minimum:"0"     doc:"Source version (non-negative)"`
	ServiceType ServiceType `query:"service_type" required:"true" doc:"Service that owns the resource"`
	Target      string      `query:"target"       format:"uuid" required:"true" doc:"Caller-minted UUID for the stored frame"`
	FileOwnerID string      `header:"fileownerid"                doc:"File owner ID (PowerStore routing)" required:"false"`
}

// GenerateVideoOutput echoes the stored node id of the generated frame.
type GenerateVideoOutput struct {
	Body struct {
		PreviewID string `json:"preview_id" doc:"Storage node id of the stored first-frame image (echoes target)"`
	}
}
