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
	// ErrExtractTimeout means the extraction hit its context deadline.
	ErrExtractTimeout = errors.New("video: extraction timed out")
)

// ExtractFirstFrameJPEG streams r to a seekable temp file, extracts frame 0
// with ffmpeg, and returns the JPEG-encoded frame. No size cap is applied —
// preview is a pure transform service; size policy belongs to the caller (WSC).
//
// A seekable temp file (not a pipe) is mandatory: moov-at-end MP4 requires
// backward seeks ffmpeg cannot do over a pipe.
//
// Concurrency is NOT bounded here. The caller is the background video worker,
// which bounds concurrency at exactly one point — the dedicated video semaphore
// (capacity = video.max-concurrent-extractions, default runtime.NumCPU()).
// Bounding again inside this function would double-bound the same work.
//
// ctx is the only time budget for the ffmpeg subprocess (via
// exec.CommandContext); reading r is bounded by whatever r itself enforces.
func ExtractFirstFrameJPEG(ctx context.Context, r io.Reader) ([]byte, error) {
	tmp, err := os.CreateTemp("", "carbonio-preview-video-*.bin")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	// Retain only head+tail; discard the middle (frame-0 lives in the head,
	// the trailing moov/index lives in the tail). See StreamToSparseTemp.
	if _, err := StreamToSparseTemp(tmp, r); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}

	return runFFmpegFirstFrame(ctx, tmp.Name())
}
