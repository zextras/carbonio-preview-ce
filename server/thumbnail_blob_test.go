// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

func sqlInt(t *testing.T, dsn, q string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func (h *harness) blobRows(status string) int {
	h.t.Helper()
	return sqlInt(h.t, h.dsn, `SELECT count(*) FROM thumbnail_blob WHERE status=$1`, status)
}

func (h *harness) blobStatus(blobID string) string {
	h.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, h.dsn)
	if err != nil {
		h.t.Fatal(err)
	}
	defer conn.Close(ctx)
	var s string
	if err := conn.QueryRow(ctx, `SELECT status FROM thumbnail_blob WHERE blob_id=$1`, blobID).Scan(&s); err != nil {
		return ""
	}
	return s
}

func (h *harness) purge() {
	h.t.Helper()
	if !h.worker.cleanupOnce(context.Background()) {
		h.t.Fatal("cleanup reported the DB not ready")
	}
}

func TestBlobCycle_ReservationBeforeTheUploadConsumedOnConfirm(t *testing.T) {
	stubImageRender(t)
	h := newHarness(t, harnessOpts{})
	started, release := h.blockUploads(t)
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))

	recv(t, h.getAsync(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s"}), "the response")
	recv(t, started, "the upload to start")
	if n := h.blobRows("PENDING"); n != 1 {
		t.Fatalf("reservations during the upload = %d, want 1 written before it", n)
	}
	if rows := h.pair(id, testVersion); len(rows) != 0 {
		t.Errorf("row published before the blob was stored: %+v", rows)
	}
	release()
	h.settle()
	r := h.pair(id, testVersion)[db.SizeS]
	if r == nil || r.Status != db.StatusReady || r.BlobID == nil {
		t.Fatalf("row = %+v", r)
	}
	if n := sqlInt(t, h.dsn, `SELECT count(*) FROM thumbnail_blob`); n != 0 {
		t.Errorf("thumbnail_blob rows after the confirm = %d, the reservation must be consumed", n)
	}
}

func TestBlobCycle_ReservationFailureUploadsNothing(t *testing.T) {
	t.Run("sync", func(t *testing.T) {
		stubImageRender(t)
		h := newHarness(t, harnessOpts{noWorker: true})
		h.exec(`DROP TABLE thumbnail_blob`)
		id := newID()
		h.seedSource(id, []byte("webp-bytes"))
		rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s"})
		if rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) {
			t.Fatalf("%d %q, the response is served from memory", rec.Code, rec.Body)
		}
		if n := len(h.store.putKeys()); n != 0 {
			t.Errorf("uploads = %d, want none without a reservation", n)
		}
		if rows := h.pair(id, testVersion); len(rows) != 0 {
			t.Errorf("rows = %+v", rows)
		}
	})
	t.Run("async", func(t *testing.T) {
		stubVideoPipeline(t)
		h := newHarness(t, harnessOpts{})
		h.exec(`DROP TABLE thumbnail_blob`)
		id := newID()
		h.store.seed(id, testVersion, "files", testOwner, []byte("fake-video"))
		if rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime, size: "s"}); rec.Code != http.StatusAccepted {
			t.Fatalf("miss: %d", rec.Code)
		}
		eventually(t, "row dropped as a connectivity failure", func() bool { return h.rowCount(id) == 0 })
		if n := len(h.store.putKeys()); n != 0 {
			t.Errorf("uploads = %d, want none", n)
		}
	})
}

func TestBlobCycle_ExpiredReservationBecomesATombstoneAndIsPurged(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	h.store.seed("crashed", testVersion, "files", testOwner, []byte("half"))
	h.store.seed("fresh", testVersion, "files", testOwner, []byte("inflight"))
	h.exec(`INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status, created_at, updated_at)
        VALUES ('crashed', $1, 'files', $2, 'PENDING', now() - interval '25 hours', now() - interval '25 hours'),
               ('fresh', $1, 'files', $2, 'PENDING', now() - interval '1 hour', now() - interval '1 hour')`, testOwner, testVersion)

	h.purge()
	if h.store.has("crashed", testVersion, "files", testOwner) || h.blobStatus("crashed") != "" {
		t.Errorf("expired reservation: blob present=%v status=%q, want deleted and forgotten", h.store.has("crashed", testVersion, "files", testOwner), h.blobStatus("crashed"))
	}
	if !h.store.has("fresh", testVersion, "files", testOwner) || h.blobStatus("fresh") != "PENDING" {
		t.Errorf("a reservation younger than 24 h must stay untouched")
	}
}

