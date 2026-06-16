// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// toStatus converts an HTTP status code (as used by the REST handlers) into a
// gRPC status error. The mapping is defined in proto/preview.proto:
//
//	404 Not Found            → NOT_FOUND
//	400 Bad Request          → INVALID_ARGUMENT
//	422 Unprocessable Entity → FAILED_PRECONDITION
//	500 Internal Server Error→ INTERNAL
//
// Any HTTP status not in the table falls back to INTERNAL.
func toStatus(httpStatus int, msg string) error {
	var code codes.Code
	switch httpStatus {
	case http.StatusNotFound:
		code = codes.NotFound
	case http.StatusBadRequest:
		code = codes.InvalidArgument
	case http.StatusUnprocessableEntity:
		code = codes.FailedPrecondition
	case http.StatusInternalServerError:
		code = codes.Internal
	default:
		code = codes.Internal
	}
	return status.Error(code, msg)
}
