// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo

package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
)

func TestImageThumbnail_QualityOrdersOutputSize(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	sizes := map[string]int{}
	for _, q := range []string{"lowest", "low", "medium", "high"} {
		out, err := ImageThumbnail(buf.Bytes(), 300, 300, "jpeg", q, "rectangular", "center")
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		sizes[q] = len(out)
	}
	if !(sizes["lowest"] < sizes["low"] && sizes["low"] < sizes["medium"] && sizes["medium"] < sizes["high"]) {
		t.Fatalf("sizes not strictly increasing: %v", sizes)
	}
}
