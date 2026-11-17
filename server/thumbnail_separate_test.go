// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
)

type sideLog struct {
	mu    sync.Mutex
	sides []int
}

func (l *sideLog) add(side int) {
	l.mu.Lock()
	l.sides = append(l.sides, side)
	l.mu.Unlock()
}

func (l *sideLog) get() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.sides)
}

func stubFor(side int) render.Rendition {
	if side == render.SideS {
		return stubS
	}
	return stubM
}

func stubImageRender(t *testing.T) *sideLog {
	t.Helper()
	l := &sideLog{}
	swap(t, &imageThumbFunc, func(_ []byte, side int) (render.Rendition, error) {
		l.add(side)
		return stubFor(side), nil
	})
	swap(t, &pdfThumbFunc, func(_ context.Context, _ []byte, side int) (render.Rendition, error) {
		l.add(side)
		return stubFor(side), nil
	})
	return l
}

func (h *harness) settle() { h.deps.Limits.bg.Wait() }

func (h *harness) blockUploads(t *testing.T) (started <-chan struct{}, release func()) {
	t.Helper()
	st := make(chan struct{}, 16)
	rel := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(rel) }) }
	t.Cleanup(release)
	h.store.mu.Lock()
	h.store.putErr = func(memKey) error {
		st <- struct{}{}
		<-rel
		return nil
	}
	h.store.mu.Unlock()
	return st, release
}

func (h *harness) getAsync(r thumbReq) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() { ch <- h.getNow(r) }()
	return ch
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

var syncKinds = []struct{ name, mime, source string }{
	{"image", "image/webp", "webp-bytes"},
	{"pdf", "application/pdf", "%PDF-fake"},
}

var sizeSides = map[string]int{"s": render.SideS, "m": render.SideM}

func TestSyncGet_RendersAndStoresOnlyTheRequestedSize(t *testing.T) {
	for _, k := range syncKinds {
		for size, side := range sizeSides {
			t.Run(k.name+"_"+size, func(t *testing.T) {
				log := stubImageRender(t)
				h := newHarness(t, harnessOpts{})
				id := newID()
				h.seedSource(id, []byte(k.source))

				rec := h.get(thumbReq{id: id, version: testVersion, mime: k.mime, size: size})
				want := stubFor(side)
				if rec.Code != http.StatusOK || rec.Body.String() != string(want.Body) {
					t.Fatalf("%d %q, want 200 %q", rec.Code, rec.Body, want.Body)
				}
				h.settle()
				if got := log.get(); !slices.Equal(got, []int{side}) {
					t.Errorf("rendered sides = %v, want only %d", got, side)
				}
				rows := h.pair(id, testVersion)
				if len(rows) != 1 || rows[size] == nil || rows[size].Status != db.StatusReady || rows[size].BlobID == nil {
					t.Fatalf("rows = %+v, want only a READY %s row with a blob", rows, size)
				}
				if n := len(h.store.putKeys()); n != 1 {
					t.Errorf("uploads = %d, want 1", n)
				}
				if blob, err := h.store.get(*rows[size].BlobID, testVersion, "files", testOwner); err != nil || !bytes.Equal(blob, want.Body) {
					t.Errorf("stored blob = %q %v, want %q", blob, err, want.Body)
				}
				other := map[string]string{"s": "m", "m": "s"}[size]
				if _, ok := h.deps.Cache.Get(h.thumbKeyOf(thumbReq{id: id, version: testVersion}, other)); ok {
					t.Errorf("size %s sits in the cache after a %s request", other, size)
				}

				rec = h.get(thumbReq{id: id, version: testVersion, mime: k.mime, size: other})
				if rec.Code != http.StatusOK || rec.Body.String() != string(stubFor(sizeSides[other]).Body) {
					t.Fatalf("other size: %d %q", rec.Code, rec.Body)
				}
				h.settle()
				if got := log.get(); !slices.Equal(got, []int{side, sizeSides[other]}) {
					t.Errorf("rendered sides = %v, want one render per requested size", got)
				}
				if len(h.pair(id, testVersion)) != 2 || len(h.store.putKeys()) != 2 {
					t.Errorf("rows=%d uploads=%d, want 2 each", len(h.pair(id, testVersion)), len(h.store.putKeys()))
				}
			})
		}
	}
}

