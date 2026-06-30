// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"os"
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
