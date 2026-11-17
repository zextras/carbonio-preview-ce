// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"image/gif"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
)

const gifMime = "image/gif"

func (h *harness) interimKey(r thumbReq, size string) string {
	k := h.thumbKeyOf(r, size)
	return "g" + k[1:]
}

func gifHarness(t *testing.T, opts harnessOpts) (*harness, string) {
	t.Helper()
	realVips(t)
	h := newHarness(t, opts)
	return h, newID()
}

func failingGIF(t *testing.T) *int {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	prev := animatedGIFFunc
	animatedGIFFunc = func(context.Context, string, render.Info, int, string) ([]byte, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, render.ErrGIFFailed
	}
	t.Cleanup(func() { animatedGIFFunc = prev })
	return &calls
}

func TestThumbnailGet_StaticGIFIsSync(t *testing.T) {
	h, id := gifHarness(t, harnessOpts{noDB: true})
	src := gifOf(t, 500, 300, 1)
	h.seedSource(id, src)

	m := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "m"})
	mw, _, mf := decodeInfo(t, m.Body.Bytes())
	if m.Code != http.StatusOK || (mf != "jpeg" && mf != "png") || mw != 500 || bytes.Equal(m.Body.Bytes(), src) {
		t.Fatalf("m: %d %s %d, want a re-encoded static frame at 500 px (the source GIF is never served)", m.Code, mf, mw)
	}
	s := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"})
	w, _, f := decodeInfo(t, s.Body.Bytes())
	if s.Code != http.StatusOK || (f != "jpeg" && f != "png") || w != 320 {
		t.Fatalf("s: %d %s %d, want a 320 px static JPEG/PNG", s.Code, f, w)
	}
}

func TestThumbnailGet_AnimatedGIFWithoutDBIsResizedInTheRequest(t *testing.T) {
	var sides []int
	swap(t, &animatedGIFFunc, func(_ context.Context, _ string, _ render.Info, side int, _ string) ([]byte, error) {
		sides = append(sides, side)
		return []byte("GIF89a-capped"), nil
	})
	h, id := gifHarness(t, harnessOpts{noDB: true})
	h.seedSource(id, gifOf(t, 600, 400, 6))
	rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime})
	if rec.Code != http.StatusOK || rec.Body.String() != "GIF89a-capped" || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("status %d %q, want the ffmpeg output even though the source fits M", rec.Code, rec.Body)
	}
	if len(sides) != 1 || sides[0] != render.SideM {
		t.Errorf("ffmpeg runs = %v, want one M pass", sides)
	}
}

func TestThumbnailGet_AnimatedGIFBelowMIsAnAsyncJobPerRequestedSize(t *testing.T) {
	h, id := gifHarness(t, harnessOpts{noWorker: true})
	h.seedSource(id, gifOf(t, 600, 400, 6))

	rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("s: %d, want 202", rec.Code)
	}
	rows := h.pair(id, testVersion)
	if s := rows[db.SizeS]; len(rows) != 1 || s == nil || s.Status != db.StatusPending || s.Kind != "gif" {
		t.Fatalf("rows = %+v, want only S PENDING gif", rows)
	}
	rec = h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "m"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("m: %d, want 202 (a GIF below M is capped and re-encoded too)", rec.Code)
	}
	if m := h.pair(id, testVersion)[db.SizeM]; m == nil || m.Status != db.StatusPending || m.Kind != "gif" {
		t.Errorf("M row = %+v, want PENDING gif", m)
	}
}

func TestThumbnailGet_AnimatedGIF202WithInterimFrame(t *testing.T) {
	h, id := gifHarness(t, harnessOpts{noWorker: true})
	h.seedSource(id, gifOf(t, 1600, 900, 6))

	for size, side := range map[string]int{"m": 1280, "s": 320} {
		rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: size})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s: %d, want 202", size, rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Retry-After") != "3" {
			t.Errorf("%s: headers %v", size, rec.Header())
		}
		ct := rec.Header().Get("Content-Type")
		w, _, f := decodeInfo(t, rec.Body.Bytes())
		if (ct != "image/jpeg" && ct != "image/png") || (f != "jpeg" && f != "png") || w != side {
			t.Errorf("%s: interim body %s %s %d wide, want a static frame %d wide", size, ct, f, w, side)
		}
	}
	for size, r := range h.pair(id, testVersion) {
		if r.Status != db.StatusPending || r.Kind != "gif" {
			t.Errorf("row %s = %+v", size, r)
		}
	}
}

