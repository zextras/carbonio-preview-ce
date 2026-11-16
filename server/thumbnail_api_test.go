// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
)

const (
	testVersion = 7
	testOwner   = "owner-a"
	videoMime   = "video/mp4"
	docxMime    = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
)

var (
	stubS = render.Rendition{Body: []byte("S-rendition"), ContentType: "image/jpeg", Width: 320, Height: 180}
	stubM = render.Rendition{Body: []byte("M-rendition"), ContentType: "image/png", Width: 1280, Height: 720}
)

type harness struct {
	t      *testing.T
	st     *db.Store
	dsn    string
	store  *memStore
	deps   Deps
	mux    *http.ServeMux
	worker *AsyncWorker
	cancel context.CancelFunc
}

type harnessOpts struct {
	noWorker bool
	noDB     bool
	cfg      func(*testing.T, Deps) Deps
}

func newHarness(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	h := &harness{t: t, store: newMemStore()}
	deps := Deps{
		Cfg:      testCfg(),
		Store:    h.store,
		Cache:    cache.New(1 << 24),
		Limits:   newLimits(4),
		AsyncSem: make(chan struct{}, 4),
	}
	if !o.noDB {
		h.st, h.dsn = startDBStoreWithDSN(t)
		deps.DB = h.st
	}
	if o.cfg != nil {
		deps = o.cfg(t, deps)
	}
	h.deps = deps
	h.mux = http.NewServeMux()
	h.worker = RegisterOperations(newHumaAPI(h.mux), deps)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	if !o.noWorker && !o.noDB {
		h.worker.Start(ctx)
	}
	t.Cleanup(func() {
		cancel()
		h.worker.Wait()
	})
	return h
}

func stubVideoPipeline(t *testing.T) {
	t.Helper()
	prevCodec, prevFrame, prevPair := videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc, imagePairFunc
	videoDetectCodecFromFileFunc = func(context.Context, string) (string, error) { return "h264", nil }
	videoFirstFrameFromFileFunc = func(context.Context, string) ([]byte, error) { return []byte("frame"), nil }
	imagePairFunc = func([]byte) (render.Pair, error) { return render.Pair{S: stubS, M: stubM}, nil }
	t.Cleanup(func() {
		videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc, imagePairFunc = prevCodec, prevFrame, prevPair
	})
}

type thumbReq struct {
	id, mime, digest, lang, serviceType, owner string
	version                                    int
	size                                       string
}

func (r thumbReq) path() string {
	if r.size == "" {
		r.size = "m"
	}
	if r.serviceType == "" {
		r.serviceType = "files"
	}
	q := url.Values{}
	q.Set("service_type", r.serviceType)
	if r.mime != "-" {
		q.Set("mime", r.mime)
	}
	if r.digest != "" {
		q.Set("digest", r.digest)
	}
	if r.lang != "" {
		q.Set("lang", r.lang)
	}
	return fmt.Sprintf("/thumbnail/%s/%d/%s?%s", r.id, r.version, r.size, q.Encode())
}

func (h *harness) get(r thumbReq) *httptest.ResponseRecorder {
	h.t.Helper()
	if r.owner == "" {
		r.owner = testOwner
	}
	req := httptest.NewRequest(http.MethodGet, r.path(), nil)
	req.Header.Set("fileownerid", r.owner)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *harness) pair(id string, version int) map[string]*db.Thumbnail {
	h.t.Helper()
	rows, err := h.st.FindPair(context.Background(), id, version)
	if err != nil {
		h.t.Fatalf("FindPair: %v", err)
	}
	return rows
}

func (h *harness) waitStatus(id string, version int, status string) map[string]*db.Thumbnail {
	h.t.Helper()
	var rows map[string]*db.Thumbnail
	eventually(h.t, "both sizes "+status, func() bool {
		rows = h.pair(id, version)
		return len(rows) == 2 && rows[db.SizeS].Status == status && rows[db.SizeM].Status == status
	})
	return rows
}

func (h *harness) seedReady(id string, version int, size, kind, blob, owner, serviceType string, data []byte, digest string) {
	h.t.Helper()
	row := db.Thumbnail{FileID: id, Version: version, Size: size, Kind: kind, OwnerID: owner, ServiceType: serviceType,
		BlobID: &blob, ContentType: strp("image/jpeg")}
	if digest != "" {
		row.Digest = &digest
	}
	if ok, err := h.st.PutReady(context.Background(), row, nil); err != nil || !ok {
		h.t.Fatalf("seed READY: ok=%v err=%v", ok, err)
	}
	h.store.seed(blob, version, serviceType, owner, data)
}

