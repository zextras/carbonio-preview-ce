// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestTailRing_RetainsLastBytes(t *testing.T) {
	cases := []struct {
		name  string
		cap   int
		write [][]byte
		want  []byte
	}{
		{"empty", 4, nil, []byte{}},
		{"under cap, single", 8, [][]byte{[]byte("abc")}, []byte("abc")},
		{"under cap, multi", 8, [][]byte{[]byte("ab"), []byte("cd")}, []byte("abcd")},
		{"exactly cap", 4, [][]byte{[]byte("abcd")}, []byte("abcd")},
		{"over cap, single", 4, [][]byte{[]byte("abcdef")}, []byte("cdef")},
		{"over cap, multi wrap", 4, [][]byte{[]byte("abc"), []byte("def")}, []byte("cdef")},
		{"over cap, many small", 3, [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")}, []byte("cde")},
		{"big then small", 4, [][]byte{[]byte("abcdef"), []byte("gh")}, []byte("efgh")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTailRing(tc.cap)
			for _, w := range tc.write {
				r.Write(w)
			}
			if got := r.Bytes(); !bytes.Equal(got, tc.want) {
				t.Fatalf("ring=%q, want %q", got, tc.want)
			}
		})
	}
}

// withWindows temporarily overrides headWindow/tailWindow for a test.
func withWindows(t *testing.T, head, tail int) {
	t.Helper()
	oh, ot := headWindow, tailWindow
	headWindow, tailWindow = head, tail
	t.Cleanup(func() { headWindow, tailWindow = oh, ot })
}

// readSparse runs StreamToSparseTemp into a fresh temp file and reads it back.
func readSparse(t *testing.T, src []byte) []byte {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "sparse-*.bin")
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	defer f.Close()
	n, err := StreamToSparseTemp(f, bytes.NewReader(src))
	if err != nil {
		t.Fatalf("StreamToSparseTemp: %v", err)
	}
	if n != int64(len(src)) {
		t.Fatalf("total=%d, want %d", n, len(src))
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("readfile: %v", err)
	}
	return got
}

func TestStreamToSparseTemp_FitsInHead(t *testing.T) {
	withWindows(t, 16, 8)
	src := []byte("0123456789") // 10 < head(16)
	got := readSparse(t, src)
	if !bytes.Equal(got, src) {
		t.Fatalf("got %q, want %q", got, src)
	}
}

func TestStreamToSparseTemp_ContiguousNoHole(t *testing.T) {
	withWindows(t, 8, 8)
	// total=12: head[0:8] + tail = last 8 bytes [4:12]; tail offset = 12-8 = 4 < head(8)
	// => tail overlaps the head region, written contiguously, no hole, no gap.
	src := []byte("ABCDEFGHIJKL") // 12 bytes
	got := readSparse(t, src)
	if !bytes.Equal(got, src) {
		t.Fatalf("got %q, want %q", got, src)
	}
}

func TestStreamToSparseTemp_HasHole(t *testing.T) {
	withWindows(t, 4, 4)
	// total=12: head = [0:4]="ABCD"; tail = last 4 = [8:12]="IJKL" at offset 8.
	// middle [4:8] is a sparse hole => zeros.
	src := []byte("ABCDEFGHIJKL")
	got := readSparse(t, src)
	want := append(append([]byte("ABCD"), 0, 0, 0, 0), []byte("IJKL")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len(got) != len(src) {
		t.Fatalf("size=%d, want %d", len(got), len(src))
	}
}

// genTestVideo builds a fixture in the given container/codec, large enough to
// exceed the tiny test windows so the sparse path leaves a real hole. Skips if
// the encoder/muxer is unavailable in this ffmpeg build.
func genTestVideo(t *testing.T, ext string, encArgs []string, faststart bool) []byte {
	t.Helper()
	if !ffmpegAvailable() {
		t.Skip("ffmpeg not available")
	}
	f, err := os.CreateTemp(t.TempDir(), "fixture-*."+ext)
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	f.Close()
	// High-detail testsrc, several seconds, 640x480 => comfortably > 48 KiB.
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=640x480:rate=25",
	}
	args = append(args, encArgs...)
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, f.Name())
	cmd := exec.Command(FFmpegPath, args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Skipf("ffmpeg cannot produce %s with %v (encoder/muxer absent?): %v\n%s",
			ext, encArgs, err, errb.String())
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("readfile: %v", err)
	}
	return data
}

// assertSparseExtract reconstructs data through StreamToSparseTemp with tiny
// windows (forcing a hole), then asserts ffprobe sees a codec and ffmpeg
// extracts a PNG frame — proving head+tail suffices for this container.
func assertSparseExtract(t *testing.T, data []byte) {
	t.Helper()
	withWindows(t, 32<<10, 16<<10) // 32 KiB head, 16 KiB tail
	if len(data) <= headWindow+tailWindow {
		t.Skipf("fixture too small (%d B) to exercise a hole with %d+%d windows",
			len(data), headWindow, tailWindow)
	}
	f, err := os.CreateTemp(t.TempDir(), "sparse-*.bin")
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	defer f.Close()
	if _, err := StreamToSparseTemp(f, bytes.NewReader(data)); err != nil {
		t.Fatalf("StreamToSparseTemp: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	ctx := context.Background()
	codec, err := DetectCodecFromFile(ctx, f.Name())
	if err != nil || codec == "" {
		t.Fatalf("probe from sparse failed: codec=%q err=%v", codec, err)
	}
	png, err := ExtractFirstFramePNGFromFile(ctx, f.Name())
	if err != nil {
		t.Fatalf("extract from sparse failed (codec=%s): %v", codec, err)
	}
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		t.Fatalf("not a PNG (codec=%s, len=%d)", codec, len(png))
	}
	t.Logf("OK: container yielded codec=%s, frame=%d bytes from %d-byte source (sparse)",
		codec, len(png), len(data))
}

func TestSparseExtract_AllContainers(t *testing.T) {
	cases := []struct {
		name      string
		ext       string
		encArgs   []string
		faststart bool
	}{
		{"mp4_h264_faststart", "mp4", []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-b:v", "2M"}, true},
		{"mp4_h264_moov_end", "mp4", []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-b:v", "2M"}, false},
		{"mov_h264_moov_end", "mov", []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-b:v", "2M"}, false},
		{"webm_vp9", "webm", []string{"-c:v", "libvpx-vp9", "-b:v", "1M"}, false},
		{"webm_vp8", "webm", []string{"-c:v", "libvpx", "-b:v", "1M"}, false},
		{"mkv_h264", "mkv", []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-b:v", "2M"}, false},
		{"mpegts_mpeg2", "ts", []string{"-c:v", "mpeg2video", "-b:v", "2M"}, false},
		{"avi_mpeg4", "avi", []string{"-c:v", "mpeg4", "-b:v", "2M"}, false},
		{"ogg_theora", "ogv", []string{"-c:v", "libtheora", "-b:v", "1M"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := genTestVideo(t, tc.ext, tc.encArgs, tc.faststart)
			assertSparseExtract(t, data)
		})
	}
}
