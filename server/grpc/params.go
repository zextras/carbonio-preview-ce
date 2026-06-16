// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
func parseGRPCArea(area string) (int, int, error) {
	if !areaRegex.MatchString(area) {
		return 0, 0, status.Errorf(codes.InvalidArgument, "area must be WxH (e.g. 320x240), got %q", area)
	}
	parts := strings.SplitN(area, "x", 2)
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || w < 0 || h < 0 {
		return 0, 0, status.Errorf(codes.InvalidArgument, "invalid area dimensions: %q", area)
	}
	return w, h, nil
}

// defaultOutputFormat returns "jpeg" when the field is empty.
var validOutputFormats = map[string]bool{
	"jpeg": true,
	"png":  true,
	"gif":  true,
}

func defaultOutputFormat(f string) string {
	if f == "" {
		return "jpeg"
	}
	if !validOutputFormats[f] {
		return "jpeg"
	}
	return f
}

// defaultQuality converts the proto int32 quality to the string expected by render.
// The proto carries quality as int32 (0 = unset = default). Mapping:
//
//	0       → "medium"  (proto3 default / unset)
//	1–20    → "lowest"
//	21–40   → "low"
//	41–70   → "medium"
//	71–90   → "high"
//	91–100  → "highest"
//
// This allows callers to express fine-grained quality while the render layer
// still uses the five-bucket model.
func defaultQuality(q int32) string {
	switch {
	case q == 0:
		return "medium"
	case q <= 20:
		return "lowest"
	case q <= 40:
		return "low"
	case q <= 70:
		return "medium"
	case q <= 90:
		return "high"
	default:
		return "highest"
	}
}

// defaultShape returns "rectangular" when the field is empty or unknown.
var validShapes = map[string]bool{
	"rounded":     true,
	"rectangular": true,
}

func defaultShape(s string) string {
	if s == "" || !validShapes[s] {
		return "rectangular"
	}
	return s
}