func (h *harness) age(id string, ago time.Duration) {
	h.t.Helper()
	h.exec(`UPDATE thumbnail SET last_accessed_at = now() - $2::interval WHERE file_id=$1`, id, fmt.Sprintf("%d seconds", int(ago.Seconds())))
}

func (h *harness) exec(q string, args ...any) {
	h.t.Helper()
	sqlExec(h.t, h.dsn, q, args...)
}

func strp(s string) *string { return &s }

func newID() string { return uuid.NewString() }

func TestThumbnailGet_UnsupportedMIME415(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	for _, mime := range []string{"application/octet-stream", "image/bmp", "garbage"} {
		rec := h.get(thumbReq{id: newID(), version: 1, mime: mime})
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("mime %q: status %d, want 415 (body %s)", mime, rec.Code, rec.Body)
		}
	}
}

func TestThumbnailGet_InvalidSize422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.get(thumbReq{id: newID(), version: 1, mime: videoMime, size: "l"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
}

func TestThumbnailGet_MissingMIME422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.get(thumbReq{id: newID(), version: 1, mime: "-"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
}

func TestThumbnailGet_NoDB_AsyncKind424(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	for _, mime := range []string{videoMime, docxMime} {
		rec := h.get(thumbReq{id: newID(), version: 1, mime: mime})
		if rec.Code != http.StatusFailedDependency {
			t.Errorf("mime %q: status %d, want 424", mime, rec.Code)
		}
	}
}

func TestThumbnailGet_OfficeDisabledByFlag400(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.Cfg.ServiceEnableDocumentThumbnail = false
		return d
	}})
	rec := h.get(thumbReq{id: newID(), version: 1, mime: docxMime})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestThumbnailGet_VideoMissEnqueuesBothSizes202(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime, size: "s", serviceType: "chats", owner: "owner-x", lang: "it-IT", digest: "dg1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202 (%s)", rec.Code, rec.Body)
	}
	rows := h.pair(id, testVersion)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want both sizes", len(rows))
	}
	for size, r := range rows {
		if r.Status != db.StatusPending || r.Kind != "video" || r.OwnerID != "owner-x" || r.ServiceType != "chats" ||
			r.Digest == nil || *r.Digest != "dg1" || r.Lang == nil || *r.Lang != "it-IT" {
			t.Errorf("size %s row = %+v", size, r)
		}
	}
}

func TestThumbnailGet_202Headers(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	for _, mime := range []string{videoMime, docxMime} {
		rec := h.get(thumbReq{id: newID(), version: 1, mime: mime})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s: status %d, want 202", mime, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", mime, got)
		}
		if got := rec.Header().Get("Retry-After"); got != "3" {
			t.Errorf("%s: Retry-After = %q", mime, got)
		}
		if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
			t.Errorf("%s: 202 must have no body and no Content-Type: %q %q", mime, rec.Body, rec.Header().Get("Content-Type"))
		}
	}
}

func TestThumbnailGet_PendingAndGeneratingAnswer202(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	req := thumbReq{id: id, version: 1, mime: videoMime}
	if rec := h.get(req); rec.Code != http.StatusAccepted {
		t.Fatalf("first: %d", rec.Code)
	}
	if _, err := h.st.Claim(context.Background(), id, 1, "inst"); err != nil {
		t.Fatal(err)
	}
	rec := h.get(req)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Retry-After") != "3" {
		t.Fatalf("generating: %d %v", rec.Code, rec.Header())
	}
}

func TestThumbnailGet_ReadyServesStoredRendition(t *testing.T) {
	stubVideoPipeline(t)
	h := newHarness(t, harnessOpts{})
	src := newID()
	h.store.seed(src, testVersion, "files", "owner-a", []byte("fake-video"))

	if rec := h.get(thumbReq{id: src, version: testVersion, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	rows := h.waitStatus(src, testVersion, db.StatusReady)

	for size, want := range map[string]render.Rendition{"s": stubS, "m": stubM} {
		rec := h.get(thumbReq{id: src, version: testVersion, mime: videoMime, size: size})
		if rec.Code != http.StatusOK {
			t.Fatalf("size %s: %d", size, rec.Code)
		}
		if rec.Body.String() != string(want.Body) || rec.Header().Get("Content-Type") != want.ContentType {
			t.Errorf("size %s: %q %q", size, rec.Body, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Cache-Control") != "" {
			t.Errorf("200 must carry no cache headers: %v", rec.Header())
		}
		r := rows[size]
		if r.BlobID == nil || r.OwnerID != "owner-a" || r.ServiceType != "files" || *r.ContentType != want.ContentType {
			t.Errorf("row %s = %+v", size, r)
		}
		if !h.store.has(*r.BlobID, testVersion, "files", "owner-a") {
			t.Errorf("blob %s not stored under the request owner", *r.BlobID)
		}
	}
	for _, k := range h.store.putKeys() {
		if k.Version != testVersion || k.ServiceType != "files" || k.Owner != "owner-a" {
			t.Errorf("blob written with %+v, want the source version and request owner", k)
		}
	}
	if n := len(h.store.putKeys()); n != 2 {
		t.Errorf("blobs written = %d, want 2", n)
	}
}

func TestThumbnailGet_ReadyTakesNoRenderSlot(t *testing.T) {
	h := newHarness(t, harnessOpts{cfg: func(_ *testing.T, d Deps) Deps {
		d.Limits = newLimits(1)
		return d
	}})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M-bytes"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S-bytes"), "")
	h.deps.Limits.render <- struct{}{}
	defer func() { <-h.deps.Limits.render }()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- h.get(thumbReq{id: id, version: 1, mime: videoMime}) }()
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK || rec.Body.String() != "M-bytes" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("READY read waited for a render slot")
	}
}

