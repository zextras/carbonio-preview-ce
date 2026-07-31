// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ffprobeVideoCodecRe matches the codec name from ffprobe default output,
// e.g. "h264\n" or "vp9\n".
var ffprobeVideoCodecRe = regexp.MustCompile(`(?m)^\s*([a-z0-9_]+)\s*$`)

// ffmpegStreamRe parses the video codec from ffmpeg -i stderr, e.g.:
//
//	Stream #0:0(und): Video: h264 (High) ...
var ffmpegStreamRe = regexp.MustCompile(`Stream #\S+: Video: ([a-zA-Z0-9_]+)`)

// ffprobePath derives the ffprobe sibling of FFmpegPath (same directory).
// It is not a package-level var intentionally — FFmpegPath may be reassigned
// by tests, so we compute it at call time.
func ffprobePath() string {
	return filepath.Join(filepath.Dir(FFmpegPath), "ffprobe")
}

// DetectCodecFromFile detects the primary video codec of the file at inputPath
// by probing without decoding any frames.
//
// Strategy: try ffprobe first (cleaner output, purpose-built), then fall back
// to parsing ffmpeg -i stderr. Both tools ship together in carbonio-ffmpeg, so
// if ffmpeg is present, at least one of the two will work.
//
// Returns the codec name in lowercase (e.g. "h264", "hevc", "vp9").
// Returns an error if neither tool can identify a video stream.
func DetectCodecFromFile(ctx context.Context, inputPath string) (string, error) {
	// Attempt 1: ffprobe (preferred).
	ffprobe := ffprobePath()
	if _, err := os.Stat(ffprobe); err == nil {
		codec, err := probeWithFFprobe(ctx, ffprobe, inputPath)
		if err == nil && codec != "" {
			return strings.ToLower(codec), nil
		}
	}

	// Attempt 2: ffmpeg -i (stderr parse).
	codec, err := probeWithFFmpegI(ctx, FFmpegPath, inputPath)
	if err != nil {
		return "", fmt.Errorf("video: codec detection failed: %w", err)
	}
	return strings.ToLower(codec), nil
}

// DetectCodec downloads the video stream r to a temp file and calls DetectCodecFromFile.
// The temp file is shared with ExtractFirstFramePNG if you pass the same reader
// — but since both consume the reader, callers that need BOTH operations should
// download once to a temp file and call DetectCodecFromFile + runFFmpegFirstFrame
// directly. See video_worker probe-first flow for the actual usage.
//
// This function exists as a convenience for callers that have a reader but not yet
// a file path.
func DetectCodec(ctx context.Context, r io.Reader) (string, error) {
	tmp, err := os.CreateTemp("", "carbonio-preview-probe-*.bin")
	if err != nil {
		return "", fmt.Errorf("video: DetectCodec create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, r); err != nil {
		return "", fmt.Errorf("video: DetectCodec copy: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("video: DetectCodec sync: %w", err)
	}

	return DetectCodecFromFile(ctx, tmp.Name())
}

// ExtractFirstFramePNGFromFile is the seekable-file variant of ExtractFirstFramePNG,
// used internally when both probe and extract share a pre-downloaded temp file.
func ExtractFirstFramePNGFromFile(ctx context.Context, inputPath string) ([]byte, error) {
	return runFFmpegFirstFrame(ctx, inputPath)
}

func probeWithFFprobe(ctx context.Context, ffprobe, inputPath string) (string, error) {
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=nk=1:nw=1",
		inputPath,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffprobe: %w: %s", err, stderr.String())
	}
	m := ffprobeVideoCodecRe.FindStringSubmatch(stdout.String())
	if m == nil {
		return "", fmt.Errorf("ffprobe: no video codec in output: %q", stdout.String())
	}
	return m[1], nil
}

func probeWithFFmpegI(ctx context.Context, ffmpegBin, inputPath string) (string, error) {
	// ffmpeg -i always exits non-zero when given no output; we capture stderr.
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-i", inputPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // non-zero exit is expected

	m := ffmpegStreamRe.FindStringSubmatch(stderr.String())
	if m == nil {
		return "", fmt.Errorf("ffmpeg -i: no video stream found in: %q", stderr.String())
	}
	return m[1], nil
}
