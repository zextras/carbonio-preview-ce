// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// tinyJPEG returns a minimal valid JPEG standing in for the extracted frame.
// Shared by the video worker / api / codec tests.
func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}
