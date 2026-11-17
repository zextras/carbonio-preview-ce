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
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/video"
)

var probeFunc = render.Probe

var imageThumbFunc = render.ImageThumbnail

var pdfThumbFunc = render.PDFPage1

var animatedGIFFunc = render.AnimatedGIF

var fullImageFunc = render.FullImage

// videoDetectCodecFromFileFunc is the seam for video.DetectCodecFromFile.
// Tests override it to avoid spawning ffprobe/ffmpeg.
var videoDetectCodecFromFileFunc = func(ctx context.Context, inputPath string) (string, error) {
	return video.DetectCodecFromFile(ctx, inputPath)
}

// videoFirstFrameFromFileFunc is the seam for video.ExtractFirstFrameJPEGFromFile.
// Tests override it so probe and extract can be stubbed independently.
var videoFirstFrameFromFileFunc = func(ctx context.Context, inputPath string) ([]byte, error) {
	return video.ExtractFirstFrameJPEGFromFile(ctx, inputPath)
}

// collaboraConvertFunc is the seam for render.CollaboraConvert.
var collaboraConvertFunc = func(
	ctx context.Context,
	data []byte,
	langTag string,
	docsEditorURL string,
	idle time.Duration,
) ([]byte, error) {
	return render.CollaboraConvert(ctx, data, langTag, docsEditorURL, idle)
}