func TestBlobCycle_LostConfirmTombstonesTheNewBlobAndIsPurged(t *testing.T) {
	stubImageRender(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))
	var once sync.Once
	h.store.putErr = func(memKey) error {
		once.Do(func() {
			winner := db.Thumbnail{FileID: id, Version: testVersion, Size: "s", Kind: "image", OwnerID: testOwner, ServiceType: "files"}
			if ok, err := h.st.PutReady(context.Background(), winner, nil); err != nil || !ok {
				t.Errorf("competing PutReady: ok=%v err=%v", ok, err)
			}
		})
		return nil
	}
	h.get(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s"})
	if n := h.blobRows("TOMBSTONE"); n != 1 {
		t.Fatalf("tombstones = %d, the lost CAS must tombstone the new blob in the confirm tx", n)
	}
	if n := h.blobRows("PENDING"); n != 0 {
		t.Errorf("reservations left = %d", n)
	}
	h.purge()
	if n := h.store.blobCount(); n != 1 {
		t.Errorf("blobs after the purge = %d, only the source must remain", n)
	}
	if n := sqlInt(t, h.dsn, `SELECT count(*) FROM thumbnail_blob`); n != 0 {
		t.Errorf("tombstones left = %d", n)
	}
}

func TestBlobCycle_ReplacedBlobIsTombstonedInTheConfirmTxAndPurged(t *testing.T) {
	stubImageRender(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))
	h.seedReady(id, testVersion, "s", "image", "b-old", testOwner, "files", []byte("old"), "d1")

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s", digest: "d2"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	r := h.pair(id, testVersion)[db.SizeS]
	if r == nil || r.BlobID == nil || *r.BlobID == "b-old" || r.Digest == nil || *r.Digest != "d2" {
		t.Fatalf("row = %+v, want the new blob", r)
	}
	if h.blobStatus("b-old") != "TOMBSTONE" {
		t.Errorf("old blob status = %q, want TOMBSTONE written by the confirm tx", h.blobStatus("b-old"))
	}
	if !h.store.has("b-old", testVersion, "files", testOwner) {
		t.Errorf("the old blob is deleted only by the purge")
	}
	h.purge()
	if h.store.has("b-old", testVersion, "files", testOwner) || h.blobStatus("b-old") != "" {
		t.Errorf("the purge must delete the old blob and its tombstone")
	}
}

func TestBlobCycle_AsyncReplacementTombstonesTheOldBlob(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "b-old", "o", "files", []byte("old"), "d1")
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, digest: "d2", owner: "o"}); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	if h.blobStatus("b-old") != "TOMBSTONE" {
		t.Errorf("old blob status = %q, want TOMBSTONE written with the re-enqueue", h.blobStatus("b-old"))
	}
	if r := h.pair(id, 1)["m"]; r.Status != db.StatusPending || r.BlobID != nil {
		t.Errorf("row = %+v", r)
	}
}

func TestBlobCycle_DeleteWritesTombstonesAndDeletesBlobsAfterTheCommit(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "b-m", "o", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "b-s", "o", "files", []byte("S"), "")
	h.store.deleteErr = func(k memKey) error {
		if k.Node == "b-s" {
			return fmt.Errorf("%w: down", storage.ErrUnavailable)
		}
		return nil
	}
	if rec := h.deleteThumbnails(id); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if n := h.rowCount(id); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	if h.store.has("b-m", 1, "files", "o") || h.blobStatus("b-m") != "" {
		t.Errorf("b-m must be deleted inline and its tombstone dropped")
	}
	if !h.store.has("b-s", 1, "files", "o") || h.blobStatus("b-s") != "TOMBSTONE" {
		t.Errorf("b-s: a failed inline delete leaves the tombstone for the purge (status %q)", h.blobStatus("b-s"))
	}
	h.store.mu.Lock()
	h.store.deleteErr = nil
	h.store.mu.Unlock()
	h.purge()
	if h.store.has("b-s", 1, "files", "o") || h.blobStatus("b-s") != "" {
		t.Errorf("the purge must finish the delete")
	}
}

func TestBlobCycle_SweepWritesTombstonesWithTheRowDelete(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "image", "b-old", "o", "files", []byte("M"), "")
	h.age(id, 31*24*time.Hour)
	h.store.deleteErr = func(memKey) error { return fmt.Errorf("%w: down", storage.ErrUnavailable) }
	h.purge()
	if n := h.rowCount(id); n != 0 {
		t.Fatalf("rows = %d, the expired row must go", n)
	}
	if h.blobStatus("b-old") != "TOMBSTONE" || !h.store.has("b-old", 1, "files", "o") {
		t.Errorf("a failed inline delete must leave a tombstone (status %q)", h.blobStatus("b-old"))
	}
	h.store.mu.Lock()
	h.store.deleteErr = nil
	h.store.mu.Unlock()
	h.purge()
	if h.store.has("b-old", 1, "files", "o") || h.blobStatus("b-old") != "" {
		t.Errorf("the next purge must delete the blob and the tombstone")
	}
}

