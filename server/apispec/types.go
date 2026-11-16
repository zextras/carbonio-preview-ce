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

var imageBinaryResponseSchema = &huma.Schema{Type: "string", Format: "binary"}

// ---------------------------------------------------------------------------
// Thumbnails
// ---------------------------------------------------------------------------

type ThumbnailSize string

const (
	ThumbnailSizeS ThumbnailSize = "s"
	ThumbnailSizeM ThumbnailSize = "m"
)

func (ThumbnailSize) Schema(huma.Registry) *huma.Schema {
	return enumStringSchema("Thumbnail size: s = 320 px, m = 1280 px long side",
		string(ThumbnailSizeS), string(ThumbnailSizeM))
}

type ThumbnailGetInput struct {
	ID          string        `path:"id" format:"uuid" doc:"UUID of the file (UUID1-UUID4)"`
	Version     int           `path:"version" minimum:"0" doc:"Version of the file (non-negative integer)"`
	Size        ThumbnailSize `path:"size" doc:"Thumbnail size"`
	ServiceType ServiceType   `query:"service_type" required:"true" doc:"Service that owns the resource"`
	Mime        string        `query:"mime" required:"true" maxLength:"255" doc:"MIME type of the file as recorded by the caller"`
	Digest      string        `query:"digest" maxLength:"128" pattern:"^[A-Za-z0-9+/=_.:-]+$" doc:"Content digest of the version; a different digest regenerates the thumbnail"`
	Lang        string        `query:"lang" default:"en-US" maxLength:"35" pattern:"^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$" doc:"Language tag for office conversions"`
	FileOwnerID string        `header:"fileownerid" required:"false" doc:"File owner ID for Advanced storage routing"`
}

type UploadFiles struct {
	File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"Source file"`
}

type ThumbnailPostInput struct {
	Size    ThumbnailSize `path:"size" doc:"Thumbnail size"`
	Mime    string        `query:"mime" required:"true" maxLength:"255" doc:"MIME type of the uploaded file"`
	Lang    string        `query:"lang" default:"en-US" maxLength:"35" pattern:"^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$" doc:"Language tag for office conversions"`
	RawBody huma.MultipartFormFiles[UploadFiles]
}

type ThumbnailDeleteInput struct {
	ID string `path:"id" format:"uuid" doc:"UUID of the file whose thumbnails (all versions, both sizes) are deleted"`
}

// Unused headers of ThumbnailOutput are left empty, which huma does not write.
type ThumbnailOutput struct {
	Status       int
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	RetryAfter   string `header:"Retry-After"`
	Body         []byte
}

var thumbnailAcceptedHeaders = map[string]*huma.Param{
	"Retry-After":   {Description: "Seconds to wait before polling again", Schema: &huma.Schema{Type: "string"}},
	"Cache-Control": {Description: "no-store", Schema: &huma.Schema{Type: "string"}},
}

var ThumbnailOK = &huma.Response{
	Description: "Successful Response — the thumbnail rendition",
	Content: map[string]*huma.MediaType{
		"image/jpeg": {Schema: imageBinaryResponseSchema},
		"image/png":  {Schema: imageBinaryResponseSchema},
		"image/gif":  {Schema: imageBinaryResponseSchema},
	},
}

var ThumbnailAccepted = &huma.Response{
	Description: "The thumbnail is being generated; poll again after Retry-After seconds",
	Headers:     thumbnailAcceptedHeaders,
	Content: map[string]*huma.MediaType{
		"image/jpeg": {Schema: imageBinaryResponseSchema},
		"image/png":  {Schema: imageBinaryResponseSchema},
	},
}

type PreviewGetInput struct {
	ID          string      `path:"id" format:"uuid" doc:"UUID of the file (UUID1-UUID4)"`
	Version     int         `path:"version" minimum:"0" doc:"Version of the file (non-negative integer)"`
	ServiceType ServiceType `query:"service_type" required:"true" doc:"Service that owns the resource"`
	Mime        string      `query:"mime" required:"true" maxLength:"255" doc:"MIME type of the file as recorded by the caller"`
	Lang        string      `query:"lang" default:"en-US" maxLength:"35" pattern:"^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$" doc:"Language tag for office conversions"`
	FileOwnerID string      `header:"fileownerid" required:"false" doc:"File owner ID for Advanced storage routing"`
}

type PreviewPostInput struct {
	Mime    string `query:"mime" required:"true" maxLength:"255" doc:"MIME type of the uploaded file"`
	Lang    string `query:"lang" default:"en-US" maxLength:"35" pattern:"^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$" doc:"Language tag for office conversions"`
	RawBody huma.MultipartFormFiles[UploadFiles]
}

// Body writes the status and the payload itself, so a source can be streamed without buffering it.
type PreviewOutput struct {
	ContentType   string `header:"Content-Type"`
	ContentLength string `header:"Content-Length"`
	Body          func(huma.Context)
}

var PreviewOK = &huma.Response{
	Description: "Successful Response — the file itself (images, PDF, video) or its conversion to JPEG, PNG or PDF",
	Content: map[string]*huma.MediaType{
		"image/*":         {Schema: imageBinaryResponseSchema},
		"video/*":         {Schema: imageBinaryResponseSchema},
		"application/pdf": {Schema: imageBinaryResponseSchema},
	},
}
