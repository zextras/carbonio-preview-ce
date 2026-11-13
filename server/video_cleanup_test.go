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

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
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
	sqlExec(t, dsn, `UPDATE video_preview SET last_accessed_at = now() - $2::interval WHERE file_id=$1`,
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

func (s *scriptedStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, error) {
	if s.retrieveErr != nil {
		return nil, s.retrieveErr
	}
	return io.NopCloser(strings.NewReader(s.body)), nil
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
	origProbe, origExtract := videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc
	videoDetectCodecFromFileFunc = func(context.Context, string) (string, error) { return "h264", nil }
	videoFirstFrameFromFileFunc = func(context.Context, string) ([]byte, error) { return tinyJPEG(t), nil }
	t.Cleanup(func() {
		videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc = origProbe, origExtract
	})
}

func claimedRow(t *testing.T, st *db.Store, w *VideoWorker, fileID string, attempts int) db.VideoPreview {
	t.Helper()
	ctx := context.Background()
	if err := st.EnqueueIfAbsent(ctx, fileID, 1, "owner1", "files"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < attempts; i++ {
		if ok, err := st.Claim(ctx, fileID, 1, w.instanceID); err != nil || !ok {
			t.Fatalf("Claim: ok=%v err=%v", ok, err)
		}
		if err := st.ReleaseWithAttempt(ctx, fileID, 1, w.instanceID); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := st.Claim(ctx, fileID, 1, w.instanceID); err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	row, err := st.Find(ctx, fileID, 1)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	return *row
}

func TestAttempt_FailureClassification(t *testing.T) {
	st := startVideoPostgres(t)
	ctx := context.Background()

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
			id := videoUID(t, "cls")
			row := claimedRow(t, st, w, id, c.attempts)
			w.attempt(ctx, row)

			after, err := st.Find(ctx, id, 1)
			if err != nil {
				t.Fatal(err)
			}
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
	st := startVideoPostgres(t)
	ctx := context.Background()
	stubSupportedCodec(t)

	w := makeWorkerWithDB(st, &scriptedStore{retrieveErr: storage.ErrUnavailable}, 3)
	id := videoUID(t, "foreign")
	row := claimedRow(t, st, w, id, 0)
	row2 := row
	w2 := makeWorkerWithDB(st, &scriptedStore{retrieveErr: storage.ErrUnavailable}, 3)
	w2.attempt(ctx, row2)
	if after, _ := st.Find(ctx, id, 1); after == nil || after.Status != db.StatusGenerating {
		t.Fatalf("row of another instance must stay GENERATING, got %v", after)
	}
}

func TestClaimNeedsNoSemaphore(t *testing.T) {
	st, dsn := startVideoPostgresWithDSN(t)
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
		id := videoUID(t, "busy")
		if err := st.EnqueueIfAbsent(ctx, id, 1, "owner1", "files"); err != nil {
			t.Fatal(err)
		}
		w.deps.VideoSem <- struct{}{}
		tickCtx, cancel := context.WithCancel(ctx)
		t.Cleanup(cancel)
		w.tick(tickCtx)
		store.waitEOF(t, 1)

		row, _ := st.Find(ctx, id, 1)
		if row.Status != db.StatusGenerating || row.ClaimedBy == nil || *row.ClaimedBy != w.instanceID {
			t.Fatalf("row must be claimed while the semaphore is busy: %+v", row)
		}
		if probes.Load() != 0 {
			t.Fatal("ffmpeg ran without a slot")
		}
		<-w.deps.VideoSem
		eventually(t, "row READY", func() bool {
			r, _ := st.Find(ctx, id, 1)
			return r != nil && r.Status == db.StatusReady
		})
	})

	t.Run("lost_claim_takes_no_slot_and_leaves_no_live_entry", func(t *testing.T) {
		w := makeWorkerWithSem(st, &scriptedStore{body: "x"}, 3)
		w.bind(ctx)
		id := videoUID(t, "lost")
		if err := st.EnqueueIfAbsent(ctx, id, 1, "owner1", "files"); err != nil {
			t.Fatal(err)
		}
		if ok, err := st.Claim(ctx, id, 1, "other-instance"); err != nil || !ok {
			t.Fatalf("Claim: %v %v", ok, err)
		}
		w.runOne(id, 1)
		if w.liveCount() != 0 || len(w.deps.VideoSem) != 0 {
			t.Fatalf("lost claim left live=%d slots=%d", w.liveCount(), len(w.deps.VideoSem))
		}
		sqlExec(t, dsn, `UPDATE video_preview SET status='PENDING', claimed_by=NULL WHERE file_id=$1`, id)
		w.runOne(id, 1)
		eventually(t, "row READY", func() bool {
			r, _ := st.Find(ctx, id, 1)
			return r != nil && r.Status == db.StatusReady
		})
		eventually(t, "live set empty", func() bool { return w.liveCount() == 0 })
	})

	t.Run("runOne_leaves_row_for_the_sweep_when_instance_is_full", func(t *testing.T) {
		w := makeWorkerWithSem(st, &scriptedStore{body: "x"}, 3)
		w.bind(ctx)
		id := videoUID(t, "full")
		if err := st.EnqueueIfAbsent(ctx, id, 1, "owner1", "files"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < maxInFlight; i++ {
			w.live[liveKey("busy", i)] = struct{}{}
		}
		before, _ := st.Find(ctx, id, 1)
		w.runOne(id, 1)
		after, _ := st.Find(ctx, id, 1)
		if after.Status != db.StatusPending || after.ClaimedBy != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("row must be untouched while the instance is full: %+v", after)
		}
	})
}

func TestGetVideoPreview_Ready_TouchesLastAccessOnlyWhenOlderThanOneDay(t *testing.T) {
	if !haveRealRender {
		t.Skip("thumbnailing needs cgo (libvips)")
	}
	st, dsn := startVideoPostgresWithDSN(t)
	ctx := context.Background()

	const oldID = "a1b2c3d4-e5f6-7890-abcd-ef1234567891"
	const freshID = "a1b2c3d4-e5f6-7890-abcd-ef1234567892"
	for _, id := range []string{oldID, freshID} {
		if err := st.InsertReady(ctx, id, 1, "owner1", "files", "blob-"+id); err != nil {
			t.Fatal(err)
		}
	}
	setAccessedAgo(t, dsn, oldID, 3*24*time.Hour)
	setAccessedAgo(t, dsn, freshID, time.Hour)
	freshBefore, _ := st.Find(ctx, freshID, 1)

	deps := buildResolveDeps(st, &fakeVideoStore{blob: tinyJPEG(t)})
	handler := buildGetVideoPreview(deps, nil)
	for _, id := range []string{oldID, freshID} {
		if _, err := handler(ctx, &apispec.VideoGetPreviewInput{
			ID: id, Version: 1, Area: "100x100", ServiceType: "files", Quality: "medium", OutputFormat: "jpeg",
		}); err != nil {
			t.Fatalf("handler %s: %v", id, err)
		}
	}
	if row, _ := st.Find(ctx, oldID, 1); time.Since(row.LastAccessedAt) > time.Minute {
		t.Errorf("old READY row not touched: %v", row.LastAccessedAt)
	}
	if row, _ := st.Find(ctx, freshID, 1); !row.LastAccessedAt.Equal(freshBefore.LastAccessedAt) {
		t.Errorf("fresh row must not be written")
	}
}

func TestCleanupOnce(t *testing.T) {
	st, dsn := startVideoPostgresWithDSN(t)
	ctx := context.Background()

	seed := func(id, status string, ago time.Duration) {
		if err := st.InsertReady(ctx, id, 1, "owner1", "files", "blob-"+id); err != nil {
			t.Fatal(err)
		}
		sqlExec(t, dsn, `UPDATE video_preview SET status=$2 WHERE file_id=$1`, id, status)
		setAccessedAgo(t, dsn, id, ago)
	}
	const day = 24 * time.Hour
	seed("c_old", db.StatusReady, 31*day)
	seed("c_old_404", db.StatusReady, 32*day)
	seed("c_old_err", db.StatusReady, 33*day)
	seed("c_touched", db.StatusReady, 34*day)
	seed("c_recent", db.StatusReady, 29*day)
	for _, status := range []string{db.StatusFailed, db.StatusUnsupported, db.StatusPending, db.StatusGenerating} {
		seed("c_"+status, status, 90*day)
	}

	store := &scriptedStore{
		deleteErr: map[string]error{
			"blob-c_old_404": storage.ErrNotFound,
			"blob-c_old_err": errors.New("storage down"),
		},
		onDelete: func(id string) {
			if id == "blob-c_touched" {
				sqlExec(t, dsn, `UPDATE video_preview SET last_accessed_at = now() WHERE file_id='c_touched'`)
			}
		},
	}
	w := makeWorkerWithDB(st, store, 3)

	if !w.cleanupOnce(ctx) {
		t.Fatal("cleanupOnce reported DB not ready")
	}

	gone := []string{"c_old", "c_old_404"}
	kept := []string{"c_old_err", "c_touched", "c_recent", "c_" + db.StatusFailed, "c_" + db.StatusUnsupported, "c_" + db.StatusPending, "c_" + db.StatusGenerating}
	for _, id := range gone {
		if row, _ := st.Find(ctx, id, 1); row != nil {
			t.Errorf("%s must be deleted", id)
		}
	}
	for _, id := range kept {
		if row, _ := st.Find(ctx, id, 1); row == nil {
			t.Errorf("%s must be kept", id)
		}
	}
	deleted := map[string]bool{}
	for _, id := range store.deletedIDs() {
		deleted[id] = true
	}
	for _, id := range []string{"blob-c_old", "blob-c_old_404", "blob-c_old_err", "blob-c_touched"} {
		if !deleted[id] {
			t.Errorf("blob %s: Delete must have been called", id)
		}
	}
	for _, id := range []string{"blob-c_recent", "blob-c_" + db.StatusFailed, "blob-c_" + db.StatusUnsupported, "blob-c_" + db.StatusPending, "blob-c_" + db.StatusGenerating} {
		if deleted[id] {
			t.Errorf("blob %s must not be touched", id)
		}
	}

	t.Run("skips_when_lock_held", func(t *testing.T) {
		seed("c_locked", db.StatusReady, 40*day)
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
		if row, _ := st.Find(ctx, "c_locked", 1); row == nil {
			t.Fatal("run must be skipped while another session holds the lock")
		}
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(0x7a65787472617663)); err != nil {
			t.Fatal(err)
		}
		w.cleanupOnce(ctx)
		if row, _ := st.Find(ctx, "c_locked", 1); row != nil {
			t.Fatal("run must proceed once the lock is free")
		}
	})
}
