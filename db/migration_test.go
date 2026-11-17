// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"testing"
	"time"
)

func migrateTo(t *testing.T, st *Store, version uint) {
	t.Helper()
	m, closeFn, err := st.migrator(context.Background())
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}
	defer closeFn()
	if err := m.Migrate(version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
}

func TestMigration3_KeepsReadyBlobRowsAsLegacy(t *testing.T) {
	st := startPostgresRaw(t)
	ctx := context.Background()
	migrateTo(t, st, 2)

	seed := func(id, status string, blob *string) {
		st.exec(t, `INSERT INTO video_preview
            (file_id, version, status, preview_id, owner_id, service_type, codec, attempts, created_at, updated_at, last_accessed_at)
            VALUES ($1, 3, $2, $3, 'owner-1', 'chats', 'h264', 0, now(), now(), now() - interval '40 days')`,
			id, status, blob)
	}
	blob := "legacy-blob"
	seed("keep", StatusReady, &blob)
	seed("ready-no-blob", StatusReady, nil)
	seed("pending", StatusPending, nil)
	seed("unsupported", StatusUnsupported, nil)
	seed("failed", StatusFailed, nil)
	st.exec(t, `UPDATE video_preview SET claimed_by='x', claimed_at=now() WHERE file_id='pending'`)

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, id := range []string{"ready-no-blob", "pending", "unsupported", "failed"} {
		rows, err := st.FindPair(ctx, id, 3)
		if err != nil || len(rows) != 0 {
			t.Errorf("%s: rows=%v err=%v, want none", id, rows, err)
		}
	}
	rows, err := st.FindPair(ctx, "keep", 3)
	if err != nil || len(rows) != 1 || rows[SizeM] == nil {
		t.Fatalf("keep: rows=%v err=%v, want exactly one", rows, err)
	}
	r := rows[SizeM]
	if r.Size != SizeM || r.Kind != "video" || r.Status != StatusLegacy {
		t.Errorf("size/kind/status = %q/%q/%q, want m/video/LEGACY", r.Size, r.Kind, r.Status)
	}
	if r.BlobID == nil || *r.BlobID != blob || r.OwnerID != "owner-1" || r.ServiceType != "chats" || r.Version != 3 {
		t.Errorf("blob/owner/service/version not preserved: %+v", r)
	}
	if r.Original || r.Digest != nil || r.Lang != nil || r.ContentType != nil {
		t.Errorf("new columns must be empty: %+v", r)
	}
	if age := time.Since(r.LastAccessedAt); age < 39*24*time.Hour || age > 41*24*time.Hour {
		t.Errorf("last_accessed_at not preserved, age = %v", age)
	}
	if r.Codec == nil || *r.Codec != "h264" {
		t.Errorf("codec not preserved: %v", r.Codec)
	}
}

func TestMigration3_Idempotent(t *testing.T) {
	st := startPostgresRaw(t)
	ctx := context.Background()
	migrateTo(t, st, 2)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name='video_preview'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("video_preview must be gone: n=%d err=%v", n, err)
	}
}

func TestMigration3_LegacyRowsAreSweptAfterThirtyDays(t *testing.T) {
	st := startPostgresRaw(t)
	migrateTo(t, st, 2)
	blob := "legacy-blob"
	st.exec(t, `INSERT INTO video_preview
        (file_id, version, status, preview_id, owner_id, service_type, attempts, created_at, updated_at, last_accessed_at)
        VALUES ('old', 1, 'READY', $1, 'o', 'files', 0, now(), now(), now() - interval '31 days')`, blob)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	withCleanupConn(t, st, func(c *CleanupConn) {
		rows, err := c.FindExpired(context.Background(), nil, 10)
		if err != nil || len(rows) != 1 || rows[0].Status != StatusLegacy {
			t.Fatalf("FindExpired = %+v err=%v, want the LEGACY row", rows, err)
		}
	})
}
