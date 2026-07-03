// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

// TestOutputFormatContract verifies that ImageThumbnail honours the requested
// output format on all code paths, matching Python's jpeg_manipulation /
// png_manipulation contract:
//
//   - PNG input + output_format=jpeg → real JPEG bytes (JFIF magic), no alpha issue
//   - PNG input (RGBA) + output_format=jpeg → real JPEG bytes (RGBA flattened to RGB)
//   - PNG input + output_format=png  → PNG bytes
//   - PNG input + output_format=jpeg + crop=none (scale_fit_pad path) → JPEG bytes
//   - PNG input + output_format=png  + crop=none (scale_fit_pad path) → PNG bytes
//
// These tests exercise the real libvips pipeline (CGO) so they require
// libvips to be installed (pkg-config: vips).  They are skipped automatically
// in environments without libvips because the build will not compile.
//
// The test binary MUST call InitVips before running these tests; we do so in
// TestMain below.

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg" // register the JPEG decoder for image.Decode (formerly transitive via single_threaded)
	"image/png"
	"testing"
)

// jpegMagic is the first 3 bytes of every JPEG/JFIF file.
var jpegMagic = []byte{0xFF, 0xD8, 0xFF}

// pngMagic is the first 8 bytes of every PNG file.
var pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// makePNG encodes a tiny w×h RGB PNG and returns its bytes.
func makePNG(t *testing.T, w, h int, opaque bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if opaque {
				img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
			} else {
				// RGBA with partial transparency to stress the alpha-flatten path.
				img.Set(x, y, color.RGBA{R: 100, G: 150, B: 200, A: 128})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// isJPEG returns true when data starts with the JPEG SOI marker.
func isJPEG(data []byte) bool {
	return len(data) >= 3 && bytes.Equal(data[:3], jpegMagic)
}

// isPNG returns true when data starts with the PNG signature.
func isPNG(data []byte) bool {
	return len(data) >= 8 && bytes.Equal(data[:8], pngMagic)
}

// TestMain initialises libvips once for the whole test binary.
func TestMain(m *testing.M) {
	if err := InitVips("carbonio-preview-test"); err != nil {
		panic("InitVips failed: " + err.Error())
	}
	m.Run()
}

// TestOutputFormat_CoverCrop_PNGInputJPEGOutput verifies the cover-crop path
// (thumbnail / preview with crop=true) with a PNG input and jpeg output.
// This exercises coverCropVips → cover_crop C function with fmt=1.
func TestOutputFormat_CoverCrop_PNGInputJPEGOutput(t *testing.T) {
	data := makePNG(t, 200, 200, true)

	out, err := ImageThumbnail(nil, data, 100, 100, "jpeg", "medium", "rectangular", "center")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	if !isJPEG(out) {
		t.Errorf("expected JPEG bytes (0xFF 0xD8 0xFF), got %X (first 4 bytes)", out[:4])
	}
}

// TestOutputFormat_CoverCrop_RGBAPNGInputJPEGOutput verifies that a PNG with
// alpha channel (RGBA) is correctly flattened and JPEG-encoded on the
// cover-crop path (the bug: PNG with alpha was not encoded as JPEG).
func TestOutputFormat_CoverCrop_RGBAPNGInputJPEGOutput(t *testing.T) {
	data := makePNG(t, 200, 200, false) // partial alpha

	out, err := ImageThumbnail(nil, data, 100, 100, "jpeg", "medium", "rectangular", "center")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	if !isJPEG(out) {
		t.Errorf("expected JPEG bytes, got %X", out[:4])
	}
	// JPEG must not have an alpha channel — the flatten step must have occurred.
	// (We verify the output is decodable and the image format library agrees.)
	_, imgFmt, decErr := image.Decode(bytes.NewReader(out))
	if decErr != nil {
		t.Fatalf("decode output: %v", decErr)
	}
	if imgFmt != "jpeg" {
		t.Errorf("image.Decode format: got %q, want jpeg", imgFmt)
	}
}

// TestOutputFormat_CoverCrop_PNGInputPNGOutput verifies PNG→PNG on cover-crop.
func TestOutputFormat_CoverCrop_PNGInputPNGOutput(t *testing.T) {
	data := makePNG(t, 200, 200, true)

	out, err := ImageThumbnail(nil, data, 100, 100, "png", "medium", "rectangular", "center")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	if !isPNG(out) {
		t.Errorf("expected PNG bytes, got %X", out[:8])
	}
}

// TestOutputFormat_ScaleFitPad_PNGInputJPEGOutput is the PRIMARY regression test
// for the production bug: POST /preview/image/100x100/ with a PNG, no explicit
// output_format (defaults to jpeg), no crop (defaults to false → scale_fit_pad
// path). Pre-fix this returned PNG bytes with Content-Type: image/jpeg.
func TestOutputFormat_ScaleFitPad_PNGInputJPEGOutput(t *testing.T) {
	data := makePNG(t, 1, 1, false) // 1×1 RGBA, the exact production scenario

	out, err := ImageThumbnail(nil, data, 100, 100, "jpeg", "medium", "rectangular", "none")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	if !isJPEG(out) {
		magic := out
		if len(magic) > 8 {
			magic = magic[:8]
		}
		t.Errorf("BUG: expected JPEG bytes (0xFF 0xD8 0xFF), got %X — PNG returned with jpeg output_format", magic)
	}
	// Also verify it decodes correctly as JPEG.
	_, imgFmt, decErr := image.Decode(bytes.NewReader(out))
	if decErr != nil {
		t.Fatalf("decode output: %v", decErr)
	}
	if imgFmt != "jpeg" {
		t.Errorf("image.Decode format: got %q, want jpeg", imgFmt)
	}
}

// TestOutputFormat_ScaleFitPad_RGBAPNGInputJPEGOutput verifies that RGBA PNG
// on the scale_fit_pad path is also correctly JPEG-encoded (alpha flattened).
func TestOutputFormat_ScaleFitPad_RGBAPNGInputJPEGOutput(t *testing.T) {
	data := makePNG(t, 50, 50, false) // partial alpha

	out, err := ImageThumbnail(nil, data, 100, 100, "jpeg", "medium", "rectangular", "none")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	if !isJPEG(out) {
		magic := out
		if len(magic) > 8 {
			magic = magic[:8]
		}
		t.Errorf("expected JPEG bytes, got %X", magic)
	}
}

// TestOutputFormat_ScaleFitPad_PNGInputPNGOutput verifies PNG→PNG on scale_fit_pad.
func TestOutputFormat_ScaleFitPad_PNGInputPNGOutput(t *testing.T) {
	data := makePNG(t, 200, 200, true)

	out, err := ImageThumbnail(nil, data, 100, 100, "png", "medium", "rectangular", "none")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	if !isPNG(out) {
		t.Errorf("expected PNG bytes, got %X", out[:8])
	}
}

// TestOutputFormat_ScaleFitPad_PNGInputGIFOutput verifies that gif output
// on the scale_fit_pad path produces PNG bytes (GIF encode is not supported;
// this matches the existing behaviour for the cover-crop path too).
func TestOutputFormat_ScaleFitPad_PNGInputGIFOutput(t *testing.T) {
	data := makePNG(t, 200, 200, true)

	out, err := ImageThumbnail(nil, data, 100, 100, "gif", "medium", "rectangular", "none")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	// GIF falls through to PNG path (fmtStr != "jpeg") — this is known / documented.
	if !isPNG(out) {
		t.Errorf("expected PNG bytes for gif output (known limitation), got %X", out[:8])
	}
}

// TestOutputFormat_Dimensions_1x1To100x100 verifies that the sizing semantics
// for a 1×1 input with 100×100 requested area (no crop) produce 80×80 output,
// matching Python's _convert_requested_size_to_true_res_to_scale rule-3 clamp
// (original=1 < minRes=80, requested/2=50 > original=1 → clamp to minRes=80).
// This confirms the 80×80 production observation is NOT a Go divergence.
func TestOutputFormat_Dimensions_1x1To100x100(t *testing.T) {
	data := makePNG(t, 1, 1, false)

	out, err := ImageThumbnail(nil, data, 100, 100, "png", "medium", "rectangular", "none")
	if err != nil {
		t.Fatalf("ImageThumbnail: %v", err)
	}
	img, _, decErr := image.Decode(bytes.NewReader(out))
	if decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	b := img.Bounds()
	gotW, gotH := b.Dx(), b.Dy()
	// Python: origW=1 < minRes=80, req/2=50 > origW=1 → clamp both to 80.
	// Go ConvertRequestedSize applies identical rule-3 logic.
	if gotW != 80 || gotH != 80 {
		t.Errorf("dimensions: got %dx%d, want 80x80 (rule-3 clamp matching Python)", gotW, gotH)
	}
}