func TestSyncGet_ResponseReturnsBeforeTheUploadCompletes(t *testing.T) {
	for _, k := range syncKinds {
		t.Run(k.name, func(t *testing.T) {
			log := stubImageRender(t)
			h := newHarness(t, harnessOpts{})
			started, release := h.blockUploads(t)
			id := newID()
			h.seedSource(id, []byte(k.source))
			req := thumbReq{id: id, version: testVersion, mime: k.mime, size: "s"}

			rec := recv(t, h.getAsync(req), "the response while the upload is blocked")
			if rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) {
				t.Fatalf("%d %q, want 200 with S", rec.Code, rec.Body)
			}
			recv(t, started, "the background upload to start")
			if rows := h.pair(id, testVersion); len(rows) != 0 {
				t.Errorf("row written before the upload finished: %+v", rows)
			}

			again := recv(t, h.getAsync(req), "a second request during the upload")
			if again.Code != http.StatusOK || again.Body.String() != string(stubS.Body) {
				t.Fatalf("second: %d %q", again.Code, again.Body)
			}
			if n := len(log.get()); n != 1 {
				t.Errorf("renders = %d, the second request must come from the cache", n)
			}

			release()
			h.settle()
			if r := h.pair(id, testVersion)[db.SizeS]; r == nil || r.Status != db.StatusReady || r.BlobID == nil {
				t.Errorf("row after the upload = %+v", r)
			}
		})
	}
}

func TestSyncGet_BackgroundSaveFailureLeavesNothingAndRegenerates(t *testing.T) {
	log := stubImageRender(t)
	h := newHarness(t, harnessOpts{})
	h.store.putErr = func(memKey) error { return errors.New("storages down") }
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))
	req := thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s"}

	if rec := h.get(req); rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) {
		t.Fatalf("%d %q, a failed save must not fail the response", rec.Code, rec.Body)
	}
	h.settle()
	if rows := h.pair(id, testVersion); len(rows) != 0 {
		t.Errorf("rows after a failed save: %+v", rows)
	}
	if n := h.store.blobCount(); n != 1 {
		t.Errorf("blobs = %d, only the source must remain", n)
	}

	h.store.mu.Lock()
	h.store.putErr = nil
	h.store.mu.Unlock()
	h.deps.Cache.Delete(h.thumbKeyOf(req, "s"))
	if rec := h.get(req); rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) {
		t.Fatalf("later miss: %d %q", rec.Code, rec.Body)
	}
	h.settle()
	if got := log.get(); !slices.Equal(got, []int{render.SideS, render.SideS}) {
		t.Errorf("rendered sides = %v, the later miss must regenerate only S", got)
	}
	if r := h.pair(id, testVersion)[db.SizeS]; r == nil || r.Status != db.StatusReady || r.BlobID == nil {
		t.Errorf("row after the retry = %+v", r)
	}
}

func TestSyncGet_ShutdownDrainsAPendingBackgroundSave(t *testing.T) {
	stubImageRender(t)
	h := newHarness(t, harnessOpts{})
	started, release := h.blockUploads(t)
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))

	rec := recv(t, h.getAsync(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "m"}), "the response")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	recv(t, started, "the background upload to start")

	short, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if h.deps.Limits.drain(short) {
		t.Fatal("drain returned while the save was still uploading")
	}
	release()
	long, cancelLong := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelLong()
	if !h.deps.Limits.drain(long) {
		t.Fatal("drain did not finish after the upload completed")
	}
	if r := h.pair(id, testVersion)[db.SizeM]; r == nil || r.Status != db.StatusReady || r.BlobID == nil {
		t.Errorf("row after the drain = %+v", r)
	}
}

