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
	s.exec(t, `UPDATE video_preview SET last_accessed_at = now() - $2::interval WHERE file_id=$1`,
		fileID, fmt.Sprintf("%d seconds", int(ago.Seconds())))
}

func (s *Store) setStatus(t *testing.T, fileID, status string) {
	t.Helper()
	s.exec(t, `UPDATE video_preview SET status=$2 WHERE file_id=$1`, fileID, status)
}

func TestMigration2_DropsFailedKeepsOthersAndStampsLastAccess(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()

	st.exec(t, `DROP INDEX idx_video_preview_cleanup`)
	st.exec(t, `ALTER TABLE video_preview DROP COLUMN last_accessed_at`)
	st.exec(t, `UPDATE schema_migrations SET version=1`)
	for _, status := range []string{StatusFailed, StatusReady, StatusPending} {
		st.exec(t, `INSERT INTO video_preview (file_id, version, status, owner_id, service_type, attempts, created_at, updated_at)
                    VALUES ($1, 1, $2, 'o', 'files', 0, now() - interval '90 days', now() - interval '90 days')`,
			"v1_"+status, status)
	}

	before := time.Now().Add(-time.Minute)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if vp, err := st.Find(ctx, "v1_"+StatusFailed, 1); err != nil || vp != nil {
		t.Fatalf("FAILED row must be gone: vp=%v err=%v", vp, err)
	}
	for _, status := range []string{StatusReady, StatusPending} {
		vp, err := st.Find(ctx, "v1_"+status, 1)
		if err != nil || vp == nil {
			t.Fatalf("%s row must be kept: vp=%v err=%v", status, vp, err)
		}
		if vp.LastAccessedAt.Before(before) {
			t.Errorf("%s: last_accessed_at %v must be the migration time, not the old created_at", status, vp.LastAccessedAt)
		}
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
		if err := st.EnqueueIfAbsent(ctx, id, 1, "o", "files"); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ id, inst string }{{own, "inst-a"}, {other, "inst-b"}, {ready, "inst-a"}} {
		if ok, err := st.Claim(ctx, c.id, 1, c.inst); err != nil || !ok {
			t.Fatalf("Claim %s: ok=%v err=%v", c.id, ok, err)
		}
	}
	if err := st.MarkReady(ctx, ready, 1, "inst-a", "blob"); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{own, other, pending, ready} {
		if err := st.DeleteClaimed(ctx, id, 1, "inst-a"); err != nil {
			t.Fatalf("DeleteClaimed %s: %v", id, err)
		}
	}
	want := map[string]bool{own: false, other: true, pending: true, ready: true}
	for id, kept := range want {
		vp, err := st.Find(ctx, id, 1)
		if err != nil {
			t.Fatal(err)
		}
		if (vp != nil) != kept {
			t.Errorf("%s: present=%v, want %v", id, vp != nil, kept)
		}
	}
}

func readyRow(t *testing.T, st *Store, id string, ago time.Duration) {
	t.Helper()
	if err := st.InsertReady(context.Background(), id, 1, "o", "files", "blob-"+id); err != nil {
		t.Fatal(err)
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

	freshBefore, _ := st.Find(ctx, fresh, 1)
	notReadyBefore, _ := st.Find(ctx, notReady, 1)

	for _, id := range []string{old, fresh, notReady} {
		if err := st.TouchReady(ctx, id, 1); err != nil {
			t.Fatalf("TouchReady %s: %v", id, err)
		}
	}

	if vp, _ := st.Find(ctx, old, 1); time.Since(vp.LastAccessedAt) > time.Minute {
		t.Errorf("old READY row not touched: %v", vp.LastAccessedAt)
	}
	if vp, _ := st.Find(ctx, fresh, 1); !vp.LastAccessedAt.Equal(freshBefore.LastAccessedAt) {
		t.Errorf("fresh row must not be written")
	}
	if vp, _ := st.Find(ctx, notReady, 1); !vp.LastAccessedAt.Equal(notReadyBefore.LastAccessedAt) {
		t.Errorf("non-READY row must not be touched")
	}
}

func TestExpiredReady_SelectionDeletionAndPaging(t *testing.T) {
	st := startPostgres(t)

	expired := []string{uid(t, "e1"), uid(t, "e2"), uid(t, "e3")}
	for i, id := range expired {
		readyRow(t, st, id, time.Duration(40+i)*24*time.Hour)
	}
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
		cleanupSelectionAndDeletion(t, st, c, expired, recent, others)
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

func cleanupSelectionAndDeletion(t *testing.T, st *Store, c *CleanupConn, expired []string, recent string, others map[string]string) {
	ctx := context.Background()
	var got []string
	var cursor *VideoPreview
	for {
		rows, err := c.FindExpiredReady(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			got = append(got, r.FileID)
		}
		if len(rows) < 2 {
			break
		}
		cursor = &rows[len(rows)-1]
	}
	if len(got) != 3 || got[0] != expired[2] || got[1] != expired[1] || got[2] != expired[0] {
		t.Fatalf("expired selection (oldest first, paged): got %v", got)
	}

	if err := st.TouchReady(ctx, expired[0], 1); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(append([]string{recent}, expired...), keys(others)...) {
		if err := c.DeleteExpiredReady(ctx, id, 1); err != nil {
			t.Fatalf("DeleteExpiredReady %s: %v", id, err)
		}
	}
	for _, id := range expired[1:] {
		if vp, _ := st.Find(ctx, id, 1); vp != nil {
			t.Errorf("%s should be deleted", id)
		}
	}
	for _, id := range append([]string{recent, expired[0]}, keys(others)...) {
		if vp, _ := st.Find(ctx, id, 1); vp == nil {
			t.Errorf("%s must be kept", id)
		}
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
		rows, err := c.FindExpiredReady(ctx, nil, 10)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Errorf("expired rows: got %d, want 1", len(rows))
		}
		return c.DeleteExpiredReady(ctx, id, 1)
	})
	if err != nil || !locked {
		t.Fatalf("run: locked=%v err=%v", locked, err)
	}
	vp, err := st.Find(ctx, id, 1)
	if err != nil || vp != nil {
		t.Fatalf("Find after run: vp=%v err=%v", vp, err)
	}
}

func TestMarkReady_RefreshesLastAccessedAt(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()

	id := uid(t, "re")
	if err := st.EnqueueIfAbsent(ctx, id, 1, "o", "files"); err != nil {
		t.Fatal(err)
	}
	st.setAccessed(t, id, 90*24*time.Hour)
	if ok, err := st.Claim(ctx, id, 1, "inst"); err != nil || !ok {
		t.Fatalf("Claim: %v %v", ok, err)
	}
	if err := st.MarkReady(ctx, id, 1, "inst", "blob"); err != nil {
		t.Fatal(err)
	}
	vp, err := st.Find(ctx, id, 1)
	if err != nil || vp == nil {
		t.Fatal(err)
	}
	if time.Since(vp.LastAccessedAt) > time.Minute {
		t.Errorf("MarkReady must refresh last_accessed_at, got %v", vp.LastAccessedAt)
	}
}
