// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var areaRegex = regexp.MustCompile(`^[0-9]+x[0-9]+$`)

// validServiceTypes mirrors server.validServiceTypes.
var validServiceTypes = map[string]bool{
	"files": true,
	"chats": true,
}

// validateUUID validates a UUID string and returns its canonical form.
func validateUUID(id string) (string, error) {
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return "", fmt.Errorf("invalid UUID")
	}
	return u.String(), nil
}

// validateServiceType validates the service_type field.
func validateServiceType(st string) (string, error) {
	if !validServiceTypes[st] {
		return "", fmt.Errorf("must be one of: files, chats")
	}
	return st, nil
}

// parseGRPCArea parses the area string (e.g. "320x240") into width and height.
// Returns 422 FAILED_PRECONDITION on invalid input, mirroring REST errValidation (HTTP 422).
func parseGRPCArea(area string) (int, int, error) {
	if !areaRegex.MatchString(area) {
		return 0, 0, toStatus(http.StatusUnprocessableEntity, fmt.Sprintf("area must be WxH (e.g. 320x240), got %q", area))
	}
	parts := strings.SplitN(area, "x", 2)
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || w < 0 || h < 0 {
		return 0, 0, toStatus(http.StatusUnprocessableEntity, fmt.Sprintf("invalid area dimensions: %q", area))
	}
	return w, h, nil
}

// defaultOutputFormats holds the accepted output_format values.
// Mirrors server.validOutputFormats.
var validOutputFormats = map[string]bool{
	"jpeg": true,
	"png":  true,
	"gif":  true,
}

// parseOutputFormat returns the effective output format.
// Empty → "jpeg" (proto3 zero value = unset, mirrors REST default).
// Non-empty invalid → 422 FAILED_PRECONDITION, mirroring REST parseOutputFormat (HTTP 422).
func parseOutputFormat(f string) (string, error) {
	if f == "" {
		return "jpeg", nil
	}
	if !validOutputFormats[f] {
		return "", toStatus(http.StatusUnprocessableEntity,
			fmt.Sprintf("output_format must be one of jpeg|png|gif, got %q", f))
	}
	return f, nil
}

// validQualities mirrors server.validQualities.
var validQualities = map[string]bool{
	"lowest":  true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"highest": true,
}

// parseGRPCQuality validates the quality string field and returns the effective
// bucket string, mirroring server.parseQuality exactly:
//   - empty string → "medium" (proto3 zero value = unset)
//   - valid bucket → passed through unchanged
//   - anything else → 422 FAILED_PRECONDITION (mirrors REST HTTP 422)
func parseGRPCQuality(q string) (string, error) {
	if q == "" {
		return "medium", nil
	}
	if !validQualities[q] {
		return "", toStatus(http.StatusUnprocessableEntity,
			fmt.Sprintf("quality must be one of lowest|low|medium|high|highest, got %q", q))
	}
	return q, nil
}

// parseCropMode converts the bool crop field to the cropMode string expected by
// render.ImageThumbnail, mirroring server.parseCrop + the REST image handler:
//
//	crop=true  → "center"  (cover/center-crop)
//	crop=false → "none"    (scale-to-fit)
func parseCropMode(crop bool) string {
	if crop {
		return "center"
	}
	return "none"
}

// parseGRPCPages normalises the gRPC first_page / last_page fields, mirroring
// server.parsePages:
//
//	first_page 0  → default 1 (proto3 zero value = unset)
//	last_page  0  → default 0 (means "to end")
//
// Returns 422 FAILED_PRECONDITION if the page combination is invalid,
// mirroring REST errDetail(w, StatusUnprocessableEntity, …):
//
//	first_page >= 1 AND (first_page <= last_page OR last_page == 0)
func parseGRPCPages(firstPage, lastPage int32) (int, int, error) {
	fp := int(firstPage)
	lp := int(lastPage)
	if fp == 0 {
		fp = 1
	}
	if fp < 1 || (lp != 0 && fp > lp) {
		return 0, 0, toStatus(http.StatusUnprocessableEntity,
			fmt.Sprintf("invalid page range: first_page=%d last_page=%d", firstPage, lastPage))
	}
	return fp, lp, nil
}

// parseGRPCLangTag returns the effective lang_tag, mirroring server.parseLangTag:
//
//	empty string → "en-US"
func parseGRPCLangTag(langTag string) string {
	if langTag == "" {
		return "en-US"
	}
	return langTag
}

// validShapes mirrors server.validShapes.
var validShapes = map[string]bool{
	"rounded":     true,
	"rectangular": true,
}

// parseShape returns the effective shape value.
// Empty → "rectangular" (proto3 zero value = unset, mirrors REST default).
// Non-empty invalid → 422 FAILED_PRECONDITION, mirroring REST parseShape (HTTP 422).
// "rounded" forces PNG output in the caller (mirrors REST renderPDFThumbnail behaviour).
func parseShape(s string) (string, error) {
	if s == "" {
		return "rectangular", nil
	}
	if !validShapes[s] {
		return "", toStatus(http.StatusUnprocessableEntity,
			fmt.Sprintf("shape must be one of rectangular|rounded, got %q", s))
	}
	return s, nil
}
