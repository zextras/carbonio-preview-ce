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

	"github.com/zextras/carbonio-preview-ce/config"
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

// errValidation writes 422 + FastAPI array-detail JSON for a single param.
//
//	loc[0] = "path" | "query" | "body"
//	loc[1] = param name
func errValidation(w http.ResponseWriter, locKind, paramName, msg string) {
	body := validationErrorBody{
		Detail: []validationDetail{
			{
				Loc:  []string{locKind, paramName},
				Msg:  msg,
				Type: "value_error",
			},
		},
	}
	writeJSON(w, http.StatusUnprocessableEntity, body)
}

// errDetail writes 422 + FastAPI string-detail JSON.
// Used for page-combination failures and storage failures.
func errDetail(w http.ResponseWriter, statusCode int, msg string) {
	writeJSON(w, statusCode, stringDetailBody{Detail: msg})
}

// errNotFound writes 404 + {"detail":"<msg>"}.
func errNotFound(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusNotFound, stringDetailBody{Detail: msg})
}

// errBadRequest writes 400 + {"detail":"<msg>"}.
// Used for render failures (FastAPI returns 400 with HTTPException).
func errBadRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, stringDetailBody{Detail: msg})
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

// parseVersion validates and parses a non-negative integer version path segment.
func parseVersion(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s", config.Msg.VersionNotValid)
	}
	return n, nil
}

// validServiceTypes holds the set of accepted service_type query values.
var validServiceTypes = map[string]bool{
	"files": true,
	"chats": true,
}

// parseServiceType validates the service_type query parameter.
func parseServiceType(r *http.Request) (string, error) {
	st := r.URL.Query().Get("service_type")
	if st == "" {
		return "", fmt.Errorf("%s", config.Msg.InputError)
	}
	if !validServiceTypes[st] {
		return "", fmt.Errorf("%s", config.Msg.InputError)
	}
	return st, nil
}

// validOutputFormats holds the accepted output_format values.
var validOutputFormats = map[string]bool{
	"jpeg": true,
	"png":  true,
	"gif":  true,
}

// parseOutputFormat returns the output_format query param (default "jpeg").
func parseOutputFormat(r *http.Request) (string, error) {
	v := r.URL.Query().Get("output_format")
	if v == "" {
		return "jpeg", nil
	}
	if !validOutputFormats[v] {
		return "", fmt.Errorf("%s", config.Msg.InputError)
	}
	return v, nil
}

// validQualities holds the accepted quality values.
var validQualities = map[string]bool{
	"lowest":  true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"highest": true,
}

// parseQuality returns the quality query param (default "medium").
func parseQuality(r *http.Request) (string, error) {
	v := r.URL.Query().Get("quality")
	if v == "" {
		return "medium", nil
	}
	if !validQualities[v] {
		return "", fmt.Errorf("%s", config.Msg.InputError)
	}
	return v, nil
}

// validShapes holds the accepted shape values.
var validShapes = map[string]bool{
	"rounded":     true,
	"rectangular": true,
}

// parseShape returns the shape query param (default "rectangular").
func parseShape(r *http.Request) (string, error) {
	v := r.URL.Query().Get("shape")
	if v == "" {
		return "rectangular", nil
	}
	if !validShapes[v] {
		return "", fmt.Errorf("%s", config.Msg.InputError)
	}
	return v, nil
}

// parseCrop returns the crop query param (default false).
func parseCrop(r *http.Request) (bool, error) {
	v := r.URL.Query().Get("crop")
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s", config.Msg.InputError)
	}
	return b, nil
}

// parsePages returns first_page (default 1) and last_page (default 0).
// Returns error if the page combination is invalid per the Python spec:
// first_page >= 1 AND (first_page <= last_page OR last_page == 0).
func parsePages(r *http.Request) (firstPage, lastPage int, err error) {
	firstPage = 1
	lastPage = 0

	if v := r.URL.Query().Get("first_page"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			return 0, 0, fmt.Errorf("%s", config.Msg.NumberOfPagesNotValid)
		}
		firstPage = n
	}
	if v := r.URL.Query().Get("last_page"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 {
			return 0, 0, fmt.Errorf("%s", config.Msg.NumberOfPagesNotValid)
		}
		lastPage = n
	}

	// Validate: first_page >= 1 AND (first_page <= last_page OR last_page == 0)
	if firstPage < 1 || (lastPage != 0 && firstPage > lastPage) {
		return 0, 0, fmt.Errorf("%s", config.Msg.NumberOfPagesNotValid)
	}
	return firstPage, lastPage, nil
}

// parseLangTag returns the lang_tag query param (default "en-US").
func parseLangTag(r *http.Request) string {
	v := r.URL.Query().Get("lang_tag")
	if v == "" {
		return "en-US"
	}
	return v
}

// ownerID reads the fileownerid header (case-insensitive).
// CE's DirectClient ignores it; Advanced's PowerStore uses it for routing.
func ownerID(r *http.Request) string {
	return r.Header.Get("fileownerid")
}
