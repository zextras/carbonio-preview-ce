// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteJSON_MarshalError_500 covers the json.Marshal error fallback arm of
// writeJSON (params.go): when the value cannot be marshalled, it must emit a
// plain-text 500 instead of partial JSON. A chan is not JSON-marshalable.
// This is a same-package test, so writeJSON is directly callable — no
// production seam is required.
func TestWriteJSON_MarshalError_500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"bad": make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code=%d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type=%q, want text/plain", ct)
	}
	if rec.Body.String() != "internal server error" {
		t.Errorf("body=%q, want %q", rec.Body.String(), "internal server error")
	}
}

// TestWriteJSON_Success verifies the happy path: marshalable value → JSON body
// with the requested status and application/json content-type.
func TestWriteJSON_Success(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusTeapot, stringDetailBody{Detail: "hi"})

	if rec.Code != http.StatusTeapot {
		t.Errorf("code=%d, want 418", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"detail":"hi"`) {
		t.Errorf("body=%q, want detail:hi", rec.Body.String())
	}
}
