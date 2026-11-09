// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"context"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"testing"
)

// TestMain resolves a usable ffmpeg: prefer the production carbonio path
// (/opt/zextras/common/bin/ffmpeg), else fall back to ffmpeg on PATH (dev
// machines / CI build image with distro ffmpeg). If none, ffmpeg tests skip.
func TestMain(m *testing.M) {
	if _, err := os.Stat(FFmpegPath); err != nil {
		if p, lookErr := exec.LookPath("ffmpeg"); lookErr == nil {
			FFmpegPath = p
		}
	}
	os.Exit(m.Run())
}

func ffmpegAvailable() bool {
	_, err := exec.LookPath(FFmpegPath)
	return err == nil
}

// genTestMP4 builds a tiny H.264 MP4 with ffmpeg. faststart=false reproduces
// the moov-at-end layout of real phone/screen recordings.
// MP4 requires a seekable output (moov box is written at the end and then
// relocated), so we always write to a temp file and read it back.
func genTestMP4(t *testing.T, faststart bool) []byte {
	t.Helper()
	if !ffmpegAvailable() {
		t.Skip("ffmpeg not available")
	}
	tmp, err := os.CreateTemp("", "carbonio-preview-test-*.mp4")
	if err != nil {
		t.Fatalf("genTestMP4: create temp: %v", err)
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-frames:v", "10",
	}
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, tmp.Name())
	cmd := exec.Command(FFmpegPath, args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("genTestMP4: %v\n%s", err, errb.String())
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("genTestMP4: read: %v", err)
	}
	return data
}

func TestExtractFirstFrameJPEG_MoovAtEnd(t *testing.T) {
	data := genTestMP4(t, false) // moov-at-end (the hard case)
	jpg, err := ExtractFirstFrameJPEG(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Fatalf("output is not a JPEG (len=%d, prefix=%q)", len(jpg), jpg[:min(8, len(jpg))])
	}
}

func TestExtractFirstFrameJPEG_Faststart(t *testing.T) {
	data := genTestMP4(t, true)
	jpg, err := ExtractFirstFrameJPEG(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Fatalf("not a JPEG")
	}
}

func TestExtractFirstFrameJPEG_DecodesWithSameSizeAndColors(t *testing.T) {
	data := genTestMP4(t, true)
	jpg, err := ExtractFirstFrameJPEG(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	if b := got.Bounds(); b.Dx() != 320 || b.Dy() != 240 {
		t.Fatalf("dimensions: got %dx%d, want 320x240", b.Dx(), b.Dy())
	}

	in, err := os.CreateTemp(t.TempDir(), "ref-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	in.Write(data)
	in.Close()
	cmd := exec.Command(FFmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", in.Name(), "-an", "-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("reference png: %v: %s", err, errb.String())
	}
	ref, err := png.Decode(&out)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	var sumAbs, mr, mg, mb, rr, rg, rb float64
	b := ref.Bounds()
	n := float64(b.Dx() * b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r1, g1, b1, _ := ref.At(x, y).RGBA()
			r2, g2, b2, _ := got.At(x, y).RGBA()
			rr += float64(r1 >> 8)
			rg += float64(g1 >> 8)
			rb += float64(b1 >> 8)
			mr += float64(r2 >> 8)
			mg += float64(g2 >> 8)
			mb += float64(b2 >> 8)
			sumAbs += math.Abs(float64(r1>>8)-float64(r2>>8)) + math.Abs(float64(g1>>8)-float64(g2>>8)) + math.Abs(float64(b1>>8)-float64(b2>>8))
		}
	}
	for name, d := range map[string]float64{"r": (mr - rr) / n, "g": (mg - rg) / n, "b": (mb - rb) / n} {
		if math.Abs(d) > 3 {
			t.Errorf("mean %s differs from the PNG path by %.2f levels", name, d)
		}
	}
	if mad := sumAbs / (3 * n); mad > 6 {
		t.Errorf("mean absolute difference vs PNG path: %.2f levels", mad)
	}
}

func TestExtractFirstFrameJPEG_NotAVideo(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg not available")
	}
	_, err := ExtractFirstFrameJPEG(context.Background(), bytes.NewReader([]byte("not a video")))
	if err == nil {
		t.Fatalf("want error for non-video input, got nil")
	}
}
