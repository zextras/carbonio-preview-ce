// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

func sqlExec(t *testing.T, dsn, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func setAccessedAgo(t *testing.T, dsn, fileID string, d time.Duration) {
	t.Helper()
	sqlExec(t, dsn, `UPDATE thumbnail SET last_accessed_at = now() - $2::interval WHERE file_id=$1`,
		fileID, fmt.Sprintf("%d seconds", int(d.Seconds())))
}

type scriptedStore struct {
	mu          sync.Mutex
	retrieveErr error
	storeErr    error
	deleteErr   map[string]error
	deleted     []string
	onDelete    func(previewID string)
	body        string
}

func (s *scriptedStore) RetrieveData(context.Context, string, int, string, string) (storage.Blob, error) {
	return []byte("blob"), nil
}

func (s *scriptedStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, int64, error) {
	if s.retrieveErr != nil {
		return nil, 0, s.retrieveErr
	}
	return io.NopCloser(strings.NewReader(s.body)), int64(len(s.body)), nil
}

func (s *scriptedStore) StoreData(_ context.Context, nodeID string, _ int, _ string, _ string, _ []byte) (string, error) {
	if s.storeErr != nil {
		return "", s.storeErr
	}
	return nodeID, nil
}

func (s *scriptedStore) Delete(_ context.Context, id string, _ int, _ string, _ string) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, id)
	cb := s.onDelete
	err := s.deleteErr[id]
	s.mu.Unlock()
	if cb != nil {
		cb(id)
	}
	return err
}

func (s *scriptedStore) deletedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

func stubSupportedCodec(t *testing.T) {
	t.Helper()
	stubVideoPipeline(t)
	swap(t, &videoFirstFrameFromFileFunc, func(context.Context, string) ([]byte, error) { return tinyJPEG(t), nil })
}

func makeWorkerWithDB(dbStore *db.Store, storeClient storage.Client, maxAttempts int) *AsyncWorker {
	cfg := testCfg()
	cfg.AsyncMaxAttempts = maxAttempts
	return NewAsyncWorker(Deps{
		Cfg:      cfg,
		Store:    storeClient,
		Cache:    cache.New(0),
		DB:       dbStore,
		AsyncSem: nil,
	})
}

func seedPending(t *testing.T, st *db.Store, fileID string, version int) {
	t.Helper()
	enqueueKind(t, st, fileID, version, "video")
}

func enqueueKind(t *testing.T, st *db.Store, fileID string, version int, kind string) {
	t.Helper()
	row := db.Thumbnail{FileID: fileID, Version: version, Size: db.SizeM, Kind: kind, OwnerID: "owner1", ServiceType: "files"}
	if ok, err := st.Enqueue(context.Background(), row, nil); err != nil || !ok {
		t.Fatalf("Enqueue: ok=%v err=%v", ok, err)
	}
}

func claimOne(t *testing.T, st *db.Store, instanceID, fileID string, version int) db.Thumbnail {
	t.Helper()
	rows, err := st.Claim(context.Background(), fileID, version, db.SizeM, instanceID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Claim: rows=%d err=%v", len(rows), err)
	}
	return rows[0]
}

func claimedRow(t *testing.T, st *db.Store, w *AsyncWorker, fileID string, attempts int) db.Thumbnail {
	t.Helper()
	ctx := context.Background()
	seedPending(t, st, fileID, 1)
	for i := 0; i < attempts; i++ {
		claimOne(t, st, w.instanceID, fileID, 1)
		if err := st.ReleaseWithAttempt(ctx, fileID, 1, w.instanceID, db.AllSizes); err != nil {
			t.Fatal(err)
		}
	}
	return claimOne(t, st, w.instanceID, fileID, 1)
}

func pairOf(t *testing.T, st *db.Store, fileID string, version int) map[string]*db.Thumbnail {
	t.Helper()
	rows, err := st.FindPair(context.Background(), fileID, version)
	if err != nil {
		t.Fatalf("FindPair: %v", err)
	}
	return rows
}

func findRow(st *db.Store, fileID string, version int) *db.Thumbnail {
	rows, err := st.FindPair(context.Background(), fileID, version)
	if err != nil {
		return nil
	}
	return rows[db.SizeM]
}