func TestOfficeJob_OnlyTheRequestedSizeIsProcessed(t *testing.T) {
	calls := stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	log := stubImageRender(t)
	h, id := officeHarness(t)

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime, size: "s"}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "S READY", func() bool {
		r := h.pair(id, testVersion)[db.SizeS]
		return r != nil && r.Status == db.StatusReady
	})
	h.settle()
	if rows := h.pair(id, testVersion); len(rows) != 1 {
		t.Fatalf("rows = %+v, an S request must not create an M row", rows)
	}
	if got := log.get(); !slices.Equal(got, []int{render.SideS}) {
		t.Errorf("rendered sides = %v, want only S", got)
	}
	if n := len(h.store.putKeys()); n != 1 {
		t.Errorf("uploads = %d, want 1", n)
	}

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime, size: "m"}); rec.Code != http.StatusAccepted {
		t.Fatalf("m miss: %d", rec.Code)
	}
	eventually(t, "M READY", func() bool {
		r := h.pair(id, testVersion)[db.SizeM]
		return r != nil && r.Status == db.StatusReady
	})
	if n := len(*calls); n != 2 {
		t.Errorf("collabora conversions = %d, each size runs its own job", n)
	}
	if got := log.get(); !slices.Equal(got, []int{render.SideS, render.SideM}) {
		t.Errorf("rendered sides = %v", got)
	}
}

func TestVideoJob_OnlyTheRequestedSizeIsProcessed(t *testing.T) {
	stubVideoPipeline(t)
	log := stubImageRender(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("fake-video"))

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime, size: "s"}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "S READY", func() bool {
		r := h.pair(id, testVersion)[db.SizeS]
		return r != nil && r.Status == db.StatusReady
	})
	h.settle()
	if rows := h.pair(id, testVersion); len(rows) != 1 {
		t.Fatalf("rows = %+v, an S request must not create an M row", rows)
	}
	if got := log.get(); !slices.Equal(got, []int{render.SideS}) {
		t.Errorf("rendered sides = %v, want only S", got)
	}
	if n := len(h.store.putKeys()); n != 1 {
		t.Errorf("uploads = %d, want 1", n)
	}
}

func TestAsyncJob_PollDuringTheUploadIsServedFromTheCache(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	stubImageRender(t)
	h, id := officeHarness(t)
	started, release := h.blockUploads(t)
	req := thumbReq{id: id, version: testVersion, mime: docxMime, size: "s"}

	if rec := h.get(req); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	recv(t, started, "the job upload to start")

	rec := recv(t, h.getAsync(req), "the poll during the upload")
	if rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) || rec.Header().Get("Content-Type") != stubS.ContentType {
		t.Fatalf("poll: %d %q, want 200 from the cache", rec.Code, rec.Body)
	}
	if r := h.pair(id, testVersion)[db.SizeS]; r == nil || r.Status != db.StatusGenerating {
		t.Errorf("row during the upload = %+v, want GENERATING", r)
	}

	release()
	eventually(t, "S READY", func() bool {
		r := h.pair(id, testVersion)[db.SizeS]
		return r != nil && r.Status == db.StatusReady
	})
}

func TestGIFJob_OnlyTheRequestedSizeFromTheSource(t *testing.T) {
	var mu sync.Mutex
	type call struct {
		side  int
		input []byte
	}
	var calls []call
	swap(t, &animatedGIFFunc, func(_ context.Context, in string, _ render.Info, side int, _ string) ([]byte, error) {
		b, err := os.ReadFile(in)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		calls = append(calls, call{side, b})
		mu.Unlock()
		return []byte("GIF89a-out"), nil
	})
	h, id := gifHarness(t, harnessOpts{})
	src := gifOf(t, 1600, 900, 4)
	h.seedSource(id, src)

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "S READY", func() bool {
		r := h.pair(id, testVersion)[db.SizeS]
		return r != nil && r.Status == db.StatusReady
	})
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0].side != render.SideS || !bytes.Equal(calls[0].input, src) {
		t.Fatalf("ffmpeg runs = %+v, want one S pass straight from the source", calls)
	}
	if rows := h.pair(id, testVersion); len(rows) != 1 {
		t.Errorf("rows = %+v, an S request must not create an M row", rows)
	}
}
