// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Store) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (s *Store) setAccessed(t *testing.T, fileID string, ago time.Duration) {
	t.Helper()
	s.exec(t, `UPDATE thumbnail SET last_accessed_at = now() - $2::interval WHERE file_id=$1`,
		fileID, fmt.Sprintf("%d seconds", int(ago.Seconds())))
}

func (s *Store) setStatus(t *testing.T, fileID, status string) {
	t.Helper()
	s.exec(t, `UPDATE thumbnail SET status=$2 WHERE file_id=$1`, fileID, status)
}

func TestMigration2_DropsFailedKeepsOthersAndStampsLastAccess(t *testing.T) {
	st := startPostgresRaw(t)
	ctx := context.Background()

	migrateTo(t, st, 1)
	for _, status := range []string{StatusFailed, StatusReady, StatusPending} {
		st.exec(t, `INSERT INTO video_preview (file_id, version, status, owner_id, service_type, attempts, created_at, updated_at)
                    VALUES ($1, 1, $2, 'o', 'files', 0, now() - interval '90 days', now() - interval '90 days')`,
			"v1_"+status, status)
	}

	before := time.Now().Add(-time.Minute)
	migrateTo(t, st, 2)

	count := func(id string) int {
		var n int
		if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM video_preview WHERE file_id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("v1_" + StatusFailed); n != 0 {
		t.Fatalf("FAILED row must be gone, found %d", n)
	}
	for _, status := range []string{StatusReady, StatusPending} {
		var accessed time.Time
		if err := st.pool.QueryRow(ctx, `SELECT last_accessed_at FROM video_preview WHERE file_id=$1`, "v1_"+status).Scan(&accessed); err != nil {
			t.Fatalf("%s row must be kept: %v", status, err)
		}
		if accessed.Before(before) {
			t.Errorf("%s: last_accessed_at %v must be the migration time, not the old created_at", status, accessed)
		}
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestDeleteClaimed_OnlyOwnGeneratingRow(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()

	own, other, pending, ready := uid(t, "own"), uid(t, "other"), uid(t, "pend"), uid(t, "ready")
	for _, id := range []string{own, other, pending, ready} {
		enqueuePair(t, st, id, 1)
	}
	for _, c := range []struct{ id, inst string }{{own, "inst-a"}, {other, "inst-b"}, {ready, "inst-a"}} {
		claimPair(t, st, c.id, 1, c.inst)
	}
	written, err := st.MarkReady(ctx, "inst-a", []Thumbnail{
		{FileID: ready, Version: 1, Size: SizeS, BlobID: strp("blob-s")},
		{FileID: ready, Version: 1, Size: SizeM, BlobID: strp("blob-m")},
	})
	if err != nil || len(written) != 2 {
		t.Fatalf("MarkReady: written=%v err=%v", written, err)
	}

	for _, id := range []string{own, other, pending, ready} {
		if err := st.DeleteClaimed(ctx, id, 1, "inst-a", AllSizes); err != nil {
			t.Fatalf("DeleteClaimed %s: %v", id, err)
		}
	}
	want := map[string]int{own: 0, other: 2, pending: 2, ready: 2}
	for id, n := range want {
		rows, err := st.FindPair(ctx, id, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != n {
			t.Errorf("%s: rows=%d, want %d", id, len(rows), n)
		}
	}
}

func readyRow(t *testing.T, st *Store, id string, ago time.Duration) {
	t.Helper()
	row := newPending(id, 1, SizeM, "video")
	row.BlobID = strp("blob-" + id)
	if ok, err := st.PutReady(context.Background(), row, nil); err != nil || !ok {
		t.Fatalf("PutReady: ok=%v err=%v", ok, err)
	}
	st.setAccessed(t, id, ago)
}

func readyPair(t *testing.T, st *Store, id string, ago time.Duration) {
	t.Helper()
	for _, size := range []string{SizeS, SizeM} {
		row := newPending(id, 1, size, "video")
		row.BlobID = strp("blob-" + size + "-" + id)
		if ok, err := st.PutReady(context.Background(), row, nil); err != nil || !ok {
			t.Fatalf("PutReady %s: ok=%v err=%v", size, ok, err)
		}
	}
	st.setAccessed(t, id, ago)
}

func TestTouchReady_OnlyReadyOlderThanOneDay(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()

	old, fresh, notReady := uid(t, "old"), uid(t, "fresh"), uid(t, "notready")
	readyRow(t, st, old, 2*24*time.Hour)
	readyRow(t, st, fresh, time.Hour)
	readyRow(t, st, notReady, 2*24*time.Hour)
	st.setStatus(t, notReady, StatusUnsupported)

	freshBefore, _ := st.FindPair(ctx, fresh, 1)
	notReadyBefore, _ := st.FindPair(ctx, notReady, 1)

	for _, id := range []string{old, fresh, notReady} {
		if err := st.TouchReady(ctx, id, 1, SizeM); err != nil {
			t.Fatalf("TouchReady %s: %v", id, err)
		}
	}

	if p, _ := st.FindPair(ctx, old, 1); time.Since(p[SizeM].LastAccessedAt) > time.Minute {
		t.Errorf("old READY row not touched: %v", p[SizeM].LastAccessedAt)
	}
	if p, _ := st.FindPair(ctx, fresh, 1); !p[SizeM].LastAccessedAt.Equal(freshBefore[SizeM].LastAccessedAt) {
		t.Errorf("fresh row must not be written")
	}
	if p, _ := st.FindPair(ctx, notReady, 1); !p[SizeM].LastAccessedAt.Equal(notReadyBefore[SizeM].LastAccessedAt) {
		t.Errorf("non-READY row must not be touched")
	}
}

type rowKey struct{ id, size string }

func TestExpired_SelectionDeletionAndPaging(t *testing.T) {
	st := startPostgres(t)

	expired := []string{uid(t, "e1"), uid(t, "e2"), uid(t, "e3")}
	for i, id := range expired {
		readyRow(t, st, id, time.Duration(40+i)*24*time.Hour)
	}
	legacy := uid(t, "legacy")
	readyRow(t, st, legacy, 43*24*time.Hour)
	st.setStatus(t, legacy, StatusLegacy)
	both := uid(t, "both")
	readyPair(t, st, both, 50*24*time.Hour)
	recent := uid(t, "recent")
	readyRow(t, st, recent, 29*24*time.Hour)
	others := map[string]string{}
	for _, status := range []string{StatusFailed, StatusUnsupported, StatusPending, StatusGenerating} {
		id := uid(t, status)
		readyRow(t, st, id, 90*24*time.Hour)
		st.setStatus(t, id, status)
		others[id] = status
	}

	withCleanupConn(t, st, func(c *CleanupConn) {
		ctx := context.Background()
		wantOrder := []rowKey{{both, SizeM}, {both, SizeS}, {legacy, SizeM}, {expired[2], SizeM}, {expired[1], SizeM}, {expired[0], SizeM}}

		for _, pageSize := range []int{1, 2} {
			var got []rowKey
			var cursor *Thumbnail
			for {
				rows, err := c.FindExpired(ctx, cursor, pageSize)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					got = append(got, rowKey{r.FileID, r.Size})
				}
				if len(rows) < pageSize {
					break
				}
				cursor = &rows[len(rows)-1]
			}
			if len(got) != len(wantOrder) {
				t.Fatalf("page size %d: expired selection: got %v, want %v", pageSize, got, wantOrder)
			}
			for i := range wantOrder {
				if got[i] != wantOrder[i] {
					t.Fatalf("page size %d: expired selection (oldest first, paged): got %v, want %v", pageSize, got, wantOrder)
				}
			}
		}

		if err := st.TouchReady(ctx, expired[0], 1, SizeM); err != nil {
			t.Fatal(err)
		}
		all := append(append([]string{recent, legacy, both}, expired...), keys(others)...)
		for _, id := range all {
			for _, size := range []string{SizeS, SizeM} {
				if _, err := c.DeleteExpired(ctx, id, 1, size); err != nil {
					t.Fatalf("DeleteExpired %s/%s: %v", id, size, err)
				}
			}
		}
		for _, id := range []string{both, legacy, expired[1], expired[2]} {
			if rows, _ := st.FindByFile(ctx, id); len(rows) != 0 {
				t.Errorf("%s should be deleted: %v", id, rows)
			}
		}
		for _, id := range append([]string{recent, expired[0]}, keys(others)...) {
			if rows, _ := st.FindByFile(ctx, id); len(rows) != 1 {
				t.Errorf("%s must be kept: %v", id, rows)
			}
		}
	})
}

func withCleanupConn(t *testing.T, st *Store, fn func(*CleanupConn)) {
	t.Helper()
	locked, err := st.WithCleanupLock(context.Background(), func(_ context.Context, c *CleanupConn) error {
		fn(c)
		return nil
	})
	if err != nil || !locked {
		t.Fatalf("WithCleanupLock: locked=%v err=%v", locked, err)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestWithCleanupLock_SkipsWhileAnotherSessionHoldsIt(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()

	holder, err := st.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, cleanupLockKey).Scan(&got); err != nil || !got {
		t.Fatalf("holder lock: got=%v err=%v", got, err)
	}

	ran := false
	locked, err := st.WithCleanupLock(ctx, func(context.Context, *CleanupConn) error { ran = true; return nil })
	if err != nil || locked || ran {
		t.Fatalf("while held: locked=%v ran=%v err=%v, want skipped", locked, ran, err)
	}

	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, cleanupLockKey); err != nil {
		t.Fatal(err)
	}
	holder.Release()

	locked, err = st.WithCleanupLock(ctx, func(context.Context, *CleanupConn) error { ran = true; return nil })
	if err != nil || !locked || !ran {
		t.Fatalf("after release: locked=%v ran=%v err=%v, want run", locked, ran, err)
	}
	locked, err = st.WithCleanupLock(ctx, func(context.Context, *CleanupConn) error { return nil })
	if err != nil || !locked {
		t.Fatalf("lock must be released after a run: locked=%v err=%v", locked, err)
	}
}

func TestCleanupRun_WorksWithSingleConnectionPool(t *testing.T) {
	full := startPostgres(t)
	cfg := full.pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := &Store{pool: pool}
	t.Cleanup(st.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	id := uid(t, "old")
	readyRow(t, st, id, 40*24*time.Hour)

	locked, err := st.WithCleanupLock(ctx, func(ctx context.Context, c *CleanupConn) error {
		rows, err := c.FindExpired(ctx, nil, 10)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Errorf("expired rows: got %d, want 1", len(rows))
		}
		_, err = c.DeleteExpired(ctx, id, 1, SizeM)
		return err
	})
	if err != nil || !locked {
		t.Fatalf("run: locked=%v err=%v", locked, err)
	}
	rows, err := st.FindByFile(ctx, id)
	if err != nil || len(rows) != 0 {
		t.Fatalf("FindByFile after run: rows=%v err=%v", rows, err)
	}
}

func TestMarkReady_RefreshesLastAccessedAt(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()

	id := uid(t, "re")
	enqueuePair(t, st, id, 1)
	st.setAccessed(t, id, 90*24*time.Hour)
	rows := claimPair(t, st, id, 1, "inst")
	for i := range rows {
		rows[i].BlobID = strp("blob-" + rows[i].Size)
	}
	written, err := st.MarkReady(ctx, "inst", rows)
	if err != nil || len(written) != 2 {
		t.Fatalf("MarkReady: written=%v err=%v", written, err)
	}
	for size, r := range pairOf(t, st, id, 1) {
		if time.Since(r.LastAccessedAt) > time.Minute {
			t.Errorf("%s: MarkReady must refresh last_accessed_at, got %v", size, r.LastAccessedAt)
		}
	}
}
