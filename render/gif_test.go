// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"errors"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
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
		const seconds = 2
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
}

func TestAnimatedGIF_NeverUpscales(t *testing.T) {
	for name, dims := range map[string][2]int{"landscape": {200, 100}, "portrait": {100, 200}} {
		src := genGIF(t, dims[0], dims[1], 8)
		for _, side := range []int{SideS, SideM} {
			_, oi := resizedGIF(t, src, side)
			if oi.Width != dims[0] || oi.Height != dims[1] {
				t.Errorf("%s at side %d: %dx%d, want the source size %dx%d", name, side, oi.Width, oi.Height, dims[0], dims[1])
			}
		}
	}
}
