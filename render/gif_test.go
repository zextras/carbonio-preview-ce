// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeIn(t *testing.T, data []byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "src.gif")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return in, dir
}

func TestAnimatedGIF_ResizesLongSideKeepsAnimation(t *testing.T) {
	src := genGIF(t, 640, 360, 30)
	in, dir := writeIn(t, src)
	info, err := Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := AnimatedGIF(context.Background(), in, info, 320, dir)
	if err != nil {
		t.Fatal(err)
	}
	oi, err := Probe(out)
	if err != nil {
		t.Fatal(err)
	}
	if oi.Width != 320 || oi.Height != 180 {
		t.Fatalf("out %dx%d, want 320x180", oi.Width, oi.Height)
	}
	if oi.Pages != info.Pages || oi.Pages <= 1 {
		t.Fatalf("pages=%d, want %d (animated)", oi.Pages, info.Pages)
	}
}

func TestAnimatedGIF_PortraitScalesHeight(t *testing.T) {
	src := genGIF(t, 360, 640, 10)
	in, dir := writeIn(t, src)
	info, err := Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := AnimatedGIF(context.Background(), in, info, 320, dir)
	if err != nil {
		t.Fatal(err)
	}
	oi, err := Probe(out)
	if err != nil {
		t.Fatal(err)
	}
	if oi.Height != 320 || oi.Width != 180 || oi.Pages <= 1 {
		t.Fatalf("out %dx%d pages=%d, want 180x320 animated", oi.Width, oi.Height, oi.Pages)
	}
}

func TestAnimatedGIF_NotAGIFIsErrGIFFailed(t *testing.T) {
	resolveFFmpeg(t)
	in, dir := writeIn(t, []byte("definitely not a gif"))
	_, err := AnimatedGIF(context.Background(), in, Info{Width: 100, Height: 100, Pages: 2}, 320, dir)
	if !errors.Is(err, ErrGIFFailed) {
		t.Fatalf("err=%v, want ErrGIFFailed", err)
	}
}

func TestAnimatedGIF_CtxCancelStopsFFmpeg(t *testing.T) {
	src := genGIF(t, 320, 180, 10)
	in, dir := writeIn(t, src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AnimatedGIF(ctx, in, Info{Width: 320, Height: 180, Pages: 10}, 320, dir)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrGIFFailed) {
		t.Fatalf("err=%v, want context.Canceled and not ErrGIFFailed", err)
	}
}

