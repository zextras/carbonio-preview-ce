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

func TestParseGRPCQuality_InvalidReturnsInvalidArgument(t *testing.T) {
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
		if st.Code() != codes.InvalidArgument {
			t.Errorf("quality=%q: want INVALID_ARGUMENT, got %s", q, st.Code())
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
	_, _, err := parseGRPCPages(5, 3)
	if err == nil {
		t.Fatal("expected error for first>last, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("want INVALID_ARGUMENT, got %s", st.Code())
	}
}

func TestParseGRPCPages_InvalidNegativeFirstPage(t *testing.T) {
	_, _, err := parseGRPCPages(-1, 0)
	if err == nil {
		t.Fatal("expected error for negative first_page, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("want INVALID_ARGUMENT, got %s", st.Code())
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
