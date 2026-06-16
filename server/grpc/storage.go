// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"errors"
	"net/http"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// isNotFound reports whether err is storage.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// storageErr translates storage errors to gRPC status errors.
func storageErr(err error) error {
	if isNotFound(err) {
		return toStatus(http.StatusNotFound, config.Msg.ItemNotFound)
	}
	return toStatus(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}