func resizedGIF(t *testing.T, src []byte, side int) (*gif.GIF, Info) {
	t.Helper()
	in, dir := writeIn(t, src)
	info, err := Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := AnimatedGIF(context.Background(), in, info, side, dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	oi, err := Probe(out)
	if err != nil {
		t.Fatal(err)
	}
	return g, oi
}

func totalDelay(g *gif.GIF) int {
	n := 0
	for _, d := range g.Delay {
		n += d
	}
	return n
}

func TestAnimatedGIF_FastSourceIsCappedAt10fpsOnA100msGrid(t *testing.T) {
	for _, rate := range []int{50, 25, 15} {
		const seconds = 8
		src := genGIFRate(t, 640, 360, rate*seconds, rate)
		srcGIF, err := gif.DecodeAll(bytes.NewReader(src))
		if err != nil {
			t.Fatal(err)
		}
		g, _ := resizedGIF(t, src, 320)
		if len(g.Image) != 10*seconds {
			t.Errorf("%d fps: %d frames, want exactly %d (10 fps)", rate, len(g.Image), 10*seconds)
		}
		for i, d := range g.Delay {
			if d != 10 {
				t.Errorf("%d fps: frame %d delay %d cs, want 10 (100 ms grid)", rate, i, d)
				break
			}
		}
		if got, want := totalDelay(g), totalDelay(srcGIF); got < want-10 || got > want+10 {
			t.Errorf("%d fps: duration %d cs, want about %d cs (unchanged)", rate, got, want)
		}
	}
}

func TestAnimatedGIF_SlowSourceIsNotInflated(t *testing.T) {
	src := genGIFRate(t, 640, 360, 6, 1)
	g, _ := resizedGIF(t, src, 320)
	if len(g.Image) != 6 {
		t.Fatalf("%d frames, want the 6 of the 1 fps slideshow", len(g.Image))
	}
	for i, d := range g.Delay[:5] {
		if d != 100 {
			t.Errorf("frame %d delay %d cs, want the source's 100", i, d)
		}
	}
	if got := totalDelay(g); got != 600 {
		t.Errorf("duration %d cs, want 600 (unchanged)", got)
	}
}

func TestAnimatedGIF_NeverUpscales(t *testing.T) {
	for name, dims := range map[string][2]int{"landscape": {200, 100}, "portrait": {100, 200}, "odd": {201, 101}, "odd portrait": {101, 201}} {
		src := genGIF(t, dims[0], dims[1], 8)
		for _, side := range []int{SideS, SideM} {
			_, oi := resizedGIF(t, src, side)
			if oi.Width != dims[0] || oi.Height != dims[1] {
				t.Errorf("%s at side %d: %dx%d, want the source size %dx%d", name, side, oi.Width, oi.Height, dims[0], dims[1])
			}
		}
	}
}

func slideshowGIF(t *testing.T, delays []int, distinct func(i int) int) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}, color.RGBA{0, 0, 255, 255}}
	g := &gif.GIF{}
	for i, d := range delays {
		f := image.NewPaletted(image.Rect(0, 0, 640, 360), pal)
		draw.Draw(f, f.Bounds(), &image.Uniform{pal[distinct(i)%len(pal)]}, image.Point{}, draw.Src)
		x := (distinct(i) * 37) % 600
		draw.Draw(f, image.Rect(x, 100, x+40, 160), &image.Uniform{pal[(distinct(i)+2)%len(pal)]}, image.Point{}, draw.Src)
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, d)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func repeat(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestAnimatedGIF_FastSourceWithALongLastPauseKeepsDurationAndPause(t *testing.T) {
	resolveFFmpeg(t)
	delays := append(repeat(100, 2), 200)
	src := slideshowGIF(t, delays, func(i int) int { return i })
	g, _ := resizedGIF(t, src, 320)
	if got := totalDelay(g); got < 400-1 || got > 400+1 {
		t.Errorf("duration %d cs, want 400 (within 1 cs)", got)
	}
	if last := g.Delay[len(g.Delay)-1]; last < 190 {
		t.Errorf("last delay %d cs, want the 2 s pause kept", last)
	}
}

func TestAnimatedGIF_TrailingExactDuplicatesKeepDuration(t *testing.T) {
	resolveFFmpeg(t)
	delays := repeat(60, 4)
	src := slideshowGIF(t, delays, func(i int) int { return min(i, 49) })
	g, _ := resizedGIF(t, src, 320)
	if got := totalDelay(g); got < 240-1 || got > 240+1 {
		t.Errorf("duration %d cs, want 240 (the dropped duplicates' time goes to the last frame)", got)
	}
}

func TestAnimatedGIF_UnparsableSourceKeepsFFmpegsOutput(t *testing.T) {
	src := genGIF(t, 320, 180, 10)
	truncated := src[:len(src)-1]
	if _, ok := gifDelays(truncated); ok {
		t.Fatal("a GIF without its trailer must not parse")
	}
	in, dir := writeIn(t, truncated)
	info, err := Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := AnimatedGIF(context.Background(), in, info, 320, dir)
	if err != nil {
		t.Fatalf("ffmpeg output must be returned untouched: %v", err)
	}
	if _, err := gif.DecodeAll(bytes.NewReader(out)); err != nil {
		t.Errorf("output is not a GIF: %v", err)
	}
	if got := patchLastDelay([]byte("garbage"), 100); string(got) != "garbage" {
		t.Errorf("garbage must be left alone, got %q", got)
	}
}

func solidFrames(n int) []*image.Paletted {
	pal := color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}, color.RGBA{0, 0, 255, 255}}
	var out []*image.Paletted
	for i := 0; i < n; i++ {
		f := image.NewPaletted(image.Rect(0, 0, 320, 180), pal)
		draw.Draw(f, f.Bounds(), &image.Uniform{pal[i%len(pal)]}, image.Point{}, draw.Src)
		x := (i * 11) % 280
		draw.Draw(f, image.Rect(x, 60, x+30, 90), &image.Uniform{pal[(i+2)%len(pal)]}, image.Point{}, draw.Src)
		out = append(out, f)
	}
	return out
}

