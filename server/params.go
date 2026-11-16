// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package server implements the HTTP server for carbonio-preview-ce.
// It registers the thumbnail, preview and health operations.
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

	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v4/config"
)

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
