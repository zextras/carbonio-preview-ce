// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// This file provides injectable function variables that wrap the render
// package calls.  Production code uses the real implementations; tests
// override them with stubs so that the CGO render layer (libvips / PDFium)
// is never exercised during unit tests.
//
// All vars are package-level (not per-request) because the handlers are
// also package-level closures — a per-request override would require a
// context value or a handler struct, both of which are larger changes.
// Resetting to defaults in test teardown is enough for isolated tests.

import (
	"context"
	"io"
	"time"

	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/video"
)

// imageThumbnailFunc is the seam for render.ImageThumbnail.
var imageThumbnailFunc = func(
	sem chan struct{},
	data []byte,
	width, height int,
	outputFormat, quality, shape, cropMode string,
) ([]byte, error) {
	return render.ImageThumbnail(sem, data, width, height, outputFormat, quality, shape, cropMode)
}

// videoFirstFrameFunc is the seam for video.ExtractFirstFramePNG. Tests override
// it to avoid spawning ffmpeg; production uses the real extractor.
var videoFirstFrameFunc = func(ctx context.Context, r io.Reader) ([]byte, error) {
	return video.ExtractFirstFramePNG(ctx, r)
}

// videoDetectCodecFromFileFunc is the seam for video.DetectCodecFromFile.
// Tests override it to avoid spawning ffprobe/ffmpeg.
var videoDetectCodecFromFileFunc = func(ctx context.Context, inputPath string) (string, error) {
	return video.DetectCodecFromFile(ctx, inputPath)
}

// videoFirstFrameFromFileFunc is the seam for video.ExtractFirstFramePNGFromFile.
// Tests override it so probe and extract can be stubbed independently.
var videoFirstFrameFromFileFunc = func(ctx context.Context, inputPath string) ([]byte, error) {
	return video.ExtractFirstFramePNGFromFile(ctx, inputPath)
}

// pdfSliceFunc is the seam for render.PDFSlice.
var pdfSliceFunc = func(sem chan struct{}, data []byte, firstPage, lastPage int) ([]byte, error) {
	return render.PDFSlice(sem, data, firstPage, lastPage)
}

// pdfRasterizeFunc is the seam for render.PDFRasterize.
var pdfRasterizeFunc = func(
	sem chan struct{},
	data []byte,
	page, width, height int,
	outputFormat, quality, shape string,
) ([]byte, error) {
	return render.PDFRasterize(sem, data, page, width, height, outputFormat, quality, shape)
}

// collaboraConvertFunc is the seam for render.CollaboraConvert.
var collaboraConvertFunc = func(
	ctx context.Context,
	data []byte,
	langTag string,
	docsEditorURL string,
	timeout time.Duration,
) ([]byte, error) {
	return render.CollaboraConvert(ctx, data, langTag, docsEditorURL, timeout)
}