func TestThumbnailGet_ReadyBlobMissingReenqueuesThatSize(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "")
	h.store.mu.Lock()
	delete(h.store.blobs, memKey{"blob-m", 1, "files", "o"})
	h.store.mu.Unlock()

	rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, size: "m"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	rows := h.pair(id, 1)
	if rows["m"].Status != db.StatusPending || rows["m"].BlobID != nil {
		t.Errorf("m = %+v, want PENDING without blob", rows["m"])
	}
	if rows["s"].Status != db.StatusReady {
		t.Errorf("s must be untouched: %+v", rows["s"])
	}
}

func TestThumbnailGet_ReadyBlobStorageError502(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "")
	h.store.getErr = func(memKey) error { return fmt.Errorf("storage down") }
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime}); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
}

func TestThumbnailGet_ReadReadsBlobWithRowOwnerNotRequestOwner(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "row-owner", "chats", []byte("M-bytes"), "")
	rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, owner: "request-owner", serviceType: "files"})
	if rec.Code != http.StatusOK || rec.Body.String() != "M-bytes" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestThumbnailGet_LegacyRowRegeneratesAndDeletesFrameBlob(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.exec(`INSERT INTO thumbnail (file_id, version, size, status, kind, blob_id, owner_id, service_type, attempts, created_at, updated_at, last_accessed_at)
        VALUES ($1, 1, 'm', 'LEGACY', 'video', 'legacy-frame', 'legacy-owner', 'chats', 0, now(), now(), now())`, id)
	h.store.seed("legacy-frame", 1, "chats", "legacy-owner", []byte("big-frame"))

	rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, owner: "request-owner"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if h.store.has("legacy-frame", 1, "chats", "legacy-owner") {
		t.Errorf("legacy frame blob must be deleted at once, using the row's owner and service type")
	}
	rows := h.pair(id, 1)
	if len(rows) != 2 || rows["m"].Status != db.StatusPending || rows["m"].BlobID != nil || rows["m"].OwnerID != "request-owner" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestThumbnailGet_DigestMismatchReenqueuesAndDeletesOldBlob(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "row-owner", "files", []byte("M"), "d1")
	h.seedReady(id, 1, "s", "video", "blob-s", "row-owner", "files", []byte("S"), "d1")

	rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, digest: "d2", owner: "request-owner"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if h.store.has("blob-m", 1, "files", "row-owner") || h.store.has("blob-s", 1, "files", "row-owner") {
		t.Errorf("old blobs must be deleted at once")
	}
	for size, r := range h.pair(id, 1) {
		if r.Status != db.StatusPending || r.Digest == nil || *r.Digest != "d2" || r.BlobID != nil {
			t.Errorf("size %s = %+v", size, r)
		}
	}
	for _, k := range h.store.deletedKeys() {
		if k.Owner != "row-owner" {
			t.Errorf("blob deleted with owner %q, want the row's", k.Owner)
		}
	}
}

func TestThumbnailGet_SameDigestAndNoDigestNeverRegenerate(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "d1")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "d1")
	for _, d := range []string{"", "d1"} {
		rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, digest: d})
		if rec.Code != http.StatusOK {
			t.Fatalf("digest %q: %d", d, rec.Code)
		}
	}
	if n := len(h.store.deletedKeys()); n != 0 {
		t.Errorf("deletes = %d, want 0", n)
	}
}

