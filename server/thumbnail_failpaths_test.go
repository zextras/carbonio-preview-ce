// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

func (h *harness) rowCount(id string) int {
	h.t.Helper()
	rows, err := h.st.FindByFile(context.Background(), id)
	if err != nil {
		h.t.Fatalf("FindByFile: %v", err)
	}
	return len(rows)
}

func (h *harness) deleteThumbnails(id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/thumbnail/"+id, nil))
	return rec
}

func TestDeleteThumbnails_ConcurrentReadyKeepsNewBlob(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "b-old", "o", "files", []byte("x"), "")
	var once sync.Once
	h.store.deleteErr = func(memKey) error {
		once.Do(func() {
			cur := h.pair(id, 1)
			next := db.Thumbnail{FileID: id, Version: 1, Size: "m", Kind: "video", OwnerID: "o", ServiceType: "files", BlobID: strp("b-new")}
			h.store.seed("b-new", 1, "files", "o", []byte("y"))
			if ok, err := h.st.PutReady(context.Background(), next, cur["m"]); err != nil || !ok {
				t.Errorf("concurrent PutReady: ok=%v err=%v", ok, err)
			}
		})
		return nil
	}
	if rec := h.deleteThumbnails(id); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
	row := h.pair(id, 1)["m"]
	if row == nil || row.BlobID == nil || *row.BlobID != "b-new" {
		t.Fatalf("the row written meanwhile must survive: %+v", row)
	}
	if !h.store.has("b-new", 1, "files", "o") {
		t.Errorf("the blob of the surviving row was deleted")
	}
}

func TestThumbnailGet_DigestChangeKeepsRowWhenOldBlobDeleteFails(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, testVersion, "m", "video", "b-old", testOwner, "files", []byte("x"), "d1")
	h.store.deleteErr = func(memKey) error { return fmt.Errorf("boom") }
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime, digest: "d2"}); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	row := h.pair(id, testVersion)["m"]
	if row.Status != db.StatusReady || row.BlobID == nil || *row.BlobID != "b-old" {
		t.Fatalf("a row whose old blob could not be deleted must stay: %+v", row)
	}
	if !h.store.has("b-old", testVersion, "files", testOwner) {
		t.Errorf("old blob must still exist")
	}
}

func TestThumbnailGet_ImageRegenKeepsRowWhenOldBlobDeleteFails(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, jpegOf(t, 4000, 3000))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg"}); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	before := h.pair(id, testVersion)
	puts := len(h.store.putKeys())
	h.store.deleteErr = func(memKey) error { return fmt.Errorf("boom") }
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", digest: "d2"}); rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d", rec.Code)
	}
	after := h.pair(id, testVersion)
	if len(before) != 1 || before["m"] == nil {
		t.Fatalf("rows = %+v, a request for m must store only m", before)
	}
	if *after["m"].BlobID != *before["m"].BlobID {
		t.Errorf("m: row swapped to %s although the old blob %s could not be deleted", *after["m"].BlobID, *before["m"].BlobID)
	}
	if n := len(h.store.putKeys()); n != puts {
		t.Errorf("uploaded %d blobs for rows that were not replaced", n-puts)
	}
}

func TestThumbnailGet_RegenLosingTheRaceDeletesOnlyItsOwnBlob(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, jpegOf(t, 4000, 3000))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg"}); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	var once sync.Once
	h.store.deleteErr = func(memKey) error {
		once.Do(func() {
			cur := h.pair(id, testVersion)
			next := db.Thumbnail{FileID: id, Version: testVersion, Size: "m", Kind: "image", OwnerID: testOwner, ServiceType: "files", BlobID: strp("b-other")}
			h.store.seed("b-other", testVersion, "files", testOwner, []byte("y"))
			if ok, err := h.st.PutReady(context.Background(), next, cur["m"]); err != nil || !ok {
				t.Errorf("concurrent PutReady: ok=%v err=%v", ok, err)
			}
		})
		return nil
	}
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", digest: "d2"}); rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d", rec.Code)
	}
	rows := h.pair(id, testVersion)
	for size, r := range rows {
		if r.BlobID != nil && !h.store.has(*r.BlobID, testVersion, "files", testOwner) {
			t.Errorf("%s: row points at the missing blob %s", size, *r.BlobID)
		}
	}
	if n := h.store.blobCount(); n != len(rows)+1 {
		t.Errorf("blobs = %d, want %d (rows plus the source): a blob no row references was left", n, len(rows)+1)
	}
}

