// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import "errors"

// ErrNotFound is returned when storage responds with HTTP 404,
// meaning the requested file/version does not exist.
var ErrNotFound = errors.New("storage: item not found")

// ErrUnavailable is returned when storage is unreachable (connection
// timeout, DNS failure, etc.) or returns any non-404 error response.
var ErrUnavailable = errors.New("storage: unavailable or request error")
