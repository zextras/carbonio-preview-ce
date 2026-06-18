// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
)

var (
	// ErrTooLarge means the input exceeded MaxBytes before a frame could be extracted.
	ErrTooLarge = errors.New("video: input exceeds maximum size")
	// ErrExtractFailed means ffmpeg could not extract a frame (unsupported/corrupt/no frame).
	ErrExtractFailed = errors.New("video: first-frame extraction failed")
	// ErrExtractTimeout means ffmpeg exceeded Timeout.
	ErrExtractTimeout = errors.New("video: extraction timed out")
)

// semOnce guards lazy initialisation of sem. Tests may reset both to override
// MaxConcurrent before the first call.
var (
	semOnce sync.Once
	sem     chan struct{}
)

// acquireSem blocks until a semaphore slot is available or ctx is cancelled.
// It returns an error only when the context is done before a slot is obtained.
func acquireSem(ctx context.Context) error {
	semOnce.Do(func() { sem = make(chan struct{}, MaxConcurrent) })
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSem() { <-sem }

// ExtractFirstFramePNG streams r to a seekable temp file (capped at maxBytes),
// extracts frame 0 with ffmpeg, and returns the PNG-encoded frame.
//
// A seekable temp file (not a pipe) is mandatory: moov-at-end MP4 requires
// backward seeks ffmpeg cannot do over a pipe.
//
// The semaphore slot (capacity video.MaxConcurrent) is acquired BEFORE
// creating the temp file, so it bounds the entire download+extract operation,
// not just the ffmpeg subprocess. This prevents a burst of requests from each
// writing up to MaxBytes to their own temp file simultaneously. Excess requests
// block here on the context-aware acquireSem and are unblocked immediately on
// context cancellation.
//
// ctx is the end-to-end budget for the whole operation: it is propagated to
// both the streaming body read (via the caller's io.Reader) and the ffmpeg
// subprocess. video.Timeout is an additional inner cap applied to the ffmpeg
// run only (see runFFmpegFirstFrame).
func ExtractFirstFramePNG(ctx context.Context, r io.Reader, maxBytes int64) ([]byte, error) {
	// Acquire the concurrency slot BEFORE touching disk so the semaphore bounds
	// the full download+extract operation (not just ffmpeg).
	if err := acquireSem(ctx); err != nil {
		return nil, err
	}
	defer releaseSem()

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
