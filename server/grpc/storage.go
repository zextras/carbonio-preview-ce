// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"errors"

	"github.com/zextras/carbonio-preview-ce/storage"
)

// isNotFound reports whether err is storage.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}
