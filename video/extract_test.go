// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
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

func TestExtractFirstFramePNG_MoovAtEnd(t *testing.T) {
	data := genTestMP4(t, false) // moov-at-end (the hard case)
	png, err := ExtractFirstFramePNG(context.Background(), bytes.NewReader(data), MaxBytes)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		t.Fatalf("output is not a PNG (len=%d, prefix=%q)", len(png), png[:min(8, len(png))])
	}
}

func TestExtractFirstFramePNG_Faststart(t *testing.T) {
	data := genTestMP4(t, true)
	png, err := ExtractFirstFramePNG(context.Background(), bytes.NewReader(data), MaxBytes)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if string(png[1:4]) != "PNG" {
		t.Fatalf("not a PNG")
	}
}

func TestExtractFirstFramePNG_TooLarge(t *testing.T) {
	data := genTestMP4(t, true)
	_, err := ExtractFirstFramePNG(context.Background(), bytes.NewReader(data), 1024) // 1KB cap
	if err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestExtractFirstFramePNG_NotAVideo(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg not available")
	}
	_, err := ExtractFirstFramePNG(context.Background(), bytes.NewReader([]byte("not a video")), MaxBytes)
	if err == nil {
		t.Fatalf("want error for non-video input, got nil")
	}
}

// resetSem resets the package-level semaphore so tests can override MaxConcurrent.
// Must only be called from test goroutines, never concurrently with ExtractFirstFramePNG.
func resetSem(n int) {
	semOnce = sync.Once{}
	sem = nil
	MaxConcurrent = n
}

// TestSemaphore_CancelledContextUnblocksWaiter verifies that a goroutine waiting
// for a semaphore slot returns promptly when its context is cancelled.
// This is deterministic: we hold the single slot ourselves, cancel the context,
// then assert the caller returned a context error — no timing dependency.
func TestSemaphore_CancelledContextUnblocksWaiter(t *testing.T) {
	resetSem(1)

	// Fill the only slot manually so the next acquirer must wait.
	ctx := context.Background()
	if err := acquireSem(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := acquireSem(cancelled)
	if err == nil {
		releaseSem() // clean up if we somehow got in
		t.Fatal("want context error, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	releaseSem() // release the slot we held at the start
}

// TestSemaphore_MaxConcurrentOverride verifies that setting MaxConcurrent=2
// allows exactly 2 concurrent acquirers and blocks a third until one releases.
// Deterministic: uses a pre-cancelled context for the overflow attempt.
func TestSemaphore_MaxConcurrentOverride(t *testing.T) {
	resetSem(2)

	ctx := context.Background()
	if err := acquireSem(ctx); err != nil {
		t.Fatalf("slot 1: %v", err)
	}
	if err := acquireSem(ctx); err != nil {
		releaseSem()
		t.Fatalf("slot 2: %v", err)
	}

	// A third acquire on a cancelled context must fail immediately.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acquireSem(cancelled); err == nil {
		releaseSem()
		releaseSem()
		releaseSem()
		t.Fatal("third acquire should have been blocked/rejected")
	}

	releaseSem()
	releaseSem()
}