func TestBlobCycle_PurgeCountsOnlyRealStorageFailures(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	h.store.seed("b-x", 1, "files", "o", []byte("x"))
	h.exec(`INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status) VALUES ('b-x', 'o', 'files', 1, 'TOMBSTONE')`)
	attempts := func() int { return sqlInt(t, h.dsn, `SELECT attempts FROM thumbnail_blob WHERE blob_id='b-x'`) }

	for _, outage := range []error{
		fmt.Errorf("%w: dial tcp: refused", storage.ErrUnavailable),
		context.DeadlineExceeded,
		context.Canceled,
	} {
		h.store.mu.Lock()
		h.store.deleteErr = func(memKey) error { return outage }
		h.store.mu.Unlock()
		h.purge()
	}
	if n := attempts(); n != 0 {
		t.Fatalf("attempts after outages = %d, outages never count", n)
	}

	var calls int
	h.store.mu.Lock()
	h.store.deleteErr = func(memKey) error {
		calls++
		return fmt.Errorf("%w: %w: storage delete returned HTTP 500", storage.ErrUnavailable, storage.ErrRejected)
	}
	h.store.mu.Unlock()
	for i := 0; i < 5; i++ {
		h.purge()
	}
	if n := attempts(); n != 3 || calls != 3 {
		t.Errorf("attempts=%d calls=%d, want the tombstone kept and retried exactly 3 times", n, calls)
	}
	if h.blobStatus("b-x") != "TOMBSTONE" {
		t.Errorf("the exhausted tombstone must be kept")
	}
}

func TestBlobCycle_PurgeTreatsNotFoundAsDone(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	h.exec(`INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status) VALUES ('gone', 'o', 'files', 1, 'TOMBSTONE')`)
	h.purge()
	if h.blobStatus("gone") != "" {
		t.Errorf("a tombstone whose blob is already gone must be dropped")
	}
}

func TestBlobCycle_AbandonedUploadTombstonesItsReservation(t *testing.T) {
	stubImageRender(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	h.store.putErr = func(memKey) error { return fmt.Errorf("boom") }
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))
	h.get(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s"})
	if h.blobRows("TOMBSTONE") != 1 || h.blobRows("PENDING") != 0 {
		t.Errorf("tombstones=%d pending=%d, a failed upload turns its reservation into a tombstone", h.blobRows("TOMBSTONE"), h.blobRows("PENDING"))
	}
}

func TestDBLess_SyncKindsServedAndCachedWithoutUploadsOrRows(t *testing.T) {
	for _, k := range syncKinds {
		for name, mk := range map[string]func(*testing.T) *harness{
			"not_configured": func(t *testing.T) *harness { return newHarness(t, harnessOpts{noDB: true}) },
			"unreachable": func(t *testing.T) *harness {
				h := newHarness(t, harnessOpts{noWorker: true})
				h.st.Close()
				return h
			},
		} {
			t.Run(k.name+"_"+name, func(t *testing.T) {
				log := stubImageRender(t)
				h := mk(t)
				id := newID()
				h.seedSource(id, []byte(k.source))
				req := thumbReq{id: id, version: testVersion, mime: k.mime, size: "s"}
				for i := 0; i < 2; i++ {
					if rec := h.get(req); rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) {
						t.Fatalf("%d %q", rec.Code, rec.Body)
					}
				}
				if n := len(log.get()); n != 1 {
					t.Errorf("renders = %d, the second request must come from the memory cache", n)
				}
				if n := len(h.store.putKeys()); n != 0 {
					t.Errorf("uploads = %d, want none", n)
				}
			})
		}
	}
}

