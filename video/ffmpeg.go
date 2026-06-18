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
// first video frame encoded as PNG. -an drops audio; -frames:v 1 makes ffmpeg
// exit after the first frame. Output goes to stdout (pipe:1).
func runFFmpegFirstFrame(ctx context.Context, inputPath string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, FFmpegPath,
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "1",
		"-i", inputPath,
		"-an",
		"-frames:v", "1",
		"-f", "image2pipe", "-c:v", "png",
		"pipe:1",
	)
	cmd.WaitDelay = Timeout + 2*time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%w: %v", ErrExtractTimeout, err)
		}
		return nil, fmt.Errorf("%w: %v: %s", ErrExtractFailed, err, stderr.String())
	}
	// ffmpeg can exit 0 yet produce nothing (e.g. moov-at-end over a pipe, or a
	// stream with no decodable frame). Treat empty/invalid output as failure.
	out := stdout.Bytes()
	if len(out) < 8 || string(out[1:4]) != "PNG" {
		return nil, fmt.Errorf("%w: ffmpeg produced no frame: %s", ErrExtractFailed, stderr.String())
	}
	return out, nil
}
