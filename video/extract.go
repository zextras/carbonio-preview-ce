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
	// ErrTooLarge means the input exceeded MaxBytes before a frame could be extracted.
	ErrTooLarge = errors.New("video: input exceeds maximum size")
	// ErrExtractFailed means ffmpeg could not extract a frame (unsupported/corrupt/no frame).
	ErrExtractFailed = errors.New("video: first-frame extraction failed")
	// ErrExtractTimeout means ffmpeg exceeded Timeout.
	ErrExtractTimeout = errors.New("video: extraction timed out")
)

// ExtractFirstFramePNG streams r to a seekable temp file (capped at maxBytes),
// extracts frame 0 with ffmpeg, and returns the PNG-encoded frame.
//
// A seekable temp file (not a pipe) is mandatory: moov-at-end MP4 requires
// backward seeks ffmpeg cannot do over a pipe.
func ExtractFirstFramePNG(ctx context.Context, r io.Reader, maxBytes int64) ([]byte, error) {
	tmp, err := os.CreateTemp("", "carbonio-preview-video-*.bin")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	// Copy at most maxBytes+1 so we can detect overflow.
	n, err := io.CopyN(tmp, r, maxBytes+1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n > maxBytes {
		return nil, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}

	return runFFmpegFirstFrame(ctx, tmp.Name())
}
