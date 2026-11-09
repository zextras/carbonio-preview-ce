// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// runFFmpegFirstFrame runs ffmpeg on a seekable input file and returns the
// first video frame encoded as a full-range JPEG. -an drops audio; -frames:v 1
// makes ffmpeg exit after the first frame. Output goes to stdout (pipe:1).
//
// ctx is the only time budget: the caller bounds the local extraction and
// exec.CommandContext kills ffmpeg when it is cancelled. WaitDelay is a short
// OS-level kill-grace period only (not a duration cap); it ensures the process
// is actually reaped even if it ignores SIGKILL, which is extremely rare.
func runFFmpegFirstFrame(ctx context.Context, inputPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, FFmpegPath,
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "1",
		"-i", inputPath,
		"-an",
		"-frames:v", "1",
		"-f", "image2pipe", "-c:v", "mjpeg", "-q:v", "2", "-pix_fmt", "yuvj420p",
		"pipe:1",
	)
	// WaitDelay: a small grace period after ctx fires before we force-kill.
	// This is NOT a duration cap — it only matters when the process refuses
	// to die after SIGKILL, which is essentially impossible in practice.
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%w: %v", ErrExtractTimeout, err)
		}
		if ctx.Err() == context.Canceled {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v: %s", ErrExtractFailed, err, stderr.String())
	}
	// ffmpeg can exit 0 yet produce nothing (e.g. moov-at-end over a pipe, or a
	// stream with no decodable frame). Treat empty/invalid output as failure.
	out := stdout.Bytes()
	if len(out) < 4 || out[0] != 0xFF || out[1] != 0xD8 {
		return nil, fmt.Errorf("%w: ffmpeg produced no frame: %s", ErrExtractFailed, stderr.String())
	}
	return out, nil
}
