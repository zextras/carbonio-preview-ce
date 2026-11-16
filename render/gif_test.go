// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	"errors"
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
