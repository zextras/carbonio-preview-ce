// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package db contains integration tests for the thumbnail DB layer.
//
// Each test uses a real Postgres 16 container spun up via the Docker CLI.
// A fresh schema (via Migrate) is applied once per test-helper call.
// Tests run sequentially within the package but each sub-test gets its own
// isolated store pointing at the same schema with a unique file_id prefix to
// avoid cross-test interference.
//
// Run requirements:
//   - Docker daemon accessible at the default socket
//   - postgres:16-alpine image pulled (or network access to pull it)
//
// If Docker is unavailable the tests skip themselves gracefully.

package db

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Docker-based Postgres helper (no Testcontainers library — matches repo style)
// ---------------------------------------------------------------------------

// pgContainer tracks a running Postgres Docker container for the test.
type pgContainer struct {
	id  string // Docker container ID
	dsn string
}

// startPostgres spins up a postgres:16-alpine container, waits until it is
// ready to accept connections, runs Migrate, and returns a connected *Store.
// t.Cleanup stops and removes the container automatically.
func startPostgres(t *testing.T) *Store {
	t.Helper()
	store := startPostgresRaw(t)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func startPostgresRaw(t *testing.T) *Store {
	t.Helper()

	// Require Docker.
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH — skipping DB integration tests")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable (%v): %s", err, out)
	}

	const (
		pgUser  = "preview"
		pgPass  = "preview"
		pgDB    = "preview_test"
		pgPort  = "5432"
		imgName = "postgres:16-alpine"
	)

	// Start the container.
	out, err := exec.Command("docker", "run", "--rm", "-d",
		"-e", "POSTGRES_USER="+pgUser,
		"-e", "POSTGRES_PASSWORD="+pgPass,
		"-e", "POSTGRES_DB="+pgDB,
		"-P", // publish all exposed ports to random host ports
		imgName,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v — %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-fv", containerID).Run()
	})

	// Retrieve the mapped host port.
	portOut, err := exec.Command(
		"docker", "inspect",
		"--format", fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%s/tcp") 0).HostPort}}`, pgPort),
		containerID,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect port: %v — %s", err, portOut)
	}
	hostPort := strings.TrimSpace(string(portOut))

	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%s/%s?sslmode=disable",
		pgUser, pgPass, hostPort, pgDB)

	// Poll until Postgres is ready (up to 30 s).
	ctx := context.Background()
	var store *Store
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, serr := New(ctx, dsn, PoolConfig{MaxConns: 5})
		if serr == nil {
			store = st
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if store == nil {
		t.Fatalf("postgres not ready after 30s (DSN: %s)", dsn)
	}
	t.Cleanup(store.Close)
	return store
}

// uid returns a unique file_id string for a test, preventing cross-test row
// conflicts when tests share the same schema.
func uid(t *testing.T, suffix string) string {
	t.Helper()
	// Replace any characters not safe for a VARCHAR: use test name + suffix.
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, t.Name())
	// Truncate to fit within VARCHAR(64).
	s := safe + "_" + suffix
	if len(s) > 60 {
		s = s[len(s)-60:]
	}
	return s
}

func enqueuePair(t *testing.T, st *Store, id string, version int) {
	t.Helper()
	for _, size := range []string{SizeS, SizeM} {
		if ok, err := st.Enqueue(context.Background(), newPending(id, version, size, "video"), nil); err != nil || !ok {
			t.Fatalf("Enqueue %s: ok=%v err=%v", size, ok, err)
		}
	}
}

func claimPair(t *testing.T, st *Store, id string, version int, inst string) []Thumbnail {
	t.Helper()
	var rows []Thumbnail
	for _, size := range []string{SizeS, SizeM} {
		got, err := st.Claim(context.Background(), id, version, size, inst)
		if err != nil || len(got) != 1 {
			t.Fatalf("Claim %s: rows=%d err=%v, want the row", size, len(got), err)
		}
		rows = append(rows, got...)
	}
	return rows
}

func pairOf(t *testing.T, st *Store, id string, version int) map[string]*Thumbnail {
	t.Helper()
	got, err := st.FindPair(context.Background(), id, version)
	if err != nil || len(got) != 2 || got[SizeS] == nil || got[SizeM] == nil {
		t.Fatalf("FindPair: %v err=%v, want both sizes", got, err)
	}
	return got
}

// ---------------------------------------------------------------------------
// Claim atomicity / single-flight
// ---------------------------------------------------------------------------

