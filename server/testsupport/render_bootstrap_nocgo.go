// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !cgo

package testsupport

import "testing"

// InitRealRender skips: real rendering needs cgo (libvips and libpdfium).
func InitRealRender(t *testing.T) {
	t.Helper()
	t.Skip("real rendering needs cgo (libvips, libpdfium)")
}
