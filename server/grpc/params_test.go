// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// parseGRPCQuality
// ---------------------------------------------------------------------------

func TestParseGRPCQuality_EmptyDefaultsMedium(t *testing.T) {
	got, err := parseGRPCQuality("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "medium" {
		t.Errorf("want %q, got %q", "medium", got)
	}
}

func TestParseGRPCQuality_ValidBuckets(t *testing.T) {
	cases := []string{"lowest", "low", "medium", "high", "highest"}
	for _, q := range cases {
		got, err := parseGRPCQuality(q)
		if err != nil {
			t.Errorf("quality=%q: unexpected error: %v", q, err)
			continue
		}
		if got != q {
			t.Errorf("quality=%q: want %q, got %q", q, q, got)
		}
	}
}

func TestParseGRPCQuality_InvalidReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid quality → gRPC must return FAILED_PRECONDITION.
	cases := []string{"80", "high-quality", "MEDIUM", "0", "101"}
	for _, q := range cases {
		_, err := parseGRPCQuality(q)
		if err == nil {
			t.Errorf("quality=%q: expected error, got nil", q)
			continue
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Errorf("quality=%q: expected gRPC status error, got %T", q, err)
			continue
		}
		if st.Code() != codes.FailedPrecondition {
			t.Errorf("quality=%q: want FAILED_PRECONDITION (REST 422), got %s", q, st.Code())
		}
	}
}

// ---------------------------------------------------------------------------
// parseCropMode
// ---------------------------------------------------------------------------

func TestParseCropMode_TrueIsCenter(t *testing.T) {
	if got := parseCropMode(true); got != "center" {
		t.Errorf("parseCropMode(true): want %q, got %q", "center", got)
	}
}

func TestParseCropMode_FalseIsNone(t *testing.T) {
	if got := parseCropMode(false); got != "none" {
		t.Errorf("parseCropMode(false): want %q, got %q", "none", got)
	}
}

// ---------------------------------------------------------------------------
// parseGRPCPages
// ---------------------------------------------------------------------------