// TestClaim_Atomicity fires N concurrent Claim calls on each size of a pair
// and asserts exactly one wins each row while the others get none.
func TestClaim_Atomicity(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "claim")
	const version = 1
	const N = 20

	enqueuePair(t, store, fileID, version)

	for _, size := range []string{SizeS, SizeM} {
		results := make([][]Thumbnail, N)
		var wg sync.WaitGroup
		for i := 0; i < N; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, err := store.Claim(ctx, fileID, version, size, fmt.Sprintf("inst-%02d", i))
				if err != nil {
					t.Errorf("Claim[%d]: %v", i, err)
					return
				}
				results[i] = rows
			}()
		}
		wg.Wait()

		winners := 0
		winnerID := ""
		for i, rows := range results {
			switch len(rows) {
			case 0:
			case 1:
				winners++
				winnerID = fmt.Sprintf("inst-%02d", i)
			default:
				t.Fatalf("inst-%02d got %d rows of size %s, want 0 or 1", i, len(rows), size)
			}
		}
		if winners != 1 {
			t.Fatalf("size %s: expected exactly 1 winner, got %d", size, winners)
		}
		row := pairOf(t, store, fileID, version)[size]
		if row.Status != StatusGenerating || row.ClaimedBy == nil || *row.ClaimedBy != winnerID {
			t.Errorf("size %s: status %q claimed_by %v, want GENERATING by %q", size, row.Status, row.ClaimedBy, winnerID)
		}
	}
}

// TestClaim_OnlyTheRequestedSize asserts a claim moves one row and leaves the
// other size PENDING for its own claim.
func TestClaim_OnlyTheRequestedSize(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "claim_one")
	enqueuePair(t, st, id, 1)

	rows, err := st.Claim(ctx, id, 1, SizeS, "inst-a")
	if err != nil || len(rows) != 1 || rows[0].Size != SizeS || rows[0].Status != StatusGenerating {
		t.Fatalf("Claim s = %+v err=%v, want only the s row", rows, err)
	}
	got := pairOf(t, st, id, 1)
	if got[SizeS].Status != StatusGenerating || got[SizeM].Status != StatusPending || got[SizeM].ClaimedBy != nil {
		t.Fatalf("after claiming s: s=%s m=%s claimed_by=%v, want m untouched", got[SizeS].Status, got[SizeM].Status, got[SizeM].ClaimedBy)
	}
	if rows, err := st.Claim(ctx, id, 1, SizeS, "inst-b"); err != nil || len(rows) != 0 {
		t.Fatalf("second Claim s = %+v err=%v, want none", rows, err)
	}
}

// ---------------------------------------------------------------------------
// ReleaseWithAttempt (the HIGH fix)
// ---------------------------------------------------------------------------

// TestReleaseWithAttempt_IncrementsAttempts asserts that ReleaseWithAttempt on
// the GENERATING rows owned by the right instance transitions both sizes to
// PENDING and increments attempts by 1.
func TestReleaseWithAttempt_IncrementsAttempts(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "relwith")
	const version = 1
	const instID = "inst-1"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)
	before := pairOf(t, store, fileID, version)

	if err := store.ReleaseWithAttempt(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("ReleaseWithAttempt: %v", err)
	}

	after := pairOf(t, store, fileID, version)
	for size, row := range after {
		if row.Status != StatusPending {
			t.Errorf("%s status: got %q, want PENDING", size, row.Status)
		}
		if row.Attempts != before[size].Attempts+1 {
			t.Errorf("%s attempts: got %d, want %d (incremented by 1)", size, row.Attempts, before[size].Attempts+1)
		}
		if row.ClaimedBy != nil {
			t.Errorf("%s claimed_by: got %v, want nil", size, row.ClaimedBy)
		}
	}
}

// TestReleaseWithAttempt_WrongInstanceNoop asserts that ReleaseWithAttempt from
// the wrong instance ID is a no-op: the rows stay GENERATING, attempts unchanged.
func TestReleaseWithAttempt_WrongInstanceNoop(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "relwith_wrong")
	const version = 1
	const ownerInst = "inst-owner"
	const wrongInst = "inst-intruder"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, ownerInst)
	before := pairOf(t, store, fileID, version)

	if err := store.ReleaseWithAttempt(ctx, fileID, version, wrongInst, AllSizes); err != nil {
		t.Fatalf("ReleaseWithAttempt (wrong inst): unexpected error: %v", err)
	}

	after := pairOf(t, store, fileID, version)
	for size, row := range after {
		if row.Status != StatusGenerating {
			t.Errorf("%s status: got %q, want GENERATING (no-op)", size, row.Status)
		}
		if row.Attempts != before[size].Attempts {
			t.Errorf("%s attempts: got %d, want %d (unchanged)", size, row.Attempts, before[size].Attempts)
		}
	}
}

// ---------------------------------------------------------------------------
// Release (plain — attempts UNCHANGED)
// ---------------------------------------------------------------------------

// TestRelease_DoesNotIncrementAttempts verifies that plain Release returns the
// GENERATING rows to PENDING without touching the attempts counter.
func TestRelease_DoesNotIncrementAttempts(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "plain_release")
	const version = 1
	const instID = "inst-rel"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)
	before := pairOf(t, store, fileID, version)

	if err := store.Release(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("Release: %v", err)
	}

	after := pairOf(t, store, fileID, version)
	for size, row := range after {
		if row.Status != StatusPending {
			t.Errorf("%s status: got %q, want PENDING", size, row.Status)
		}
		if row.Attempts != before[size].Attempts {
			t.Errorf("%s attempts: got %d, want %d (UNCHANGED by plain Release)", size, row.Attempts, before[size].Attempts)
		}
	}
}