func TestDBLess_AsyncKindsAreGeneratedInTheRequest(t *testing.T) {
	cases := []struct {
		name  string
		mime  string
		setup func(*testing.T)
		src   []byte
	}{
		{"office", docxMime, func(t *testing.T) {
			stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
			stubImageRender(t)
		}, []byte("docx")},
		{"video", videoMime, func(t *testing.T) { stubVideoPipeline(t); stubImageRender(t) }, []byte("fake-video")},
	}
	for _, c := range cases {
		for name, mk := range map[string]func(*testing.T) *harness{
			"not_configured": func(t *testing.T) *harness { return newHarness(t, harnessOpts{noDB: true}) },
			"unreachable": func(t *testing.T) *harness {
				h := newHarness(t, harnessOpts{noWorker: true})
				h.st.Close()
				return h
			},
		} {
			t.Run(c.name+"_"+name, func(t *testing.T) {
				c.setup(t)
				h := mk(t)
				id := newID()
				h.store.seed(id, testVersion, "files", testOwner, c.src)
				req := thumbReq{id: id, version: testVersion, mime: c.mime, size: "s"}
				for i := 0; i < 2; i++ {
					rec := h.get(req)
					if rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) || rec.Header().Get("Retry-After") != "" {
						t.Fatalf("%d %q, want a synchronous 200 (no 202)", rec.Code, rec.Body)
					}
				}
				if n := len(h.store.putKeys()); n != 0 {
					t.Errorf("uploads = %d, want none", n)
				}
				if n := len(h.deps.AsyncSem); n != 0 {
					t.Errorf("async slot leaked: %d", n)
				}
			})
		}
	}
}

func TestDBLess_AnimatedGIFIsResizedInTheRequest(t *testing.T) {
	realVips(t)
	var mu sync.Mutex
	var sides []int
	swap(t, &animatedGIFFunc, func(_ context.Context, _ string, _ render.Info, side int, _ string) ([]byte, error) {
		mu.Lock()
		sides = append(sides, side)
		mu.Unlock()
		return []byte("GIF89a-small"), nil
	})
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, gifOf(t, 1600, 900, 4))
	rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"})
	if rec.Code != http.StatusOK || rec.Body.String() != "GIF89a-small" || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("%d %q, want a synchronous 200 GIF", rec.Code, rec.Body)
	}
	if again := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"}); again.Code != http.StatusOK {
		t.Fatalf("second: %d", again.Code)
	}
	if len(sides) != 1 || sides[0] != 320 {
		t.Errorf("ffmpeg runs = %v, want one S pass (second request from the cache)", sides)
	}
	if n := len(h.store.putKeys()); n != 0 {
		t.Errorf("uploads = %d", n)
	}
}

func TestDBLess_DBComingBackPersistsOnTheNextMiss(t *testing.T) {
	stubImageRender(t)
	dbStore := startDBStore(t)
	gate := newDBGate()
	h := gateHarness(t, gate)
	id := newID()
	h.seedSource(id, []byte("webp-bytes"))
	req := thumbReq{id: id, version: testVersion, mime: "image/webp", size: "s"}

	if rec := h.get(req); rec.Code != http.StatusOK {
		t.Fatalf("db-less: %d", rec.Code)
	}
	if n := len(h.store.putKeys()); n != 0 {
		t.Fatalf("uploads while db-less = %d", n)
	}
	gate.Set(dbStore)
	h.deps.Cache.Delete(h.thumbKeyOf(req, "s"))
	if rec := h.get(req); rec.Code != http.StatusOK {
		t.Fatalf("after the DB came back: %d", rec.Code)
	}
	rows, err := dbStore.FindPair(context.Background(), id, testVersion)
	if err != nil || rows[db.SizeS] == nil || rows[db.SizeS].Status != db.StatusReady {
		t.Errorf("rows = %+v err=%v, want the thumbnail persisted", rows, err)
	}
}

func (h *harness) getCtx(ctx context.Context, r thumbReq) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodGet, r.path(), nil).WithContext(ctx)
	req.Header.Set("fileownerid", testOwner)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func TestDBLess_CallerLeavingWhileWaitingForTheSlotDownloadsNothing(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	stubImageRender(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("docx"))
	for i := 0; i < cap(h.deps.AsyncSem); i++ {
		h.deps.AsyncSem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- h.getCtx(ctx, thumbReq{id: id, version: testVersion, mime: docxMime, size: "s"}) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	rec := recv(t, done, "the request to give up")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 when the caller leaves while waiting for the slot", rec.Code)
	}
	if n := h.store.getCount(); n != 0 {
		t.Errorf("source downloads = %d, nothing is downloaded before the slot is held", n)
	}
	if n := len(h.deps.AsyncSem); n != cap(h.deps.AsyncSem) {
		t.Errorf("slots held = %d, want only the test's %d", n, cap(h.deps.AsyncSem))
	}
}