func TestThumbnailGet_InterimFrameCachedWhilePending(t *testing.T) {
	h, id := gifHarness(t, harnessOpts{noWorker: true})
	h.seedSource(id, gifOf(t, 1600, 900, 6))
	req := thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"}

	first := h.get(req)
	second := h.get(req)
	third := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "m"})
	if first.Code != 202 || second.Code != 202 || third.Code != 202 {
		t.Fatalf("codes %d %d %d", first.Code, second.Code, third.Code)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) || third.Body.Len() == 0 {
		t.Error("polls must serve the cached frame")
	}
	if n := h.sourceReads(); n != 2 {
		t.Errorf("source reads = %d, want one per size (repeated polls of s must not re-download, m needs its own frame)", n)
	}
	if _, ok := h.deps.Cache.Get(h.interimKey(req, "s")); !ok {
		t.Error("interim frame not cached under the interim key")
	}
	if _, ok := h.deps.Cache.Get(h.thumbKeyOf(req, "s")); ok {
		t.Error("a pending frame must not sit under the final key")
	}
}

func TestThumbnailGet_AnimatedGIFBecomesReadyWithAllFrames(t *testing.T) {
	realFFmpeg(t)
	h, id := gifHarness(t, harnessOpts{})
	const frames = 8
	h.seedSource(id, gifOf(t, 1600, 900, frames))
	for _, size := range []string{"m", "s"} {
		if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: size}); rec.Code != http.StatusAccepted {
			t.Fatalf("miss %s: %d", size, rec.Code)
		}
	}
	rows := h.waitStatus(id, testVersion, db.StatusReady)
	for size, r := range rows {
		if r.Kind != "gif" || r.BlobID == nil || *r.ContentType != "image/gif" {
			t.Errorf("row %s = %+v", size, r)
		}
	}
	for size, side := range map[string]int{"m": 1280, "s": 320} {
		rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: size})
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
			t.Fatalf("%s: %d %q", size, rec.Code, rec.Header().Get("Content-Type"))
		}
		g, err := gif.DecodeAll(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatalf("%s: %v", size, err)
		}
		if len(g.Image) != frames/2 || g.Config.Width != side {
			t.Errorf("%s: %d frames %d wide, want %d frames (the 20 fps fixture capped at 10 fps) %d wide", size, len(g.Image), g.Config.Width, frames/2, side)
		}
	}
}

func TestThumbnailGet_GIFReadyDropsInterim(t *testing.T) {
	realFFmpeg(t)
	h, id := gifHarness(t, harnessOpts{})
	h.seedSource(id, gifOf(t, 1600, 900, 4))
	req := thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"}

	if rec := h.get(req); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	if _, ok := h.deps.Cache.Get(h.interimKey(req, "s")); !ok {
		t.Fatal("interim not cached")
	}
	h.waitSize(id, testVersion, "s", db.StatusReady)
	if rows := h.pair(id, testVersion); len(rows) != 1 {
		t.Fatalf("rows = %+v, only the requested size", rows)
	}
	if rec := h.get(req); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("ready: %d", rec.Code)
	}
	if _, ok := h.deps.Cache.Get(h.interimKey(req, "s")); ok {
		t.Error("the interim frame must be dropped once the rendition is READY")
	}
}

