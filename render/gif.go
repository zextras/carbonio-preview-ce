// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/video"
)

var ErrGIFFailed = errors.New("animated gif resize failed")

const gifMaxFPS = 10

func AnimatedGIF(ctx context.Context, inputPath string, info Info, side int, workDir string) ([]byte, error) {
	long := strconv.Itoa(side)
	scale := "scale=-2:'min(" + long + ",ih)':flags=lanczos"
	if info.Width >= info.Height {
		scale = "scale='min(" + long + ",iw)':-2:flags=lanczos"
	}
	chain := "fps=" + strconv.Itoa(gifMaxFPS) + "," + scale + ",mpdecimate=hi=1:lo=1:frac=1:max=0"
	palette := filepath.Join(workDir, "palette.png")
	out := filepath.Join(workDir, "out.gif")

	if err := runFFmpegGIF(ctx,
		"-filter_threads", "1", "-i", inputPath,
		"-vf", chain+",palettegen=stats_mode=diff",
		"-y", palette,
	); err != nil {
		return nil, err
	}
	if err := runFFmpegGIF(ctx,
		"-filter_complex_threads", "1", "-i", inputPath, "-i", palette,
		"-lavfi", "[0:v]"+chain+"[x];[x][1:v]paletteuse=dither=none",
		"-fps_mode", "vfr", "-f", "gif", "-y", out,
	); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGIFFailed, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: ffmpeg produced no output", ErrGIFFailed)
	}
	return data, nil
}

func runFFmpegGIF(ctx context.Context, args ...string) error {
	base := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1"}
	cmd := exec.CommandContext(ctx, video.FFmpegPath, append(base, args...)...)
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v: %s", ErrGIFFailed, err, stderr.String())
	}
	return nil
}
