// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"context"
	"errors"
	"io"
	"os"
)

var (
	// ErrExtractFailed means ffmpeg could not extract a frame (unsupported/corrupt/no frame).
	ErrExtractFailed = errors.New("video: first-frame extraction failed")
	// ErrExtractTimeout means ffmpeg exceeded Timeout.
	ErrExtractTimeout = errors.New("video: extraction timed out")
)

// ExtractFirstFramePNG streams r to a seekable temp file, extracts frame 0
// with ffmpeg, and returns the PNG-encoded frame. No size cap is applied —
// preview is a pure transform service; size policy belongs to the caller (WSC).
//
// A seekable temp file (not a pipe) is mandatory: moov-at-end MP4 requires
// backward seeks ffmpeg cannot do over a pipe.
//
// Concurrency is NOT bounded here. The sole caller is the generate handler,
// which bounds concurrency at exactly one point — the dedicated video-semaphore
// middleware (capacity = video-concurrency, default runtime.NumCPU()). Bounding
// again inside this function would double-bound the same work.
//
// Single-clock design: ctx is the SOLE time budget for the entire operation.
// It is the per-request context.WithTimeout set by the generate handler
// (ServiceTimeoutInSeconds). It is propagated to both the streaming body read
// (via r, which is backed by an http.Response.Body bound to ctx) and the
// ffmpeg subprocess (via exec.CommandContext). There is no inner timeout.
func ExtractFirstFramePNG(ctx context.Context, r io.Reader) ([]byte, error) {
	tmp, err := os.CreateTemp("", "carbonio-preview-video-*.bin")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	// Stream the full body — no size cap; the caller owns size policy.
	if _, err := io.Copy(tmp, r); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}

	return runFFmpegFirstFrame(ctx, tmp.Name())
}
