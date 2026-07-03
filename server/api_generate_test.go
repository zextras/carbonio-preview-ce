// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// tinyPNG returns a minimal valid PNG so the JPEG re-encode step has real input.
// Shared by the video worker / api / codec tests.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}