func encodeDelays(t *testing.T, delays []int, explicitGCE bool) []byte {
	t.Helper()
	g := &gif.GIF{Image: solidFrames(len(delays)), Delay: delays}
	if explicitGCE {
		g.Disposal = bytes.Repeat([]byte{gif.DisposalNone}, len(delays))
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAnimatedGIF_ZeroDelaysPlayAsTenAndKeepTheFinalPause(t *testing.T) {
	resolveFFmpeg(t)
	delays := append(repeat(29, 0), 300)
	for _, explicit := range []bool{true, false} {
		g, _ := resizedGIF(t, encodeDelays(t, delays, explicit), 320)
		if len(g.Image) != 30 {
			t.Errorf("explicit GCE=%v: %d frames, want 30", explicit, len(g.Image))
		}
		if got := totalDelay(g); got < 590-1 || got > 590+1 {
			t.Errorf("explicit GCE=%v: duration %d cs, want 590 as a browser plays it", explicit, got)
		}
		if last := g.Delay[len(g.Delay)-1]; last < 299 {
			t.Errorf("explicit GCE=%v: last delay %d cs, want the 300 cs pause", explicit, last)
		}
	}
}

func TestAnimatedGIF_OneCsDelaysPlayAsTenAndAreNotMerged(t *testing.T) {
	resolveFFmpeg(t)
	delays := append(repeat(19, 1), 200)
	g, _ := resizedGIF(t, encodeDelays(t, delays, true), 320)
	if len(g.Image) != 20 {
		t.Errorf("%d frames, want 20 (1 cs plays as 10 cs: 10 fps, nothing to merge)", len(g.Image))
	}
	if got := totalDelay(g); got < 390-1 || got > 390+1 {
		t.Errorf("duration %d cs, want 390 as a browser plays it", got)
	}
}

func pathologicalGIF(t *testing.T, w, h, frames, delay int) []byte {
	t.Helper()
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(255 - i), uint8(i * 7), 255}
	}
	g := &gif.GIF{}
	for f := 0; f < frames; f++ {
		img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Pix[y*img.Stride+x] = uint8((x/8 + y/8 + f*50) % 256)
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, delay)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAnimatedGIF_CostDoesNotGrowWithTheDeclaredDuration(t *testing.T) {
	resolveFFmpeg(t)
	for name, src := range map[string][]byte{
		"1080p_5_frames_of_60s": pathologicalGIF(t, 1920, 1080, 5, 6000),
		"2_frames_of_655s":      pathologicalGIF(t, 640, 360, 2, 65535),
	} {
		in, dir := writeIn(t, src)
		info, err := Probe(src)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		start := time.Now()
		out, err := AnimatedGIF(ctx, in, info, 320, dir)
		took := time.Since(start)
		cancel()
		t.Logf("%s: %d KB source, %v", name, len(src)/1024, took)
		if err != nil {
			t.Errorf("%s: %v after %v", name, err, took)
			continue
		}
		if took > 5*time.Second {
			t.Errorf("%s: took %v, want well under 5 s", name, took)
		}
		g, err := gif.DecodeAll(bytes.NewReader(out))
		if err != nil || len(g.Image) != len(info2frames(src)) {
			t.Errorf("%s: %d frames err=%v, want the source's frames (no frame is ever created)", name, len(g.Image), err)
		}
	}
}

func info2frames(src []byte) []int {
	d, _ := gifDelays(src)
	return d
}