func assertCachedWithoutRowThenRegenerates(t *testing.T, h *harness, req thumbReq) {
	t.Helper()
	if rec := h.get(req); rec.Code != http.StatusOK || rec.Body.String() != string(stubM.Body) {
		t.Fatalf("poll after the dropped row: %d %q, want 200 from the cache", rec.Code, rec.Body)
	}
	if n := h.rowCount(req.id); n != 0 {
		t.Fatalf("rows = %d, a cache hit must not create a row", n)
	}
	h.cancel()
	h.deps.Cache.Delete(h.thumbKeyOf(req, "m"))
	if rec := h.get(req); rec.Code != http.StatusAccepted {
		t.Fatalf("after the cache entry is gone: %d, want 202", rec.Code)
	}
	if r := h.pair(req.id, req.version)["m"]; r == nil || r.Status != db.StatusPending {
		t.Errorf("row = %+v, want a new PENDING row", r)
	}
}

func TestWorker_UploadFailureDropsTheRowAndLeavesOnlyTheSource(t *testing.T) {
	stubVideoPipeline(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("fake-video"))
	h.store.putErr = func(memKey) error { return fmt.Errorf("boom") }
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "row dropped and nothing uploaded", func() bool {
		return h.rowCount(id) == 0 && h.store.blobCount() == 1
	})
	assertCachedWithoutRowThenRegenerates(t, h, thumbReq{id: id, version: testVersion, mime: videoMime})
	if !h.store.has(id, testVersion, "files", testOwner) {
		t.Errorf("only the source blob must remain")
	}
}

func TestWorker_MarkReadyErrorDeletesBlobsAndDropsRows(t *testing.T) {
	stubVideoPipeline(t)
	h := newHarness(t, harnessOpts{})
	h.exec(`CREATE FUNCTION fail_ready() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'boom'; END $$ LANGUAGE plpgsql`)
	h.exec(`CREATE TRIGGER fail_ready BEFORE UPDATE ON thumbnail FOR EACH ROW WHEN (NEW.status = 'READY') EXECUTE FUNCTION fail_ready()`)
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("fake-video"))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "blob deleted and row dropped after the MarkReady error", func() bool {
		return len(h.store.putKeys()) == 1 && h.rowCount(id) == 0 && h.store.blobCount() == 1
	})
	assertCachedWithoutRowThenRegenerates(t, h, thumbReq{id: id, version: testVersion, mime: videoMime})
}

func TestLimits_DrainWaitsForFlightsAndBackgroundCleanup(t *testing.T) {
	lim := newLimits(0)
	c := cache.New(1 << 20)
	started, release := make(chan struct{}), make(chan struct{})
	callerCtx, leave := context.WithCancel(context.Background())
	go func() {
		_, _ = lim.serve(callerCtx, c, "op", "k", func(runner) (cache.Entry, error) {
			close(started)
			<-release
			return cache.Entry{Body: []byte("x")}, nil
		})
	}()
	<-started
	leave()

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if lim.drain(short) {
		t.Fatal("drain returned while a flight was still running")
	}
	close(release)
	long, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if !lim.drain(long) {
		t.Fatal("drain did not return after the flight ended")
	}
}