func TestAttempt_FailureClassification(t *testing.T) {
	st := startDBStore(t)
	ctx := context.Background()
	stubVideoPipeline(t)

	type tc struct {
		name       string
		store      *scriptedStore
		probe      func(context.Context, string) (string, error)
		extract    func(context.Context, string) ([]byte, error)
		attempts   int
		wantRow    bool
		wantStatus string
		wantAtt    int
	}
	okProbe := func(context.Context, string) (string, error) { return "h264", nil }
	okExtract := func(context.Context, string) ([]byte, error) { return tinyJPEG(t), nil }
	cases := []tc{
		{name: "download_unavailable", store: &scriptedStore{retrieveErr: fmt.Errorf("%w: boom", storage.ErrUnavailable)}, probe: okProbe, extract: okExtract, attempts: 2},
		{name: "owner_resolution", store: &scriptedStore{retrieveErr: fmt.Errorf("owner ip: %w", storage.ErrUnavailable)}, probe: okProbe, extract: okExtract},
		{name: "upload_fails", store: &scriptedStore{body: "x", storeErr: errors.New("upload down")}, probe: okProbe, extract: okExtract, attempts: 2},
		{name: "source_404", store: &scriptedStore{retrieveErr: storage.ErrNotFound}, probe: okProbe, extract: okExtract, wantRow: true, wantStatus: db.StatusFailed, wantAtt: 0},
		{name: "probe_fails_below_cap", store: &scriptedStore{body: "x"}, probe: func(context.Context, string) (string, error) { return "", errors.New("bad") }, extract: okExtract, wantRow: true, wantStatus: db.StatusPending, wantAtt: 1},
		{name: "extract_fails_below_cap", store: &scriptedStore{body: "x"}, probe: okProbe, extract: func(context.Context, string) ([]byte, error) { return nil, errors.New("bad") }, wantRow: true, wantStatus: db.StatusPending, wantAtt: 1},
		{name: "extract_fails_at_cap", store: &scriptedStore{body: "x"}, probe: okProbe, extract: func(context.Context, string) ([]byte, error) { return nil, errors.New("bad") }, attempts: 2, wantRow: true, wantStatus: db.StatusFailed, wantAtt: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			origProbe, origExtract := videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc
			videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc = c.probe, c.extract
			t.Cleanup(func() { videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc = origProbe, origExtract })

			w := makeWorkerWithDB(st, c.store, 3)
			id := testUID(t, "cls")
			row := claimedRow(t, st, w, id, c.attempts)
			w.attempt(ctx, row)

			after := findRow(st, id, 1)
			if !c.wantRow {
				if after != nil {
					t.Fatalf("row must be deleted, got status %q attempts %d", after.Status, after.Attempts)
				}
				return
			}
			if after == nil {
				t.Fatal("row must be kept")
			}
			if after.Status != c.wantStatus || after.Attempts != c.wantAtt {
				t.Errorf("got status=%s attempts=%d, want %s/%d", after.Status, after.Attempts, c.wantStatus, c.wantAtt)
			}
		})
	}
}

func TestAttempt_ConnectivityFailure_DoesNotDeleteRowOwnedByAnotherInstance(t *testing.T) {
	st := startDBStore(t)
	ctx := context.Background()
	stubSupportedCodec(t)

	w := makeWorkerWithDB(st, &scriptedStore{retrieveErr: storage.ErrUnavailable}, 3)
	id := testUID(t, "foreign")
	row := claimedRow(t, st, w, id, 0)
	w2 := makeWorkerWithDB(st, &scriptedStore{retrieveErr: storage.ErrUnavailable}, 3)
	w2.attempt(ctx, row)
	if after := findRow(st, id, 1); after == nil || after.Status != db.StatusGenerating {
		t.Fatalf("row of another instance must stay GENERATING, got %v", after)
	}
}

