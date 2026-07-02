// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"io"
	"os"
)

// headWindow / tailWindow bound the bytes retained from the source stream when
// reconstructing a seekable temp file for first-frame extraction. They are NOT
// operator config (no Consul KV): like readIdleTimeout / extractCeiling they are
// fixed code-level constants, sized so generously that practically no real video
// — not even 8K — fails to yield a first frame from head+tail alone.
//
// The head is written straight to disk (free on RAM). The tail must be buffered
// in RAM until EOF (the stream length is unknown up front), so tailWindow costs
// roughly tailWindow x video-concurrency of RAM. Hence head >> tail.
var (
	headWindow = 64 << 20 // 64 MiB
	tailWindow = 32 << 20 // 32 MiB
)

const copyBufSize = 1 << 20 // 1 MiB stream read chunk

// tailRing retains the last `cap` bytes written to it, in chronological order.
type tailRing struct {
	buf      []byte
	capacity int
	w        int // next write index (mod capacity)
	full     bool
}

func newTailRing(c int) *tailRing {
	if c < 0 {
		c = 0
	}
	return &tailRing{buf: make([]byte, c), capacity: c}
}

func (t *tailRing) Write(p []byte) {
	if t.capacity == 0 || len(p) == 0 {
		return
	}
	// If this write alone is >= capacity, only its last cap bytes survive.
	if len(p) >= t.capacity {
		copy(t.buf, p[len(p)-t.capacity:])
		t.w = 0
		t.full = true
		return
	}
	end := t.w + len(p)
	if end <= t.capacity {
		copy(t.buf[t.w:], p)
	} else {
		first := t.capacity - t.w
		copy(t.buf[t.w:], p[:first])
		copy(t.buf, p[first:])
	}
	if end >= t.capacity {
		t.full = true
	}
	t.w = end % t.capacity
}

// Bytes returns the retained bytes in chronological (oldest-first) order.
func (t *tailRing) Bytes() []byte {
	if !t.full {
		out := make([]byte, t.w)
		copy(out, t.buf[:t.w])
		return out
	}
	out := make([]byte, t.capacity)
	n := copy(out, t.buf[t.w:])
	copy(out[n:], t.buf[:t.w])
	return out
}

// StreamToSparseTemp streams r into dst, retaining only the first headWindow
// bytes and the last tailWindow bytes; the middle is discarded and left as a
// sparse hole (reads as zeros). dst is sized to the true total via Truncate so
// the tail sits at its real offset and ffmpeg/ffprobe can seek to it. Returns
// the total number of bytes read from r.
//
// No size cap and no ctx: this mirrors io.Copy. Cancellation comes from r (the
// worker wraps the body in an idle-read watchdog bound to dlCtx).
func StreamToSparseTemp(dst *os.File, r io.Reader) (int64, error) {
	head := int64(headWindow)
	ring := newTailRing(tailWindow)
	buf := make([]byte, copyBufSize)
	var total int64

	// Phase 1: write the head straight to the file.
	for total < head {
		want := int64(len(buf))
		if rem := head - total; rem < want {
			want = rem
		}
		n, err := r.Read(buf[:want])
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil // whole stream fit within the head
		}
		if err != nil {
			return total, err
		}
	}

	// Phase 2: drain the remainder, retaining only the last tailWindow bytes.
	for {
		n, err := r.Read(buf)
		if n > 0 {
			ring.Write(buf[:n])
			total += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return total, err
		}
	}

	// Materialize: size to total (sparse hole in the middle), place the tail.
	tail := ring.Bytes()
	if err := dst.Truncate(total); err != nil {
		return total, err
	}
	if len(tail) > 0 {
		// total-len(tail) >= head always (len(tail) <= total-head), so this
		// never overwrites the head region.
		if _, err := dst.WriteAt(tail, total-int64(len(tail))); err != nil {
			return total, err
		}
	}
	return total, nil
}