func TestStoreBlob_FailedUploadCleanupIsTrackedByDrain(t *testing.T) {
	lim := newLimits(0)
	store := newMemStore()
	store.putErr = func(memKey) error { return fmt.Errorf("boom") }
	store.deleteErr = func(memKey) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	if _, err := storeBlob(context.Background(), lim, store, []byte("x"), 1, "files", "o"); err == nil {
		t.Fatal("want the upload error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !lim.drain(ctx) {
		t.Fatal("drain timed out")
	}
	if n := len(store.deletedKeys()); n != 1 {
		t.Errorf("deletes after drain = %d, want 1", n)
	}
}

func TestThumbnailGet_UseOriginalNeedsAnUnrotatedJPEGPNGOrGIFLoader(t *testing.T) {
	src := []byte("source-bytes")
	cases := []struct {
		name     string
		info     render.Info
		original bool
	}{
		{"jpeg loader", render.Info{Width: 300, Height: 200, Pages: 1, Loader: "jpegload_buffer"}, true},
		{"heif behind a jpeg label", render.Info{Width: 300, Height: 200, Pages: 1, Loader: "heifload_buffer"}, false},
		{"unknown loader", render.Info{Width: 300, Height: 200, Pages: 1}, false},
		{"exif rotated jpeg", render.Info{Width: 300, Height: 200, Pages: 1, Loader: "jpegload_buffer", Orientation: 6}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			swap(t, &probeFunc, func([]byte) (render.Info, error) { return c.info, nil })
			swap(t, &imageThumbFunc, func(_ []byte, side int) (render.Rendition, error) { return stubFor(side), nil })
			h := newHarness(t, harnessOpts{noDB: true})
			id := newID()
			h.seedSource(id, src)
			rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", size: "s"})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if got := bytes.Equal(rec.Body.Bytes(), src); got != c.original {
				t.Errorf("served the source = %v, want %v", got, c.original)
			}
		})
	}
}

func TestThumbnailGet_InterimFrameOnlyForTheRequestedPendingSize(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, gifOf(t, 600, 400, 5))
	rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/gif", size: "s"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	rows := h.pair(id, testVersion)
	if len(rows) != 1 || rows["s"].Status != db.StatusPending {
		t.Fatalf("rows = %+v, want only s PENDING (m is not generated by an s request)", rows)
	}
	if _, ok := h.deps.Cache.Get(thumbKey("g", "files", testOwner, id, testVersion, "s", "")); !ok {
		t.Errorf("interim frame of the requested size is not cached")
	}
	if _, ok := h.deps.Cache.Get(thumbKey("g", "files", testOwner, id, testVersion, "m", "")); ok {
		t.Errorf("interim frame cached for a size that was not requested")
	}
	if m := h.get(thumbReq{id: id, version: testVersion, mime: "image/gif", size: "m"}); m.Code != http.StatusOK {
		t.Fatalf("m: %d, want the original GIF", m.Code)
	}
	if r := h.pair(id, testVersion)["m"]; r == nil || r.Status != db.StatusReady || !r.Original || r.BlobID != nil {
		t.Errorf("m row = %+v, want a READY original written by the m request", r)
	}
}

func TestThumbnailGet_OriginalRowsAreStreamedAndNeverCached(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	src := pngOf(t, 1000, 600, false)
	h.seedSource(id, src)
	req := thumbReq{id: id, version: testVersion, mime: "image/png", size: "m"}
	for i, label := range []string{"regenerated", "ready row"} {
		rec := h.get(req)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), src) || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%s: %d %q identical=%v", label, rec.Code, rec.Header().Get("Content-Type"), bytes.Equal(rec.Body.Bytes(), src))
		}
		if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(src)) {
			t.Errorf("%s: Content-Length %q, want %d", label, got, len(src))
		}
		if _, ok := h.deps.Cache.Get(h.thumbKeyOf(req, "m")); ok {
			t.Errorf("%s (request %d): the original source was cached", label, i)
		}
	}
}

func TestThumbnailGet_PDFRenderUnavailableIs503(t *testing.T) {
	swap(t, &pdfThumbFunc, func(context.Context, []byte, int) (render.Rendition, error) {
		return render.Rendition{}, render.ErrRenderUnavailable
	})
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("%PDF-fake"))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "application/pdf"}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET status %d, want 503 (body %s)", rec.Code, rec.Body)
	}
}

func TestThumbnailPost_PDFRenderUnavailableIs503(t *testing.T) {
	swap(t, &pdfThumbFunc, func(context.Context, []byte, int) (render.Rendition, error) {
		return render.Rendition{}, render.ErrRenderUnavailable
	})
	h := newHarness(t, harnessOpts{noDB: true})
	if rec := h.post("m", "application/pdf", []byte("%PDF-fake"), true); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("POST status %d, want 503 (body %s)", rec.Code, rec.Body)
	}
}

func TestThumbnailGet_SourceStorageErrorIs422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	h.store.getErr = func(memKey) error { return storage.ErrUnavailable }
	rec := h.get(thumbReq{id: newID(), version: testVersion, mime: "image/jpeg"})
	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}