func TestClaimNeedsNoSemaphore(t *testing.T) {
	st, dsn := startDBStoreWithDSN(t)
	ctx := context.Background()
	stubSupportedCodec(t)

	t.Run("busy_semaphore_does_not_block_claim_or_download", func(t *testing.T) {
		var probes atomic.Int32
		swap(t, &videoDetectCodecFromFileFunc, func(context.Context, string) (string, error) {
			probes.Add(1)
			return "h264", nil
		})
		store := newEOFStore()
		w := makeWorkerWithSem(st, store, 3)
		id := testUID(t, "busy")
		seedPending(t, st, id, 1)
		w.deps.AsyncSem <- struct{}{}
		tickCtx, cancel := context.WithCancel(ctx)
		t.Cleanup(cancel)
		w.tick(tickCtx)
		store.waitEOF(t, 1)

		row := findRow(st, id, 1)
		if row.Status != db.StatusGenerating || row.ClaimedBy == nil || *row.ClaimedBy != w.instanceID {
			t.Fatalf("row must be claimed while the semaphore is busy: %+v", row)
		}
		if probes.Load() != 0 {
			t.Fatal("ffmpeg ran without a slot")
		}
		<-w.deps.AsyncSem
		eventually(t, "row READY", func() bool {
			r := findRow(st, id, 1)
			return r != nil && r.Status == db.StatusReady
		})
	})

	t.Run("lost_claim_takes_no_slot_and_leaves_no_live_entry", func(t *testing.T) {
		w := makeWorkerWithSem(st, &scriptedStore{body: "x"}, 3)
		w.bind(ctx)
		id := testUID(t, "lost")
		seedPending(t, st, id, 1)
		claimOne(t, st, "other-instance", id, 1)
		w.runOne(id, 1, db.SizeM)
		if w.liveCount() != 0 || len(w.deps.AsyncSem) != 0 {
			t.Fatalf("lost claim left live=%d slots=%d", w.liveCount(), len(w.deps.AsyncSem))
		}
		sqlExec(t, dsn, `UPDATE thumbnail SET status='PENDING', claimed_by=NULL WHERE file_id=$1`, id)
		w.runOne(id, 1, db.SizeM)
		eventually(t, "row READY", func() bool {
			r := findRow(st, id, 1)
			return r != nil && r.Status == db.StatusReady
		})
		eventually(t, "live set empty", func() bool { return w.liveCount() == 0 })
	})

	t.Run("runOne_leaves_row_for_the_sweep_when_instance_is_full", func(t *testing.T) {
		w := makeWorkerWithSem(st, &scriptedStore{body: "x"}, 3)
		w.bind(ctx)
		id := testUID(t, "full")
		seedPending(t, st, id, 1)
		for i := 0; i < maxInFlight; i++ {
			w.live[liveKey("busy", i, db.SizeM)] = struct{}{}
		}
		before := findRow(st, id, 1)
		w.runOne(id, 1, db.SizeM)
		after := findRow(st, id, 1)
		if after.Status != db.StatusPending || after.ClaimedBy != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("row must be untouched while the instance is full: %+v", after)
		}
	})
}

