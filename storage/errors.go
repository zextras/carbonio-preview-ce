// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrNotFound is returned when storage responds with HTTP 404,
// meaning the requested file/version does not exist.
var ErrNotFound = errors.New("storage: item not found")

// ErrUnavailable is returned when storage is unreachable (connection
// timeout, DNS failure, etc.) or returns any non-404 error response.
var ErrUnavailable = errors.New("storage: unavailable or request error")

// ErrRejected marks a definitive refusal by a reachable storage, as opposed to an outage.
var ErrRejected = errors.New("storage: request rejected")

// StatusError is the ErrUnavailable error of a non-success HTTP status; it also wraps ErrRejected for 4xx other than 408 and 429, and for 500 and 501.
func StatusError(op string, code int) error {
	rejected := code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests ||
		code == http.StatusInternalServerError || code == http.StatusNotImplemented
	if rejected {
		return fmt.Errorf("%w: %w: %s returned HTTP %d", ErrUnavailable, ErrRejected, op, code)
	}
	return fmt.Errorf("%w: %s returned HTTP %d", ErrUnavailable, op, code)
}
