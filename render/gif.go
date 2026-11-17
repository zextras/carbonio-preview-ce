// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"encoding/binary"
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
	scale := "scale=-1:'min(" + long + ",ih)':flags=lanczos"
	if info.Width >= info.Height {
		scale = "scale='min(" + long + ",iw)':-1:flags=lanczos"
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
	if src, err := os.ReadFile(inputPath); err == nil {
		if delays, ok := gifDelays(src); ok {
			total := 0
			for _, d := range delays {
				total += d
			}
			data = patchLastDelay(data, total)
		}
	}
	return data, nil
}

// gifBlocks walks the GIF block structure without decoding pixels and returns the offset of every Graphic Control Extension delay; ok is false unless the stream ends with its trailer.
func gifBlocks(data []byte) (offsets []int, ok bool) {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return nil, false
	}
	pos := 13
	if data[10]&0x80 != 0 {
		pos += 3 << (data[10]&7 + 1)
	}
	skip := func(p int) (int, bool) {
		for {
			if p >= len(data) {
				return 0, false
			}
			n := int(data[p])
			p++
			if n == 0 {
				return p, true
			}
			p += n
		}
	}
	for pos < len(data) {
		switch data[pos] {
		case 0x3B:
			return offsets, true
		case 0x21:
			if pos+2 >= len(data) {
				return nil, false
			}
			if data[pos+1] == 0xF9 {
				if pos+6 > len(data) || data[pos+2] != 4 {
					return nil, false
				}
				offsets = append(offsets, pos+4)
			}
			var good bool
			if pos, good = skip(pos + 2); !good {
				return nil, false
			}
		case 0x2C:
			if pos+10 > len(data) {
				return nil, false
			}
			next := pos + 10
			if data[pos+9]&0x80 != 0 {
				next += 3 << (data[pos+9]&7 + 1)
			}
			var good bool
			if pos, good = skip(next + 1); !good {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return nil, false
}

func gifDelays(data []byte) ([]int, bool) {
	offsets, ok := gifBlocks(data)
	if !ok {
		return nil, false
	}
	delays := make([]int, len(offsets))
	for i, o := range offsets {
		delays[i] = int(binary.LittleEndian.Uint16(data[o:]))
	}
	return delays, true
}

// patchLastDelay sets the last frame delay of out so its total duration is total centiseconds; unparsable input is returned untouched.
func patchLastDelay(out []byte, total int) []byte {
	offsets, ok := gifBlocks(out)
	if !ok || len(offsets) == 0 {
		return out
	}
	others := 0
	for _, o := range offsets[:len(offsets)-1] {
		others += int(binary.LittleEndian.Uint16(out[o:]))
	}
	binary.LittleEndian.PutUint16(out[offsets[len(offsets)-1]:], uint16(min(max(2, total-others), 65535)))
	return out
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