func TestThumbnailGet_GIFFailedServesStaticFrame200(t *testing.T) {
	calls := failingGIF(t)
	h, id := gifHarness(t, harnessOpts{cfg: func(_ *testing.T, d Deps) Deps {
		d.Cfg.AsyncMaxAttempts = 1
		return d
	}})
	h.seedSource(id, gifOf(t, 1600, 900, 4))
	req := thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"}

	if rec := h.get(req); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitSize(id, testVersion, "s", db.StatusFailed)
	if *calls != 1 {
		t.Fatal("the job never ran ffmpeg")
	}
	h.deps.Cache.Delete(h.thumbKeyOf(req, "s"))
	h.deps.Cache.Delete(h.interimKey(req, "s"))

	rec := h.get(req)
	w, _, f := decodeInfo(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || (f != "jpeg" && f != "png") || w != 320 {
		t.Fatalf("FAILED: %d %s %d, want a 200 static frame", rec.Code, f, w)
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Errorf("final answer carries %v", rec.Header())
	}
	before := h.sourceReads()
	if again := h.get(req); again.Code != http.StatusOK || h.sourceReads() != before {
		t.Errorf("the static frame must be cached under the final key (reads %d -> %d)", before, h.sourceReads())
	}
}

func TestGIFJob_OnePassPerRequestedSizeUnderAsyncSlot(t *testing.T) {
	var h *harness
	var mu sync.Mutex
	var slots []int
	var sides []int
	prev := animatedGIFFunc
	animatedGIFFunc = func(_ context.Context, in string, _ render.Info, side int, _ string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		slots = append(slots, len(h.deps.AsyncSem))
		sides = append(sides, side)
		return []byte("GIF89a-" + string(rune('0'+len(sides)))), nil
	}
	t.Cleanup(func() { animatedGIFFunc = prev })
	var id string
	h, id = gifHarness(t, harnessOpts{})
	h.seedSource(id, gifOf(t, 1600, 900, 4))

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "m"}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitSize(id, testVersion, "m", db.StatusReady)
	mu.Lock()
	if len(sides) != 1 || sides[0] != 1280 {
		t.Fatalf("ffmpeg runs after an m request = %v, want only M", sides)
	}
	mu.Unlock()

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"}); rec.Code != http.StatusAccepted {
		t.Fatalf("s miss: %d", rec.Code)
	}
	h.waitSize(id, testVersion, "s", db.StatusReady)
	mu.Lock()
	defer mu.Unlock()
	if len(sides) != 2 || sides[1] != 320 {
		t.Fatalf("ffmpeg runs = %v, want M then S", sides)
	}
	for _, n := range slots {
		if n != 1 {
			t.Errorf("a GIF pass ran holding %d async slots, want exactly 1", n)
		}
	}
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("async slot leaked: %d", n)
	}
}

func TestGIFJob_EachSizeStraightFromTheSource(t *testing.T) {
	var mu sync.Mutex
	type call struct {
		side  int
		input []byte
	}
	var calls []call
	prev := animatedGIFFunc
	animatedGIFFunc = func(_ context.Context, in string, _ render.Info, side int, _ string) ([]byte, error) {
		b, err := os.ReadFile(in)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		calls = append(calls, call{side, b})
		mu.Unlock()
		return []byte("out-" + string(rune('0'+side/320))), nil
	}
	t.Cleanup(func() { animatedGIFFunc = prev })
	h, id := gifHarness(t, harnessOpts{})
	src := gifOf(t, 1600, 900, 4)
	h.seedSource(id, src)

	for _, size := range []string{"m", "s"} {
		if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: size}); rec.Code != http.StatusAccepted {
			t.Fatalf("miss %s: %d", size, rec.Code)
		}
	}
	h.waitStatus(id, testVersion, db.StatusReady)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("ffmpeg runs = %d, want one per requested size", len(calls))
	}
	for _, c := range calls {
		if !bytes.Equal(c.input, src) {
			t.Errorf("side %d: ffmpeg input is not the source (S must never be derived from M): %q", c.side, c.input)
		}
	}
}

func TestGIFJob_FailureCountsAttempt(t *testing.T) {
	failingGIF(t)
	h, id := gifHarness(t, harnessOpts{})
	h.seedSource(id, gifOf(t, 1600, 900, 4))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "attempt counted and the row back to PENDING", func() bool {
		rows := h.pair(id, testVersion)
		return len(rows) == 1 && rows[db.SizeM].Status == db.StatusPending && rows[db.SizeM].Attempts == 1
	})
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("async slot leaked: %d", n)
	}
}