func TestParseGRPCPages_ZeroZeroDefaultsOneZero(t *testing.T) {
	fp, lp, err := parseGRPCPages(0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp != 1 {
		t.Errorf("first_page: want 1, got %d", fp)
	}
	if lp != 0 {
		t.Errorf("last_page: want 0, got %d", lp)
	}
}

func TestParseGRPCPages_ExplicitRange(t *testing.T) {
	fp, lp, err := parseGRPCPages(3, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp != 3 {
		t.Errorf("first_page: want 3, got %d", fp)
	}
	if lp != 7 {
		t.Errorf("last_page: want 7, got %d", lp)
	}
}

func TestParseGRPCPages_FirstPageOnlyOpenEnd(t *testing.T) {
	fp, lp, err := parseGRPCPages(5, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp != 5 {
		t.Errorf("first_page: want 5, got %d", fp)
	}
	if lp != 0 {
		t.Errorf("last_page: want 0 (to end), got %d", lp)
	}
}

func TestParseGRPCPages_InvalidFirstPageGreaterThanLast(t *testing.T) {
	// REST returns HTTP 422 for invalid page range → gRPC must return FAILED_PRECONDITION.
	_, _, err := parseGRPCPages(5, 3)
	if err == nil {
		t.Fatal("expected error for first>last, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FAILED_PRECONDITION (REST 422), got %s", st.Code())
	}
}

func TestParseGRPCPages_InvalidNegativeFirstPage(t *testing.T) {
	_, _, err := parseGRPCPages(-1, 0)
	if err == nil {
		t.Fatal("expected error for negative first_page, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FAILED_PRECONDITION (REST 422), got %s", st.Code())
	}
}

// ---------------------------------------------------------------------------
// parseGRPCArea
// ---------------------------------------------------------------------------

func TestParseGRPCArea_InvalidReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid area → gRPC must return FAILED_PRECONDITION.
	cases := []string{"320", "x240", "320x", "widthxheight", ""}
	for _, a := range cases {
		_, _, err := parseGRPCArea(a)
		if err == nil {
			t.Errorf("area=%q: expected error, got nil", a)
			continue
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Errorf("area=%q: expected gRPC status error, got %T", a, err)
			continue
		}
		if st.Code() != codes.FailedPrecondition {
			t.Errorf("area=%q: want FAILED_PRECONDITION (REST 422), got %s", a, st.Code())
		}
	}
}

// ---------------------------------------------------------------------------
// parseOutputFormat
// ---------------------------------------------------------------------------

func TestParseOutputFormat_EmptyDefaultsJpeg(t *testing.T) {
	got, err := parseOutputFormat("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "jpeg" {
		t.Errorf("want %q, got %q", "jpeg", got)
	}
}

func TestParseOutputFormat_ValidValues(t *testing.T) {
	cases := []string{"jpeg", "png", "gif"}
	for _, f := range cases {
		got, err := parseOutputFormat(f)
		if err != nil {
			t.Errorf("output_format=%q: unexpected error: %v", f, err)
			continue
		}
		if got != f {
			t.Errorf("output_format=%q: want %q, got %q", f, f, got)
		}
	}
}

func TestParseOutputFormat_InvalidNonEmptyReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid output_format → gRPC must return FAILED_PRECONDITION.
	// Notably, silently coercing to "jpeg" (old behaviour) is WRONG — must error.
	cases := []string{"bmp", "webp", "JPEG", "jpg"}
	for _, f := range cases {
		_, err := parseOutputFormat(f)
		if err == nil {
			t.Errorf("output_format=%q: expected error, got nil", f)
			continue
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Errorf("output_format=%q: expected gRPC status error, got %T", f, err)
			continue
		}
		if st.Code() != codes.FailedPrecondition {
			t.Errorf("output_format=%q: want FAILED_PRECONDITION (REST 422), got %s", f, st.Code())
		}
	}
}

// ---------------------------------------------------------------------------
// parseShape
// ---------------------------------------------------------------------------

func TestParseShape_EmptyDefaultsRectangular(t *testing.T) {
	got, err := parseShape("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "rectangular" {
		t.Errorf("want %q, got %q", "rectangular", got)
	}
}

func TestParseShape_ValidValues(t *testing.T) {
	cases := []string{"rectangular", "rounded"}
	for _, s := range cases {
		got, err := parseShape(s)
		if err != nil {
			t.Errorf("shape=%q: unexpected error: %v", s, err)
			continue
		}
		if got != s {
			t.Errorf("shape=%q: want %q, got %q", s, s, got)
		}
	}
}

func TestParseShape_InvalidNonEmptyReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid shape → gRPC must return FAILED_PRECONDITION.
	// Silently coercing to "rectangular" (old behaviour) is WRONG — must error.
	cases := []string{"circle", "square", "ROUNDED"}
	for _, s := range cases {
		_, err := parseShape(s)
		if err == nil {
			t.Errorf("shape=%q: expected error, got nil", s)
			continue
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Errorf("shape=%q: expected gRPC status error, got %T", s, err)
			continue
		}
		if st.Code() != codes.FailedPrecondition {
			t.Errorf("shape=%q: want FAILED_PRECONDITION (REST 422), got %s", s, st.Code())
		}
	}
}

// ---------------------------------------------------------------------------
// parseGRPCLangTag
// ---------------------------------------------------------------------------

func TestParseGRPCLangTag_EmptyDefaultsEnUS(t *testing.T) {
	if got := parseGRPCLangTag(""); got != "en-US" {
		t.Errorf("want %q, got %q", "en-US", got)
	}
}

func TestParseGRPCLangTag_PassThrough(t *testing.T) {
	cases := []string{"it-IT", "de-DE", "fr-FR", "en-US"}
	for _, tag := range cases {
		if got := parseGRPCLangTag(tag); got != tag {
			t.Errorf("lang_tag=%q: want %q, got %q", tag, tag, got)
		}
	}
}
