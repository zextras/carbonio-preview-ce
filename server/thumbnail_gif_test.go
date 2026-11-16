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
	if m.Code != http.StatusOK || !bytes.Equal(m.Body.Bytes(), src) || m.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("m must be the original GIF: %d %q", m.Code, m.Header().Get("Content-Type"))
	}
	s := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"})
	w, _, f := decodeInfo(t, s.Body.Bytes())
	if s.Code != http.StatusOK || (f != "jpeg" && f != "png") || w != 320 {
		t.Fatalf("s: %d %s %d, want a 320 px static JPEG/PNG", s.Code, f, w)
	}
}

func TestThumbnailGet_AnimatedGIFWithoutDB424(t *testing.T) {
	h, id := gifHarness(t, harnessOpts{noDB: true})
	h.seedSource(id, gifOf(t, 600, 400, 6))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime}); rec.Code != http.StatusFailedDependency {
		t.Fatalf("status %d, want 424", rec.Code)
	}
}

func TestThumbnailGet_AnimatedGIFBelowMWritesOriginalMAndEnqueuesS(t *testing.T) {
	h, id := gifHarness(t, harnessOpts{noWorker: true})
	src := gifOf(t, 600, 400, 6)
	h.seedSource(id, src)

	rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("s: %d, want 202", rec.Code)
	}
	rows := h.pair(id, testVersion)
	m, s := rows[db.SizeM], rows[db.SizeS]
	if m == nil || m.Status != db.StatusReady || !m.Original || m.BlobID != nil || m.Kind != "gif" || *m.ContentType != "image/gif" {
		t.Errorf("M row = %+v, want a READY original without blob", m)
	}
	if s == nil || s.Status != db.StatusPending || s.Kind != "gif" {
		t.Errorf("S row = %+v, want PENDING gif", s)
	}
	rec = h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "m"})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), src) || rec.Header().Get("Content-Type") != "image/gif" {
		t.Errorf("m: %d, want the source GIF", rec.Code)
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
	if n := h.sourceReads(); n != 1 {
		t.Errorf("source reads = %d, polls must not re-download the source", n)
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
	req := thumbReq{id: id, version: testVersion, mime: gifMime, size: "m"}

	if rec := h.get(req); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	rows := h.waitStatus(id, testVersion, db.StatusReady)
	for size, r := range rows {
		if r.Kind != "gif" || r.BlobID == nil || r.Original || *r.ContentType != "image/gif" {
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
		if len(g.Image) != frames || g.Config.Width != side {
			t.Errorf("%s: %d frames %d wide, want %d frames %d wide", size, len(g.Image), g.Config.Width, frames, side)
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
	h.waitStatus(id, testVersion, db.StatusReady)
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
	h.waitStatus(id, testVersion, db.StatusFailed)
	if *calls == 0 {
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

func TestGIFJob_TwoPassUnderAsyncSlot(t *testing.T) {
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

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitStatus(id, testVersion, db.StatusReady)
	mu.Lock()
	defer mu.Unlock()
	if len(sides) != 2 || sides[0] != 1280 || sides[1] != 320 {
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

func TestGIFJob_SFromM(t *testing.T) {
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

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitStatus(id, testVersion, db.StatusReady)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0].side != 1280 || !bytes.Equal(calls[0].input, src) {
		t.Fatalf("M pass wrong: %+v", calls)
	}
	if calls[1].side != 320 || !bytes.Equal(calls[1].input, []byte("out-4")) {
		t.Errorf("S must be derived from the M output, got input %q", calls[1].input)
	}
}

func TestGIFJob_FailureCountsAttempt(t *testing.T) {
	failingGIF(t)
	h, id := gifHarness(t, harnessOpts{})
	h.seedSource(id, gifOf(t, 1600, 900, 4))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "attempt counted and rows back to PENDING", func() bool {
		rows := h.pair(id, testVersion)
		return len(rows) == 2 && rows[db.SizeM].Status == db.StatusPending && rows[db.SizeM].Attempts == 1 && rows[db.SizeS].Attempts == 1
	})
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("async slot leaked: %d", n)
	}
}