// ---------------------------------------------------------------------------
// ReclaimStale
// ---------------------------------------------------------------------------

// TestReclaimStale_ReclaimsOldRow verifies that GENERATING rows with an old
// claimed_at are returned by ReclaimStale, their attempts are incremented, and
// the claimed_by is updated to the new instance.
func TestReclaimStale_ReclaimsOldRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reclaim_stale")
	const version = 1
	const originalInst = "inst-original"
	const newInst = "inst-new"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, originalInst)

	_, err := store.pool.Exec(ctx,
		`UPDATE thumbnail SET claimed_at = now() - interval '1 hour'
		 WHERE file_id=$1 AND version=$2`,
		fileID, version,
	)
	if err != nil {
		t.Fatalf("backdate claimed_at: %v", err)
	}

	reclaimed, err := store.ReclaimStale(ctx, newInst, 1*time.Minute)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}

	found := map[string]bool{}
	for _, r := range reclaimed {
		if r.FileID == fileID && r.Version == version {
			found[r.Size] = true
			if r.ClaimedBy == nil || *r.ClaimedBy != newInst {
				t.Errorf("%s claimed_by: got %v, want %q", r.Size, r.ClaimedBy, newInst)
			}
			if r.Attempts != 1 {
				t.Errorf("%s attempts: got %d, want 1 (incremented once)", r.Size, r.Attempts)
			}
		}
	}
	if !found[SizeS] || !found[SizeM] {
		t.Errorf("stale rows (%s, %d) not all returned by ReclaimStale: %v", fileID, version, found)
	}
}

// TestReclaimStale_FreshRowNotReclaimed verifies that GENERATING rows with a
// recent claimed_at are NOT returned by ReclaimStale.
func TestReclaimStale_FreshRowNotReclaimed(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reclaim_fresh")
	const version = 1
	const instID = "inst-fresh"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)

	reclaimed, err := store.ReclaimStale(ctx, "other-inst", 1*time.Hour)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}

	for _, r := range reclaimed {
		if r.FileID == fileID && r.Version == version {
			t.Errorf("fresh row should NOT be reclaimed, but it was: %+v", r)
		}
	}
}

// ---------------------------------------------------------------------------
// Terminal guards: MarkReady / MarkUnsupported / MarkFailed
// ---------------------------------------------------------------------------

// TestMarkReady_OnlyAffectsOwnedRow verifies that MarkReady only transitions
// the GENERATING rows owned by the matching instanceID. Stolen rows (different
// claimed_by) must remain unchanged, and a size that was never claimed is not
// written.
func TestMarkReady_OnlyAffectsOwnedRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markready_guard")
	const version = 1
	const realInst = "inst-real"
	const wrongInst = "inst-impostor"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, realInst)

	rows := []Thumbnail{
		{FileID: fileID, Version: version, Size: SizeS, BlobID: strp("preview-s"), ContentType: strp("image/jpeg")},
		{FileID: fileID, Version: version, Size: SizeM, BlobID: strp("preview-m"), ContentType: strp("image/jpeg")},
	}

	written, err := store.MarkReady(ctx, wrongInst, rows)
	if err != nil {
		t.Fatalf("MarkReady (wrong inst): unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written (wrong inst): got %v, want none", written)
	}
	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusGenerating {
			t.Errorf("%s status: got %q, want GENERATING (MarkReady with wrong inst must be no-op)", size, row.Status)
		}
		if row.BlobID != nil {
			t.Errorf("%s blob_id: got %v, want nil (no-op)", size, row.BlobID)
		}
	}

	store.exec(t, `DELETE FROM thumbnail_blob`)
	written, err = store.MarkReady(ctx, realInst, rows[1:])
	if err != nil {
		t.Fatalf("MarkReady (correct inst): %v", err)
	}
	if len(written) != 1 || written[0] != SizeM {
		t.Fatalf("written: got %v, want [m]", written)
	}
	pair := pairOf(t, store, fileID, version)
	if pair[SizeM].Status != StatusReady || pair[SizeM].BlobID == nil || *pair[SizeM].BlobID != "preview-m" {
		t.Errorf("m row not READY with blob: %+v", pair[SizeM])
	}
	if pair[SizeS].Status != StatusGenerating || pair[SizeS].BlobID != nil {
		t.Errorf("s row was not passed to MarkReady and must be untouched: %+v", pair[SizeS])
	}

	written, err = store.MarkReady(ctx, realInst, rows)
	if err != nil {
		t.Fatalf("MarkReady (both): %v", err)
	}
	if len(written) != 1 || written[0] != SizeS {
		t.Errorf("written: got %v, want [s] (m is already READY)", written)
	}
	pair = pairOf(t, store, fileID, version)
	if pair[SizeS].Status != StatusReady || *pair[SizeS].BlobID != "preview-s" {
		t.Errorf("s row not READY with blob: %+v", pair[SizeS])
	}
}

