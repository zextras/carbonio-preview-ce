// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

func init() { //nolint:gochecknoinits
	huma.NewError = newFastAPIError
}

// fastAPIError implements huma.StatusError with FastAPI-compatible JSON bodies:
//   - 422 with error details → array-detail: {"detail":[{"loc":[...],"msg":...,"type":"value_error"}]}
//   - all other statuses    → string-detail: {"detail":"<message>"}
//
// Content-Type is always application/json (never application/problem+json).
type fastAPIError struct {
	status  int
	msg     string
	details []*huma.ErrorDetail
}

func newFastAPIError(status int, msg string, errs ...error) huma.StatusError {
	e := &fastAPIError{status: status, msg: msg}
	for _, err := range errs {
		if d, ok := err.(*huma.ErrorDetail); ok {
			e.details = append(e.details, d)
		}
	}
	return e
}

func (e *fastAPIError) Error() string  { return e.msg }
func (e *fastAPIError) GetStatus() int { return e.status }

func (e *fastAPIError) MarshalJSON() ([]byte, error) {
	if e.status == http.StatusUnprocessableEntity && len(e.details) > 0 {
		type detailEntry struct {
			Loc  []interface{} `json:"loc"`
			Msg  string        `json:"msg"`
			Type string        `json:"type"`
		}
		type arrayBody struct {
			Detail []detailEntry `json:"detail"`
		}
		b := arrayBody{}
		for _, d := range e.details {
			b.Detail = append(b.Detail, detailEntry{
				Loc:  humaLocationToLoc(d.Location),
				Msg:  d.Message,
				Type: "value_error",
			})
		}
		return json.Marshal(b)
	}
	type stringBody struct {
		Detail string `json:"detail"`
	}
	return json.Marshal(stringBody{Detail: e.msg})
}

// ContentType ensures application/json is used, never application/problem+json.
func (e *fastAPIError) ContentType(_ string) string {
	return "application/json"
}

// humaLocationToLoc converts a huma error Location string (e.g. "query.service_type",
// "body.file", "path.id") into a FastAPI-style loc array (e.g. ["query", "service_type"]).
func humaLocationToLoc(location string) []interface{} {
	if location == "" {
		return []interface{}{"body"}
	}
	parts := strings.Split(location, ".")
	result := make([]interface{}, len(parts))
	for i, p := range parts {
		result[i] = p
	}
	return result
}