func TestThumbnailGet_NoDigestNeverRegenerates(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "stored-digest")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "stored-digest")
	rec := h.get(thumbReq{id: id, version: 1, mime: videoMime})
	if rec.Code != http.StatusOK || rec.Body.String() != "M" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if n := len(h.store.deletedKeys()); n != 0 {
		t.Errorf("deletes = %d, want 0", n)
	}
}

func TestThumbnailGet_StoredNullDigestMismatchesProvidedOne(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "")
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, digest: "d1"}); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
}

func TestThumbnailGet_DigestOnPendingRowAnswers202(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, digest: "d1"}); rec.Code != http.StatusAccepted {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, digest: "d2"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	for size, r := range h.pair(id, 1) {
		if *r.Digest != "d1" {
			t.Errorf("size %s digest = %q, a PENDING row keeps its digest", size, *r.Digest)
		}
	}
}

func TestThumbnailGet_FailedAnswers422AndUnsupported415(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatal(rec.Code)
	}
	h.exec(`UPDATE thumbnail SET status='FAILED' WHERE file_id=$1`, id)
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("FAILED: %d, want 422", rec.Code)
	}
	h.exec(`UPDATE thumbnail SET status='UNSUPPORTED', codec='hevc-unknown' WHERE file_id=$1`, id)
	if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime}); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("UNSUPPORTED: %d, want 415", rec.Code)
	}
}

func TestThumbnailGet_CacheHitSkipsDBAndStorage(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "")
	req := thumbReq{id: id, version: 1, mime: videoMime}
	if rec := h.get(req); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	gets := h.store.getCount()
	h.exec(`DELETE FROM thumbnail WHERE file_id=$1`, id)
	rec := h.get(req)
	if rec.Code != http.StatusOK || rec.Body.String() != "M" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if h.store.getCount() != gets {
		t.Errorf("a cache hit must not read storage")
	}
}

func TestThumbnailGet_CacheKeysAreScopedByOwnerAndServiceType(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "row-owner", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "row-owner", "files", []byte("S"), "")
	read := func(owner, serviceType string) {
		t.Helper()
		if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime, owner: owner, serviceType: serviceType}); rec.Code != http.StatusOK {
			t.Fatalf("%s/%s: %d", owner, serviceType, rec.Code)
		}
	}
	read("owner-a", "files")
	read("owner-a", "files")
	if n := h.store.getCount(); n != 1 {
		t.Fatalf("same caller: reads = %d, want 1", n)
	}
	read("owner-b", "files")
	if n := h.store.getCount(); n != 2 {
		t.Fatalf("other owner must miss the cache: reads = %d, want 2", n)
	}
	read("owner-a", "chats")
	if n := h.store.getCount(); n != 3 {
		t.Fatalf("other service type must miss the cache: reads = %d, want 3", n)
	}
	read("owner-b", "files")
	if n := h.store.getCount(); n != 3 {
		t.Fatalf("repeat must hit: reads = %d, want 3", n)
	}
}

func TestThumbnailGet_CacheHitTouchesAtMostDaily(t *testing.T) {
	prev := touchEvery
	touchEvery = 400 * time.Millisecond
	t.Cleanup(func() { touchEvery = prev })

	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "")
	req := thumbReq{id: id, version: 1, mime: videoMime}

	age := func() { h.age(id, 48*time.Hour) }
	touched := func() bool {
		rows, err := h.st.FindByFile(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Size == "m" {
				return time.Since(r.LastAccessedAt) < time.Hour
			}
		}
		return false
	}
	hits := func(n int) {
		for i := 0; i < n; i++ {
			if rec := h.get(req); rec.Code != http.StatusOK {
				t.Fatal(rec.Code)
			}
		}
	}

	age()
	hits(1)
	age()
	hits(20)
	time.Sleep(100 * time.Millisecond)
	if touched() {
		t.Fatalf("hits inside the interval must not touch the row")
	}

	time.Sleep(touchEvery)
	hits(20)
	eventually(t, "one touch after the interval", touched)

	age()
	hits(20)
	time.Sleep(100 * time.Millisecond)
	if touched() {
		t.Fatalf("a second touch inside the new interval must not happen")
	}
}

func TestThumbnailGet_ReadyTouchesOnlyWhenOlderThanOneDay(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.Cache = nil
		return d
	}})
	id := newID()
	h.seedReady(id, 1, "m", "video", "blob-m", "o", "files", []byte("M"), "")
	h.seedReady(id, 1, "s", "video", "blob-s", "o", "files", []byte("S"), "")
	read := func() { h.get(thumbReq{id: id, version: 1, mime: videoMime}) }
	last := func() time.Time {
		rows, _ := h.st.FindByFile(context.Background(), id)
		for _, r := range rows {
			if r.Size == "m" {
				return r.LastAccessedAt
			}
		}
		return time.Time{}
	}

	h.age(id, time.Hour)
	before := last()
	read()
	if !last().Equal(before) {
		t.Errorf("an access within a day must not write")
	}
	h.age(id, 2*24*time.Hour)
	read()
	if time.Since(last()) > time.Minute {
		t.Errorf("an access after a day must refresh last_accessed_at")
	}
}