func TestCleanupOnce(t *testing.T) {
	st, dsn := startDBStoreWithDSN(t)
	ctx := context.Background()
	store := newMemStore()

	const (
		day     = 24 * time.Hour
		version = 3
	)
	type seedOpts struct {
		status   string
		original bool
		noBlob   bool
	}
	owner := func(id string) string { return "own-" + id }
	key := func(id string) memKey { return memKey{"blob-" + id, version, "docs", owner(id)} }
	seed := func(id string, ago time.Duration, o seedOpts) {
		t.Helper()
		row := db.Thumbnail{FileID: id, Version: version, Size: db.SizeM, Kind: "video", OwnerID: owner(id), ServiceType: "docs",
			Original: o.original, ContentType: strp("image/jpeg")}
		if !o.original {
			row.BlobID = strp("blob-" + id)
			if !o.noBlob {
				store.seed("blob-"+id, version, "docs", owner(id), []byte("data"))
			}
		}
		if ok, err := st.PutReady(ctx, row, nil); err != nil || !ok {
			t.Fatalf("seed %s: ok=%v err=%v", id, ok, err)
		}
		if o.status != "" {
			sqlExec(t, dsn, `UPDATE thumbnail SET status=$2 WHERE file_id=$1`, id, o.status)
		}
		setAccessedAgo(t, dsn, id, ago)
	}
	seed("c_ready", 31*day, seedOpts{})
	seed("c_original", 31*day, seedOpts{original: true})
	seed("c_legacy", 31*day, seedOpts{status: db.StatusLegacy})
	seed("c_old_404", 32*day, seedOpts{noBlob: true})
	seed("c_old_err", 33*day, seedOpts{})
	seed("c_recent", 29*day, seedOpts{})
	for _, status := range []string{db.StatusFailed, db.StatusUnsupported, db.StatusPending, db.StatusGenerating} {
		seed("c_"+status, 90*day, seedOpts{status: status})
	}
	store.deleteErr = func(k memKey) error {
		switch k {
		case key("c_old_err"):
			return errors.New("storage down")
		}
		return nil
	}
	w := makeWorkerWithDB(st, store, 3)

	if !w.cleanupOnce(ctx) {
		t.Fatal("cleanupOnce reported DB not ready")
	}

	gone := []string{"c_ready", "c_original", "c_legacy", "c_old_404", "c_old_err"}
	kept := []string{"c_recent", "c_" + db.StatusFailed, "c_" + db.StatusUnsupported, "c_" + db.StatusPending, "c_" + db.StatusGenerating}
	for _, id := range gone {
		if row := findRow(st, id, version); row != nil {
			t.Errorf("%s must be deleted", id)
		}
	}
	for _, id := range kept {
		if row := findRow(st, id, version); row == nil {
			t.Errorf("%s must be kept", id)
		}
	}
	deleted := map[memKey]bool{}
	for _, k := range store.deletedKeys() {
		deleted[k] = true
	}
	for _, id := range []string{"c_ready", "c_legacy", "c_old_404"} {
		if !deleted[key(id)] {
			t.Errorf("blob of %s: Delete must have been called with the row's version, service type and owner (deleted: %v)", id, store.deletedKeys())
		}
	}
	for _, id := range []string{"c_ready", "c_legacy"} {
		if store.has("blob-"+id, version, "docs", owner(id)) {
			t.Errorf("blob of %s still stored", id)
		}
	}
	if deleted[key("c_original")] {
		t.Error("an original row has no blob: no storage call")
	}
	if !store.has("blob-c_old_err", version, "docs", owner("c_old_err")) {
		t.Error("a failed blob delete must leave the blob in place")
	}
	if n := sqlInt(t, dsn, `SELECT count(*) FROM thumbnail_blob WHERE blob_id='blob-c_old_err' AND status='TOMBSTONE'`); n != 1 {
		t.Error("the row of a failed blob delete goes with a tombstone for the purge to retry")
	}
	if n := sqlInt(t, dsn, `SELECT count(*) FROM thumbnail_blob`); n != 1 {
		t.Errorf("tombstones = %d, only the failed one may be left", n)
	}
	for _, id := range []string{"c_recent", "c_" + db.StatusFailed, "c_" + db.StatusUnsupported, "c_" + db.StatusPending, "c_" + db.StatusGenerating} {
		if deleted[key(id)] {
			t.Errorf("blob of %s must not be touched", id)
		}
	}
	if n := len(store.deletedKeys()); n != 3 {
		t.Errorf("recorded Delete calls = %d, want 3 (ready, legacy, 404; the failing one records nothing)", n)
	}

	t.Run("skips_when_lock_held", func(t *testing.T) {
		seed("c_locked", 40*day, seedOpts{})
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		var got bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(0x7a65787472617663)).Scan(&got); err != nil || !got {
			t.Fatalf("holder lock: %v %v", got, err)
		}
		w.cleanupOnce(ctx)
		if row := findRow(st, "c_locked", version); row == nil {
			t.Fatal("run must be skipped while another session holds the lock")
		}
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(0x7a65787472617663)); err != nil {
			t.Fatal(err)
		}
		w.cleanupOnce(ctx)
		if row := findRow(st, "c_locked", version); row != nil {
			t.Fatal("run must proceed once the lock is free")
		}
	})
}
