// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package server implements the HTTP server for carbonio-preview-ce.
// It registers all four router groups (image, pdf, document, health).
// The process model is a single Go binary with an embedded PDFium subprocess
// pool: libvips runs in-process via CGO, while PDFium rendering is delegated
// to a pool of carbonio-preview-pdfium-worker subprocesses managed by
// go-pdfium's multi_threaded backend.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v3/config"
)

// areaRegex matches the {area} path segment: two non-negative integers
// separated by "x", e.g. "100x200" or "0x0".
var areaRegex = regexp.MustCompile(`^[0-9]+x[0-9]+$`)

// ---------------------------------------------------------------------------
// JSON error shapes (FastAPI-compatible)
// ---------------------------------------------------------------------------

// validationDetail is a single entry in the FastAPI validation error array.
type validationDetail struct {
	Loc  []string `json:"loc"`
	Msg  string   `json:"msg"`
	Type string   `json:"type"`
}

// validationErrorBody is the FastAPI HTTPValidationError body:
//
//	{"detail":[{"loc":["query","<param>"],"msg":"...","type":"value_error"}]}
type validationErrorBody struct {
	Detail []validationDetail `json:"detail"`
}

// stringDetailBody is the FastAPI HTTPException body used for non-validation
// errors (storage, page-combination, render failures, not-found …):
//
//	{"detail":"<message>"}
type stringDetailBody struct {
	Detail string `json:"detail"`
}

// writeJSON serialises v to w with Content-Type application/json and the given
// status code.  If encoding fails it falls back to a plain-text 500.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("writeJSON marshal: %v", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal server error") //nolint:errcheck
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, werr := w.Write(data); werr != nil {
		log.Printf("writeJSON write: %v", werr)
	}
}

// contentTypeForFormat returns the Content-Type for the given image format string.
func contentTypeForFormat(fmt string) string {
	switch fmt {
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

// ---------------------------------------------------------------------------
// Parameter parsers
// ---------------------------------------------------------------------------

// parseArea parses the area path segment (e.g. "100x200") into width and height.
// Returns a validation error (422) on invalid format.
func parseArea(area string) (width, height int, err error) {
	if !areaRegex.MatchString(area) {
		return 0, 0, fmt.Errorf("%s", config.Msg.HeightOrWidthNotInserted)
	}
	parts := strings.SplitN(area, "x", 2)
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("%s", config.Msg.HeightWidthNotValid)
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("%s", config.Msg.HeightWidthNotValid)
	}
	if w < 0 || h < 0 {
		return 0, 0, fmt.Errorf("%s", config.Msg.HeightWidthNotValid)
	}
	return w, h, nil
}

// parseID validates and normalises a UUID path segment (UUID1–UUID4).
// Returns the canonical lower-case UUID string, or an error.
func parseID(id string) (string, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%s", config.Msg.IDNotValid)
	}
	// Reject nil UUID as invalid.
	if u == uuid.Nil {
		return "", fmt.Errorf("%s", config.Msg.IDNotValid)
	}
	return u.String(), nil
}

// cacheKey builds a stable, collision-resistant cache key from the already
// parsed/defaulted request parameters. It uses the REQUESTED area (raw width/
// height), never the post-ConvertRequestedSize values, so it is computable
// before the storages fetch. ownerID is "" for CE (DirectClient) and the
// fileownerid header value for ADV (PowerStore routing).
//
// The leading kind discriminator prevents cross-route aliasing; route-specific
// fields take their parsed default for routes that do not use them, so the
// format is uniform across all six GET handlers.
func cacheKey(
	kind string, // "img-preview" | "img-thumb" | "pdf-preview" | "pdf-thumb" | "doc-preview" | "doc-thumb"
	nodeID string, version int, serviceType string,
	width, height int,
	quality, outputFormat string,
	crop bool, shape string,
	firstPage, lastPage int,
	langTag, ownerID string,
) string {
	return fmt.Sprintf(
		"%s|%s|%d|%s|%d|%d|%s|%s|%t|%s|%d|%d|%s|%s",
		kind, nodeID, version, serviceType,
		width, height, quality, outputFormat,
		crop, shape, firstPage, lastPage, langTag, ownerID,
	)
}