// TestMarkUnsupported_TerminatesRow verifies MarkUnsupported transitions GENERATING→UNSUPPORTED on both sizes.
func TestMarkUnsupported_TerminatesRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markunsup")
	const version = 1
	const instID = "inst-u"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)
	if err := store.MarkUnsupported(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusUnsupported {
			t.Errorf("%s status: got %q, want UNSUPPORTED", size, row.Status)
		}
		if row.ClaimedBy != nil {
			t.Errorf("%s claimed_by: got %v, want nil (cleared on terminal)", size, row.ClaimedBy)
		}
	}
}

// TestMarkUnsupported_WrongInstanceNoop verifies MarkUnsupported with wrong claimed_by is a no-op.
func TestMarkUnsupported_WrongInstanceNoop(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markunsup_guard")
	const version = 1
	const realInst = "inst-real"
	const wrongInst = "inst-wrong"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, realInst)
	if err := store.MarkUnsupported(ctx, fileID, version, wrongInst, AllSizes); err != nil {
		t.Fatalf("MarkUnsupported (wrong inst): unexpected error: %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusGenerating {
			t.Errorf("%s status: got %q, want GENERATING (no-op)", size, row.Status)
		}
	}
}

// TestMarkFailed_TerminatesRow verifies MarkFailed transitions GENERATING→FAILED on both sizes.
func TestMarkFailed_TerminatesRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markfailed")
	const version = 1
	const instID = "inst-f"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)
	if err := store.MarkFailed(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusFailed {
			t.Errorf("%s status: got %q, want FAILED", size, row.Status)
		}
		if row.ClaimedBy != nil {
			t.Errorf("%s claimed_by: got %v, want nil", size, row.ClaimedBy)
		}
	}
}

// TestMarkFailed_TerminatesRow_RegressionLongErrorNoLongerStored is a regression
// test for the root cause this change fixes: previously, callers wrote the
// error/reason string into last_error VARCHAR(512). ffmpeg stderr and
// multi-line pgx connection errors can exceed 512 chars, which made the
// UPDATE itself fail with SQLSTATE 22001 ("value too long"), stranding the row
// in GENERATING forever (it never reached FAILED). Now that last_error no
// longer exists, MarkFailed takes no string argument at all, so a caller
// dealing with an arbitrarily long underlying error can never break the
// terminal transition — it is impossible by construction. This test proves
// the transition always completes regardless of how the caller would have
// described the failure.
func TestMarkFailed_TerminatesRow_RegressionLongErrorNoLongerStored(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markfailed_longerr")
	const version = 1
	const instID = "inst-longerr"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)

	// Simulate what used to be a >512-char error message (e.g. multi-line
	// ffmpeg stderr / pgx connection error). MarkFailed no longer accepts (or
	// persists) any such string, so there is nothing to truncate or overflow.
	_ = strings.Repeat("x", 2000) // representative of the oversized message that used to break the UPDATE

	if err := store.MarkFailed(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("MarkFailed: unexpected error (terminal transition must always succeed): %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusFailed {
			t.Errorf("%s status: got %q, want FAILED (row must not be stranded in GENERATING)", size, row.Status)
		}
		if row.ClaimedBy != nil {
			t.Errorf("%s claimed_by: got %v, want nil", size, row.ClaimedBy)
		}
	}
}

