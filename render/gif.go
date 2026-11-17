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

// gifSlotSelect keeps at most one frame per 100 ms slot and snaps it to the slot: frames are only dropped, never duplicated, so the cost follows the source frames.
const gifSlotSelect = `select='isnan(prev_selected_t)+gt(floor(t*10)\,floor(prev_selected_t*10))',setpts='round(floor(T*10)/10/TB)'`

func AnimatedGIF(ctx context.Context, inputPath string, info Info, side int, workDir string) ([]byte, error) {
	long := strconv.Itoa(side)
	scale := "scale=-1:'min(" + long + ",ih)':flags=lanczos"
	if info.Width >= info.Height {
		scale = "scale='min(" + long + ",iw)':-1:flags=lanczos"
	}
	chain := gifSlotSelect + "," + scale + ",mpdecimate=hi=1:lo=1:frac=1:max=0"
	palette := filepath.Join(workDir, "palette.png")
	out := filepath.Join(workDir, "out.gif")

	input, total, normalized := inputPath, 0, false
	if src, err := os.ReadFile(inputPath); err == nil {
		if norm, t, ok := normalizeGIFDelays(src); ok {
			path := filepath.Join(workDir, "normalized.gif")
			if os.WriteFile(path, norm, 0o600) == nil {
				input, total, normalized = path, t, true
			}
		}
	}

	if err := runFFmpegGIF(ctx,
		"-filter_threads", "1", "-i", input,
		"-vf", chain+",palettegen=stats_mode=diff",
		"-y", palette,
	); err != nil {
		return nil, err
	}
	if err := runFFmpegGIF(ctx,
		"-filter_complex_threads", "1", "-i", input, "-i", palette,
		"-lavfi", "[0:v]"+chain+"[x];[x][1:v]paletteuse=dither=none",
		"-fps_mode", "vfr", "-enc_time_base", "1/100", "-f", "gif", "-y", out,
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
	if normalized {
		data = patchLastDelay(data, total)
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

type gifFrame struct {
	image int
	delay int
}

// gifBlocks walks the GIF block structure without decoding pixels. For every image it returns the offset of its descriptor and of the delay of its Graphic Control Extension (-1 when it has none); ok is false unless the stream ends with its trailer.
func gifBlocks(data []byte) (frames []gifFrame, ok bool) {
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
	pending := -1
	for pos < len(data) {
		switch data[pos] {
		case 0x00:
			pos++
		case 0x3B:
			return frames, true
		case 0x21:
			if pos+2 >= len(data) {
				return nil, false
			}
			if data[pos+1] == 0xF9 {
				if pos+8 > len(data) || data[pos+2] != 4 {
					return nil, false
				}
				pending = pos + 4
			}
			var good bool
			if pos, good = skip(pos + 2); !good {
				return nil, false
			}
		case 0x2C:
			if pos+10 > len(data) {
				return nil, false
			}
			frames = append(frames, gifFrame{image: pos, delay: pending})
			pending = -1
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

// gifDelays returns the raw delay in centiseconds of every frame, 0 for a frame without a Graphic Control Extension.
func gifDelays(data []byte) ([]int, bool) {
	frames, ok := gifBlocks(data)
	if !ok {
		return nil, false
	}
	delays := make([]int, len(frames))
	for i, f := range frames {
		if f.delay >= 0 {
			delays[i] = int(binary.LittleEndian.Uint16(data[f.delay:]))
		}
	}
	return delays, true
}

// normalizeGIFDelays returns a copy of data where every delay of 0 or 1 cs, as a frame without a Graphic Control Extension, is 10 cs as browsers play it, and the total duration of that copy.
func normalizeGIFDelays(data []byte) ([]byte, int, bool) {
	frames, ok := gifBlocks(data)
	if !ok {
		return nil, 0, false
	}
	out := make([]byte, 0, len(data)+8*len(frames))
	last, total := 0, 0
	for _, f := range frames {
		switch {
		case f.delay < 0:
			out = append(out, data[last:f.image]...)
			out = append(out, 0x21, 0xF9, 0x04, 0x00, 10, 0x00, 0x00, 0x00)
			last = f.image
			total += 10
		default:
			d := int(binary.LittleEndian.Uint16(data[f.delay:]))
			if d < 2 {
				out = append(out, data[last:f.delay]...)
				out = append(out, 10, 0x00)
				last = f.delay + 2
				d = 10
			}
			total += d
		}
	}
	return append(out, data[last:]...), total, true
}

// patchLastDelay sets the last frame delay of out so its total duration is total centiseconds; unparsable input is returned untouched.
func patchLastDelay(out []byte, total int) []byte {
	frames, ok := gifBlocks(out)
	if !ok || len(frames) == 0 || frames[len(frames)-1].delay < 0 {
		return out
	}
	others := 0
	for _, f := range frames[:len(frames)-1] {
		if f.delay >= 0 {
			others += int(binary.LittleEndian.Uint16(out[f.delay:]))
		}
	}
	binary.LittleEndian.PutUint16(out[frames[len(frames)-1].delay:], uint16(min(max(2, total-others), 65535)))
	return out
}