func TestDBLess_ErrorPathsReleaseTheSlot(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return nil, errors.New("collabora returned status 400") })
	stubImageRender(t)
	h := newHarness(t, harnessOpts{noDB: true})
	missing := newID()
	if rec := h.get(thumbReq{id: missing, version: testVersion, mime: docxMime}); rec.Code != http.StatusNotFound {
		t.Errorf("missing source: %d, want 404", rec.Code)
	}
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("docx"))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("failed conversion: %d, want 422", rec.Code)
	}
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("slots leaked = %d", n)
	}
}

func TestDBLess_InlineGenerationsAreBoundedByTheAsyncSlots(t *testing.T) {
	const slots, requests = 2, 6
	var mu sync.Mutex
	var cur, peak int
	release := make(chan struct{})
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) {
		mu.Lock()
		cur++
		peak = max(peak, cur)
		mu.Unlock()
		<-release
		mu.Lock()
		cur--
		mu.Unlock()
		return []byte("png"), nil
	})
	stubImageRender(t)
	h := newHarness(t, harnessOpts{noDB: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.AsyncSem = make(chan struct{}, slots)
		return d
	}})
	var done []<-chan *httptest.ResponseRecorder
	for i := 0; i < requests; i++ {
		id := newID()
		h.store.seed(id, testVersion, "files", testOwner, []byte("docx"))
		done = append(done, h.getAsync(thumbReq{id: id, version: testVersion, mime: docxMime, size: "s"}))
	}
	eventually(t, "the slots are full", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return cur == slots
	})
	time.Sleep(200 * time.Millisecond)
	if n := h.store.getCount(); n != slots {
		t.Errorf("source downloads while the slots are held = %d, want %d (a download needs a slot)", n, slots)
	}
	close(release)
	for _, d := range done {
		if rec := recv(t, d, "a request"); rec.Code != http.StatusOK {
			t.Errorf("status %d", rec.Code)
		}
	}
	if peak != slots {
		t.Errorf("peak concurrent generations = %d, want %d", peak, slots)
	}
}

func TestBlobCycle_PurgeSurvivesATombstoneRemovedMeanwhile(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	h.store.seed("b-a", 1, "files", "o", []byte("a"))
	h.store.seed("b-b", 1, "files", "o", []byte("b"))
	h.exec(`INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status, created_at) VALUES
        ('b-a', 'o', 'files', 1, 'TOMBSTONE', now() - interval '2 hours'),
        ('b-b', 'o', 'files', 1, 'TOMBSTONE', now() - interval '1 hour')`)
	h.store.deleteErr = func(k memKey) error {
		if k.Node == "b-a" {
			h.exec(`DELETE FROM thumbnail_blob WHERE blob_id='b-a'`)
			return fmt.Errorf("%w: %w: HTTP 400", storage.ErrUnavailable, storage.ErrRejected)
		}
		return nil
	}
	h.purge()
	if h.blobStatus("b-b") != "" || h.store.has("b-b", 1, "files", "o") {
		t.Errorf("the run must go on after a tombstone vanished: b-b status %q", h.blobStatus("b-b"))
	}
}

func TestDBLess_DBDroppingBetweenReadAndEnqueueFallsBack(t *testing.T) {
	t.Run("animated_gif", func(t *testing.T) {
		swap(t, &animatedGIFFunc, func(context.Context, string, render.Info, int, string) ([]byte, error) {
			return []byte("GIF89a-small"), nil
		})
		h, id := gifHarness(t, harnessOpts{noWorker: true})
		h.seedSource(id, gifOf(t, 1600, 900, 4))
		h.store.onGet = func(memKey) { h.st.Close() }
		rec := h.get(thumbReq{id: id, version: testVersion, mime: gifMime, size: "s"})
		if rec.Code != http.StatusOK || rec.Body.String() != "GIF89a-small" {
			t.Fatalf("status %d %q, want the request generation instead of 424", rec.Code, rec.Body)
		}
	})
	t.Run("video", func(t *testing.T) {
		stubVideoPipeline(t)
		h := newHarness(t, harnessOpts{noWorker: true})
		id := newID()
		h.store.seed(id, testVersion, "files", testOwner, []byte("fake-video"))
		h.st.Close()
		p, _ := pipelineFor(videoMime)
		q := thumbRequest{p: p, id: id, version: testVersion, size: "s", serviceType: "files", owner: testOwner}
		step := h.deps.slotLimits().forCaller(context.Background(), "t")
		e, err := regenerate(step, h.deps, h.worker, h.st, q, nil)
		if err != nil || string(e.Body) != string(stubS.Body) {
			t.Fatalf("regenerate with a dropped DB: %v %q, want the request generation instead of 424", err, e.Body)
		}
	})
}