// TestMarkFailed_WrongInstanceNoop verifies MarkFailed with wrong claimed_by is a no-op.
func TestMarkFailed_WrongInstanceNoop(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markfailed_guard")
	const version = 1
	const realInst = "inst-real"
	const wrongInst = "inst-thief"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, realInst)
	if err := store.MarkFailed(ctx, fileID, version, wrongInst, AllSizes); err != nil {
		t.Fatalf("MarkFailed (wrong inst): unexpected error: %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusGenerating {
			t.Errorf("%s status: got %q, want GENERATING (no-op)", size, row.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// FindPendingNewest ordering + limit
// ---------------------------------------------------------------------------

// TestFindPendingNewest_OrderAndLimit inserts several PENDING rows with distinct
// created_at and verifies FindPendingNewest returns them newest-first up to limit.
func TestFindPendingNewest_OrderAndLimit(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	base := uid(t, "pending_order")
	type entry struct {
		fileID    string
		createdAt time.Time
	}
	now := time.Now().UTC()
	entries := []entry{
		{base + "_A", now.Add(-5 * time.Second)},
		{base + "_B", now.Add(-4 * time.Second)},
		{base + "_C", now.Add(-3 * time.Second)},
		{base + "_D", now.Add(-2 * time.Second)},
		{base + "_E", now.Add(-1 * time.Second)},
	}
	for _, e := range entries {
		_, err := store.pool.Exec(ctx,
			`INSERT INTO thumbnail
			    (file_id, version, size, status, kind, owner_id, service_type, attempts, created_at, updated_at)
			 VALUES ($1, 1, 'm', 'PENDING', 'video', 'owner1', 'files', 0, $2, $2)
			 ON CONFLICT (file_id, version, size) DO NOTHING`,
			e.fileID, e.createdAt,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", e.fileID, err)
		}
	}

	rows, err := store.FindPendingNewest(ctx, 3)
	if err != nil {
		t.Fatalf("FindPendingNewest: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].CreatedAt.After(rows[i-1].CreatedAt) {
			t.Errorf("row[%d].created_at=%v > row[%d].created_at=%v (not newest-first)",
				i, rows[i].CreatedAt, i-1, rows[i-1].CreatedAt)
		}
	}
	if rows[0].FileID != base+"_E" {
		t.Errorf("first row file_id: got %q, want %q", rows[0].FileID, base+"_E")
	}
}

func newPending(id string, version int, size, kind string) Thumbnail {
	return Thumbnail{FileID: id, Version: version, Size: size, Kind: kind, OwnerID: "o", ServiceType: "files"}
}

func strp(s string) *string { return &s }

func TestPutReady_InsertAndConflict(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "put")

	row := newPending(id, 1, SizeM, "office")
	row.BlobID, row.ContentType, row.Digest = strp("b1"), strp("image/jpeg"), strp("d1")
	ok, err := st.PutReady(ctx, row, nil)
	if err != nil || !ok {
		t.Fatalf("first PutReady: ok=%v err=%v", ok, err)
	}
	got, err := st.FindPair(ctx, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	r := got[SizeM]
	if r == nil || r.Status != StatusReady || *r.BlobID != "b1" || *r.Digest != "d1" || *r.ContentType != "image/jpeg" || r.Kind != "office" {
		t.Fatalf("stored row = %+v", r)
	}
	if got[SizeS] != nil {
		t.Errorf("size s must be absent")
	}

	row.BlobID = strp("b2")
	ok, err = st.PutReady(ctx, row, nil)
	if err != nil || ok {
		t.Fatalf("conflicting PutReady: ok=%v err=%v, want false", ok, err)
	}
	got, _ = st.FindPair(ctx, id, 1)
	if *got[SizeM].BlobID != "b1" {
		t.Errorf("conflicting insert must not overwrite the row")
	}
}

func TestPutReady_CASReplacesAndLoses(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "cas")

	first := newPending(id, 1, SizeS, "video")
	first.BlobID, first.Digest, first.Lang = strp("b1"), strp("d1"), strp("it")
	if ok, err := st.PutReady(ctx, first, nil); err != nil || !ok {
		t.Fatalf("seed: ok=%v err=%v", ok, err)
	}
	cur, _ := st.FindPair(ctx, id, 1)
	expected := cur[SizeS]

	next := newPending(id, 1, SizeS, "office")
	next.BlobID, next.Digest, next.ContentType, next.OwnerID, next.ServiceType = strp("b2"), strp("d2"), strp("image/png"), "o2", "chats"
	ok, err := st.PutReady(ctx, next, expected)
	if err != nil || !ok {
		t.Fatalf("CAS replace: ok=%v err=%v", ok, err)
	}
	cur, _ = st.FindPair(ctx, id, 1)
	r := cur[SizeS]
	if *r.BlobID != "b2" || *r.Digest != "d2" || r.Kind != "office" || r.OwnerID != "o2" || r.ServiceType != "chats" || r.Lang != nil || r.Attempts != 0 || r.Status != StatusReady {
		t.Fatalf("replaced row = %+v", r)
	}

	third := next
	third.BlobID = strp("b3")
	ok, err = st.PutReady(ctx, third, expected)
	if err != nil || ok {
		t.Fatalf("stale CAS: ok=%v err=%v, want false", ok, err)
	}
	cur, _ = st.FindPair(ctx, id, 1)
	if *cur[SizeS].BlobID != "b2" {
		t.Errorf("a lost CAS must not write")
	}
}

func TestFindPair(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "pair")

	got, err := st.FindPair(ctx, id, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	for _, size := range []string{SizeS, SizeM} {
		if ok, err := st.Enqueue(ctx, newPending(id, 1, size, "video"), nil); err != nil || !ok {
			t.Fatalf("Enqueue %s: ok=%v err=%v", size, ok, err)
		}
	}
	if ok, err := st.Enqueue(ctx, newPending(id, 2, SizeS, "video"), nil); err != nil || !ok {
		t.Fatal(err)
	}
	got, err = st.FindPair(ctx, id, 1)
	if err != nil || len(got) != 2 || got[SizeS] == nil || got[SizeM] == nil {
		t.Fatalf("pair = %v err=%v", got, err)
	}
	if got[SizeS].Status != StatusPending || got[SizeS].Version != 1 {
		t.Errorf("row = %+v", got[SizeS])
	}
}

func TestClaim_ReturnsOnlyPendingRows(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "claimpair")

	for _, size := range []string{SizeS, SizeM} {
		if ok, err := st.Enqueue(ctx, newPending(id, 1, size, "office"), nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	st.exec(t, `UPDATE thumbnail SET status='READY' WHERE file_id=$1 AND size='s'`, id)

	rows, err := st.Claim(ctx, id, 1, SizeS, "inst-a")
	if err != nil || len(rows) != 0 {
		t.Fatalf("Claim of the READY s row = %+v err=%v, want none", rows, err)
	}
	rows, err = st.Claim(ctx, id, 1, SizeM, "inst-a")
	if err != nil || len(rows) != 1 || rows[0].Size != SizeM || rows[0].Status != StatusGenerating {
		t.Fatalf("Claim = %+v err=%v, want only the PENDING m row", rows, err)
	}
	rows, err = st.Claim(ctx, id, 1, SizeM, "inst-b")
	if err != nil || len(rows) != 0 {
		t.Fatalf("second Claim = %+v err=%v, want none", rows, err)
	}
}

func TestEnqueue_CASReplacesReadyAndKeepsOthers(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "enq")

	ready := newPending(id, 1, SizeM, "video")
	ready.BlobID, ready.Digest = strp("old"), strp("d1")
	if ok, err := st.PutReady(ctx, ready, nil); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cur, _ := st.FindPair(ctx, id, 1)

	again := newPending(id, 1, SizeM, "video")
	again.Digest = strp("d2")
	if ok, err := st.Enqueue(ctx, again, nil); err != nil || ok {
		t.Fatalf("insert over existing row: ok=%v err=%v, want false", ok, err)
	}
	if ok, err := st.Enqueue(ctx, again, cur[SizeM]); err != nil || !ok {
		t.Fatalf("CAS enqueue: ok=%v err=%v", ok, err)
	}
	cur2, _ := st.FindPair(ctx, id, 1)
	r := cur2[SizeM]
	if r.Status != StatusPending || r.BlobID != nil || *r.Digest != "d2" || r.Original || r.ContentType != nil {
		t.Fatalf("enqueued row = %+v", r)
	}
	if ok, err := st.Enqueue(ctx, again, cur[SizeM]); err != nil || ok {
		t.Fatalf("stale expected row: ok=%v err=%v, want false", ok, err)
	}
}

func TestMarkReady_WritesOnlyClaimedSizes(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "mr")

	for _, size := range []string{SizeS, SizeM} {
		if ok, err := st.Enqueue(ctx, newPending(id, 1, size, "video"), nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	st.exec(t, `UPDATE thumbnail SET status='GENERATING', claimed_by='inst-a' WHERE file_id=$1 AND size='m'`, id)

	rows := []Thumbnail{
		{FileID: id, Version: 1, Size: SizeS, BlobID: strp("bs"), ContentType: strp("image/jpeg")},
		{FileID: id, Version: 1, Size: SizeM, BlobID: strp("bm"), ContentType: strp("image/jpeg")},
	}
	written, err := st.MarkReady(ctx, "inst-a", rows)
	if err != nil || len(written) != 1 || written[0] != SizeM {
		t.Fatalf("written = %v err=%v, want [m]", written, err)
	}
	cur, _ := st.FindPair(ctx, id, 1)
	if cur[SizeS].Status != StatusPending || cur[SizeS].BlobID != nil {
		t.Errorf("unclaimed size must be untouched: %+v", cur[SizeS])
	}
	if cur[SizeM].Status != StatusReady || *cur[SizeM].BlobID != "bm" || cur[SizeM].ClaimedBy != nil {
		t.Errorf("claimed size not ready: %+v", cur[SizeM])
	}
}

func TestEnqueue_CASRefreshesCreatedAt(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "created")
	ready := newPending(id, 1, SizeM, "video")
	ready.BlobID = strp("old")
	if ok, err := st.PutReady(ctx, ready, nil); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE thumbnail SET created_at = now() - interval '2 days' WHERE file_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	cur, _ := st.FindPair(ctx, id, 1)
	if ok, err := st.Enqueue(ctx, newPending(id, 1, SizeM, "video"), cur[SizeM]); err != nil || !ok {
		t.Fatalf("CAS enqueue: ok=%v err=%v", ok, err)
	}
	got, _ := st.FindPair(ctx, id, 1)
	if age := time.Since(got[SizeM].CreatedAt); age > time.Hour {
		t.Fatalf("re-enqueued row keeps created_at %s old, want it refreshed", age)
	}
}

func (s *Store) count(t *testing.T, q string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func tombstoneStatus(t *testing.T, st *Store, blobID string) string {
	t.Helper()
	var status string
	if err := st.pool.QueryRow(context.Background(), `SELECT status FROM thumbnail_blob WHERE blob_id=$1`, blobID).Scan(&status); err != nil {
		return ""
	}
	return status
}

func TestDeleteFile_DeletesEveryRowAndTombstonesItsBlobsTogether(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "delfile")
	for _, size := range []string{SizeS, SizeM} {
		r := newPending(id, 1, size, "video")
		r.BlobID = strp("blob-" + size)
		if ok, err := st.PutReady(ctx, r, nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	orig := newPending(id, 2, SizeM, "image")
	orig.Original = true
	if ok, err := st.PutReady(ctx, orig, nil); err != nil || !ok {
		t.Fatal(ok, err)
	}

	deleted, err := st.DeleteFile(ctx, id)
	if err != nil || len(deleted) != 3 {
		t.Fatalf("DeleteFile: %d rows err=%v", len(deleted), err)
	}
	for _, size := range []string{SizeS, SizeM} {
		if got := tombstoneStatus(t, st, "blob-"+size); got != BlobTombstone {
			t.Errorf("blob-%s status %q, want TOMBSTONE", size, got)
		}
	}
	if n := st.count(t, `SELECT count(*) FROM thumbnail_blob`); n != 2 {
		t.Errorf("tombstones = %d, an original row has no blob", n)
	}
}

func TestDeleteFile_RollsBackWhenTheTombstoneCannotBeWritten(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "delfile_tx")
	r := newPending(id, 1, SizeM, "video")
	r.BlobID = strp("blob-x")
	if ok, err := st.PutReady(ctx, r, nil); err != nil || !ok {
		t.Fatal(ok, err)
	}
	st.exec(t, `ALTER TABLE thumbnail_blob RENAME TO thumbnail_blob_gone`)
	if _, err := st.DeleteFile(ctx, id); err == nil {
		t.Fatal("want the tombstone error")
	}
	st.exec(t, `ALTER TABLE thumbnail_blob_gone RENAME TO thumbnail_blob`)
	if rows, _ := st.FindPair(ctx, id, 1); len(rows) != 1 {
		t.Fatalf("rows = %d, the row delete must roll back with the failed tombstone", len(rows))
	}
}

func TestPutReady_ConfirmConsumesTheReservationAndTombstonesTheReplacedBlob(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "confirm")
	old := newPending(id, 1, SizeM, "image")
	old.BlobID = strp("b-old")
	if ok, err := st.PutReady(ctx, old, nil); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := st.Reserve(ctx, "b-new", "owner1", "files", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Reserve(ctx, "b-new", "owner1", "files", 1); err == nil {
		t.Fatal("a blob id can be reserved only once")
	}
	cur, _ := st.FindPair(ctx, id, 1)
	next := newPending(id, 1, SizeM, "image")
	next.BlobID = strp("b-new")
	if ok, err := st.PutReady(ctx, next, cur[SizeM]); err != nil || !ok {
		t.Fatalf("confirm: ok=%v err=%v", ok, err)
	}
	if got := tombstoneStatus(t, st, "b-new"); got != "" {
		t.Errorf("reservation status %q, want consumed", got)
	}
	if got := tombstoneStatus(t, st, "b-old"); got != BlobTombstone {
		t.Errorf("replaced blob status %q, want TOMBSTONE", got)
	}

	if err := st.Reserve(ctx, "b-lost", "owner1", "files", 1); err != nil {
		t.Fatal(err)
	}
	lost := newPending(id, 1, SizeM, "image")
	lost.BlobID = strp("b-lost")
	if ok, err := st.PutReady(ctx, lost, cur[SizeM]); err != nil || ok {
		t.Fatalf("stale CAS: ok=%v err=%v, want a clean loss", ok, err)
	}
	if got := tombstoneStatus(t, st, "b-lost"); got != BlobTombstone {
		t.Errorf("lost blob status %q, want TOMBSTONE", got)
	}
}

func TestMarkReady_SkippedRowTombstonesItsBlob(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "mr_lost")
	enqueuePair(t, st, id, 1)
	claimPair(t, st, id, 1, "inst-a")
	if err := st.Reserve(ctx, "b-ok", "owner1", "files", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Reserve(ctx, "b-skipped", "owner1", "files", 1); err != nil {
		t.Fatal(err)
	}
	written, err := st.MarkReady(ctx, "inst-a", []Thumbnail{
		{FileID: id, Version: 1, Size: SizeS, BlobID: strp("b-ok"), OwnerID: "owner1", ServiceType: "files"},
		{FileID: id, Version: 1, Size: SizeM, BlobID: strp("b-skipped"), OwnerID: "owner1", ServiceType: "files"},
	})
	if err != nil || len(written) != 2 {
		t.Fatalf("MarkReady: %v %v", written, err)
	}
	written, err = st.MarkReady(ctx, "inst-a", []Thumbnail{{FileID: id, Version: 1, Size: SizeM, BlobID: strp("b-skipped-2"), OwnerID: "owner1", ServiceType: "files"}})
	if err != nil || len(written) != 0 {
		t.Fatalf("second MarkReady: %v %v", written, err)
	}
	if got := tombstoneStatus(t, st, "b-skipped-2"); got != BlobTombstone {
		t.Errorf("blob of a row that was no longer owned: status %q, want TOMBSTONE", got)
	}
	if got := tombstoneStatus(t, st, "b-ok"); got != "" {
		t.Errorf("a written row consumes its reservation, status %q", got)
	}
}

func TestPurgeQueries_ExpireFindAndCountAttempts(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	st.exec(t, `INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status, created_at)
        VALUES ('old', 'o', 'files', 1, 'PENDING', now() - interval '25 hours'),
               ('young', 'o', 'files', 1, 'PENDING', now() - interval '1 hour'),
               ('tomb', 'o', 'files', 1, 'TOMBSTONE', now())`)
	locked, err := st.WithCleanupLock(ctx, func(ctx context.Context, c *CleanupConn) error {
		if n, err := c.ExpireReservations(ctx, 24*time.Hour); err != nil || n != 1 {
			t.Errorf("ExpireReservations: %d %v, want 1", n, err)
		}
		got, err := c.FindTombstones(ctx, nil, 10)
		if err != nil || len(got) != 2 {
			t.Fatalf("FindTombstones: %v %v, want the expired reservation and the tombstone", got, err)
		}
		for i := 1; i <= TombstoneMaxAttempts; i++ {
			if n, err := c.RecordAttempt(ctx, "tomb"); err != nil || n != i {
				t.Fatalf("RecordAttempt: %d %v, want %d", n, err, i)
			}
		}
		got, _ = c.FindTombstones(ctx, nil, 10)
		if len(got) != 1 || got[0].BlobID != "old" {
			t.Errorf("an exhausted tombstone must not be retried: %v", got)
		}
		return c.DeleteTombstone(ctx, "old")
	})
	if err != nil || !locked {
		t.Fatalf("run: %v %v", locked, err)
	}
	if tombstoneStatus(t, st, "young") != BlobPending {
		t.Error("a reservation younger than 24 h stays PENDING")
	}
}

func TestClaimedTransitions_ScopedToSizes(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "scoped")
	enqueuePair(t, st, id, 1)
	claimPair(t, st, id, 1, "inst-a")

	if err := st.Release(ctx, id, 1, "inst-a", []string{SizeS}); err != nil {
		t.Fatal(err)
	}
	got := pairOf(t, st, id, 1)
	if got[SizeS].Status != StatusPending || got[SizeM].Status != StatusGenerating {
		t.Fatalf("Release scoped to s: s=%s m=%s", got[SizeS].Status, got[SizeM].Status)
	}
	if err := st.MarkFailed(ctx, id, 1, "inst-a", []string{SizeS}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFailed(ctx, id, 1, "inst-a", []string{SizeM}); err != nil {
		t.Fatal(err)
	}
	got = pairOf(t, st, id, 1)
	if got[SizeS].Status != StatusPending || got[SizeM].Status != StatusFailed {
		t.Fatalf("MarkFailed scoped to m: s=%s m=%s", got[SizeS].Status, got[SizeM].Status)
	}
}

func TestPutReady_ExpiredReservationLosesTheWriteAndKeepsTheBlobTombstoned(t *testing.T) {
	st := startPostgres(t)
	ctx := context.Background()
	id := uid(t, "expired_confirm")
	if err := st.Reserve(ctx, "b-stalled", "owner1", "files", 1); err != nil {
		t.Fatal(err)
	}
	st.exec(t, `UPDATE thumbnail_blob SET status='TOMBSTONE' WHERE blob_id='b-stalled'`)
	row := newPending(id, 1, SizeM, "image")
	row.BlobID = strp("b-stalled")
	if ok, err := st.PutReady(ctx, row, nil); err != nil || ok {
		t.Fatalf("PutReady: ok=%v err=%v, want a clean loss", ok, err)
	}
	if got, _ := st.FindPair(ctx, id, 1); len(got) != 0 {
		t.Errorf("a READY row must never point at a tombstoned blob: %+v", got)
	}
	if tombstoneStatus(t, st, "b-stalled") != BlobTombstone {
		t.Error("the blob stays tombstoned for the purge")
	}

	enqueuePair(t, st, id, 2)
	claimPair(t, st, id, 2, "inst")
	if err := st.Reserve(ctx, "b-stalled-2", "owner1", "files", 2); err != nil {
		t.Fatal(err)
	}
	st.exec(t, `UPDATE thumbnail_blob SET status='TOMBSTONE' WHERE blob_id='b-stalled-2'`)
	written, err := st.MarkReady(ctx, "inst", []Thumbnail{{FileID: id, Version: 2, Size: SizeM, BlobID: strp("b-stalled-2"), OwnerID: "owner1", ServiceType: "files"}})
	if err != nil || len(written) != 0 {
		t.Fatalf("MarkReady: %v %v, want the row left GENERATING", written, err)
	}
}