func TestDeleteThumbnails_AllVersionsAndSizes(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id, other := newID(), newID()
	h.seedReady(id, 1, "s", "video", "b1s", "owner-1", "files", []byte("x"), "")
	h.seedReady(id, 1, "m", "video", "b1m", "owner-1", "files", []byte("x"), "")
	h.seedReady(id, 2, "s", "office", "b2s", "owner-2", "chats", []byte("x"), "")
	h.exec(`INSERT INTO thumbnail (file_id, version, size, status, kind, original, content_type, owner_id, service_type, attempts, created_at, updated_at, last_accessed_at)
        VALUES ($1, 2, 'm', 'READY', 'image', true, 'image/jpeg', 'owner-2', 'chats', 0, now(), now(), now())`, id)
	h.seedReady(other, 1, "m", "video", "keep", "o", "files", []byte("x"), "")

	req := httptest.NewRequest(http.MethodDelete, "/thumbnail/"+id, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
	if rows, _ := h.st.FindByFile(context.Background(), id); len(rows) != 0 {
		t.Errorf("rows left: %+v", rows)
	}
	if rows, _ := h.st.FindByFile(context.Background(), other); len(rows) != 1 {
		t.Errorf("another file's rows must be kept")
	}
	deleted := map[memKey]bool{}
	for _, k := range h.store.deletedKeys() {
		deleted[k] = true
	}
	want := []memKey{{"b1s", 1, "files", "owner-1"}, {"b1m", 1, "files", "owner-1"}, {"b2s", 2, "chats", "owner-2"}}
	for _, k := range want {
		if !deleted[k] {
			t.Errorf("blob %+v not deleted with the row's owner and service type", k)
		}
	}
	if len(deleted) != len(want) {
		t.Errorf("deleted %v, want exactly %v (original rows have no blob)", deleted, want)
	}
	if !h.store.has("keep", 1, "files", "o") {
		t.Errorf("another file's blob must be kept")
	}
}

func TestDeleteThumbnails_StorageErrorIsSwallowed(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedReady(id, 1, "m", "video", "b", "o", "files", []byte("x"), "")
	h.store.deleteErr = func(memKey) error { return fmt.Errorf("boom") }
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/thumbnail/"+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
	if rows, _ := h.st.FindByFile(context.Background(), id); len(rows) != 0 {
		t.Errorf("rows must be deleted even when a blob delete fails")
	}
}

func TestDeleteThumbnails_NoDB_NoOp204(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/thumbnail/"+newID(), nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
}

func TestDeleteThumbnails_InvalidID422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/thumbnail/not-a-uuid", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
}

func TestDeleteThumbnails_RunningJobDeletesItsBlobs(t *testing.T) {
	stubVideoPipeline(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("fake-video"))

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	prev := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(ctx context.Context, p string) ([]byte, error) {
		entered <- struct{}{}
		<-release
		return prev(ctx, p)
	}
	t.Cleanup(func() { videoFirstFrameFromFileFunc = prev })

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("job did not start")
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/thumbnail/"+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	close(release)

	eventually(t, "the job deleted the blobs it uploaded", func() bool {
		return len(h.store.putKeys()) == 2 && h.store.blobCount() == 1
	})
	if rows, _ := h.st.FindByFile(context.Background(), id); len(rows) != 0 {
		t.Errorf("a deleted file must stay without rows: %+v", rows)
	}
}

func TestRoutes_VideoAndCopyAre404(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/preview/video/" + id + "/1/100x100/?service_type=files"},
		{http.MethodGet, "/preview/video/" + id + "/1/100x100/thumbnail/?service_type=files"},
		{http.MethodDelete, "/preview/video/" + id + "/1/?service_type=files"},
		{http.MethodPost, "/preview/video/" + id + "/1/copy/?service_type=files&target=" + newID()},
	} {
		rec := httptest.NewRecorder()
		h.mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c.method, c.path, rec.Code)
		}
	}
}

func TestThumbnailGet_ConcurrentMissesEnqueueOnce(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := h.get(thumbReq{id: id, version: 1, mime: videoMime}); rec.Code != http.StatusAccepted {
				t.Errorf("status %d", rec.Code)
			}
		}()
	}
	wg.Wait()
	if rows := h.pair(id, 1); len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
}
