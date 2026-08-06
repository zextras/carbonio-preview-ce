// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package db contains integration tests for the video_preview DB layer.
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
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
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

	// Apply migrations.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
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

// ---------------------------------------------------------------------------
// Claim atomicity / single-flight
// ---------------------------------------------------------------------------

// TestClaim_Atomicity fires N concurrent Claim calls and asserts exactly one
// wins while the row ends up in GENERATING claimed by that winner.
func TestClaim_Atomicity(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "claim")
	const version = 1
	const N = 20

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	type result struct {
		instanceID string
		claimed    bool
	}
	results := make([]result, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			instID := fmt.Sprintf("inst-%02d", i)
			ok, err := store.Claim(ctx, fileID, version, instID)
			if err != nil {
				t.Errorf("Claim[%d]: %v", i, err)
				return
			}
			results[i] = result{instID, ok}
		}()
	}
	wg.Wait()

	winners := 0
	var winnerID string
	for _, r := range results {
		if r.claimed {
			winners++
			winnerID = r.instanceID
		}
	}

	if winners != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", winners)
	}

	// Verify DB state: status=GENERATING, claimed_by=winner.
	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if row == nil {
		t.Fatal("row not found after Claim")
	}
	if row.Status != StatusGenerating {
		t.Errorf("status: got %q, want GENERATING", row.Status)
	}
	if row.ClaimedBy == nil || *row.ClaimedBy != winnerID {
		t.Errorf("claimed_by: got %v, want %q", row.ClaimedBy, winnerID)
	}
}

// ---------------------------------------------------------------------------
// EnqueueIfAbsent ON CONFLICT
// ---------------------------------------------------------------------------

// TestEnqueueIfAbsent_Idempotent asserts that concurrent enqueues for the same
// (file_id, version) produce exactly one row and no errors.
func TestEnqueueIfAbsent_Idempotent(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "enqueue")
	const version = 2
	const N = 10

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
				t.Errorf("EnqueueIfAbsent: %v", err)
			}
		}()
	}
	wg.Wait()

	// Exactly one row must exist.
	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if row == nil {
		t.Fatal("expected a row, got nil")
	}
	if row.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING", row.Status)
	}
}

// ---------------------------------------------------------------------------
// ReenqueueReady (the BLOCKER fix)
// ---------------------------------------------------------------------------

// TestReenqueueReady_FromReady asserts that a READY row with a preview_id
// becomes PENDING with preview_id cleared.
func TestReenqueueReady_FromReady(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_ready")
	const version = 1
	const previewID = "prev-abc-123"

	// Insert a READY row directly.
	if err := store.InsertReady(ctx, fileID, version, "owner1", "files", previewID); err != nil {
		t.Fatalf("InsertReady: %v", err)
	}

	if err := store.ReenqueueReady(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueReady: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if row == nil {
		t.Fatal("row disappeared after ReenqueueReady")
	}
	if row.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING", row.Status)
	}
	if row.PreviewID != nil {
		t.Errorf("preview_id: got %v, want nil (must be cleared)", row.PreviewID)
	}
}

// TestReenqueueReady_NoopOnGenerating asserts that ReenqueueReady on a
// GENERATING row is a no-op (RowsAffected=0 implicit, row status unchanged).
func TestReenqueueReady_NoopOnGenerating(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_generating")
	const version = 1

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	if _, err := store.Claim(ctx, fileID, version, "inst-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Row is now GENERATING. ReenqueueReady must be a no-op.
	if err := store.ReenqueueReady(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueReady: unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusGenerating {
		t.Errorf("status: got %q, want GENERATING (no-op)", row.Status)
	}
}

// TestReenqueueReady_NoopOnPending asserts that ReenqueueReady on a PENDING
// row is a no-op (row remains PENDING).
func TestReenqueueReady_NoopOnPending(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_pending")
	const version = 1

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	if err := store.ReenqueueReady(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueReady: unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING (no-op)", row.Status)
	}
}

// ---------------------------------------------------------------------------
// ReleaseWithAttempt (the HIGH fix)
// ---------------------------------------------------------------------------

// TestReleaseWithAttempt_IncrementsAttempts asserts that ReleaseWithAttempt on
// a GENERATING row owned by the right instance transitions to PENDING and
// increments attempts by 1.
func TestReleaseWithAttempt_IncrementsAttempts(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "relwith")
	const version = 1
	const instID = "inst-1"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	before, err := store.Find(ctx, fileID, version)
	if err != nil || before == nil {
		t.Fatalf("Find before: %v", err)
	}
	attempsBefore := before.Attempts

	if err := store.ReleaseWithAttempt(ctx, fileID, version, instID); err != nil {
		t.Fatalf("ReleaseWithAttempt: %v", err)
	}

	after, err := store.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after: %v", err)
	}
	if after.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING", after.Status)
	}
	if after.Attempts != attempsBefore+1 {
		t.Errorf("attempts: got %d, want %d (incremented by 1)", after.Attempts, attempsBefore+1)
	}
	if after.ClaimedBy != nil {
		t.Errorf("claimed_by: got %v, want nil", after.ClaimedBy)
	}
}

// TestReleaseWithAttempt_WrongInstanceNoop asserts that ReleaseWithAttempt from
// the wrong instance ID is a no-op: the row stays GENERATING, attempts unchanged.
func TestReleaseWithAttempt_WrongInstanceNoop(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "relwith_wrong")
	const version = 1
	const ownerInst = "inst-owner"
	const wrongInst = "inst-intruder"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, ownerInst)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	before, err := store.Find(ctx, fileID, version)
	if err != nil || before == nil {
		t.Fatalf("Find before: %v", err)
	}

	// Wrong instance: must be a no-op.
	if err := store.ReleaseWithAttempt(ctx, fileID, version, wrongInst); err != nil {
		t.Fatalf("ReleaseWithAttempt (wrong inst): unexpected error: %v", err)
	}

	after, err := store.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after: %v", err)
	}
	if after.Status != StatusGenerating {
		t.Errorf("status: got %q, want GENERATING (no-op)", after.Status)
	}
	if after.Attempts != before.Attempts {
		t.Errorf("attempts: got %d, want %d (unchanged)", after.Attempts, before.Attempts)
	}
}

// ---------------------------------------------------------------------------
// Release (plain — attempts UNCHANGED)
// ---------------------------------------------------------------------------

// TestRelease_DoesNotIncrementAttempts verifies that plain Release returns a
// GENERATING row to PENDING without touching the attempts counter.
func TestRelease_DoesNotIncrementAttempts(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "plain_release")
	const version = 1
	const instID = "inst-rel"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	before, err := store.Find(ctx, fileID, version)
	if err != nil || before == nil {
		t.Fatalf("Find before: %v", err)
	}

	if err := store.Release(ctx, fileID, version, instID); err != nil {
		t.Fatalf("Release: %v", err)
	}

	after, err := store.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after: %v", err)
	}
	if after.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING", after.Status)
	}
	if after.Attempts != before.Attempts {
		t.Errorf("attempts: got %d, want %d (UNCHANGED by plain Release)", after.Attempts, before.Attempts)
	}
}

// ---------------------------------------------------------------------------
// ReclaimStale
// ---------------------------------------------------------------------------

// TestReclaimStale_ReclaimsOldRow verifies that a GENERATING row with an old
// claimed_at is returned by ReclaimStale, its attempts are incremented, and the
// claimed_by is updated to the new instance.
func TestReclaimStale_ReclaimsOldRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reclaim_stale")
	const version = 1
	const originalInst = "inst-original"
	const newInst = "inst-new"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, originalInst)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Backdate claimed_at to simulate a stale row.
	_, err = store.pool.Exec(ctx,
		`UPDATE video_preview SET claimed_at = now() - interval '1 hour'
		 WHERE file_id=$1 AND version=$2`,
		fileID, version,
	)
	if err != nil {
		t.Fatalf("backdate claimed_at: %v", err)
	}

	// ReclaimStale with a 1-minute TTL — the row's claimed_at is 1 hour old.
	reclaimed, err := store.ReclaimStale(ctx, newInst, 1*time.Minute)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}

	found := false
	for _, r := range reclaimed {
		if r.FileID == fileID && r.Version == version {
			found = true
			if r.ClaimedBy == nil || *r.ClaimedBy != newInst {
				t.Errorf("claimed_by: got %v, want %q", r.ClaimedBy, newInst)
			}
			if r.Attempts != 1 {
				t.Errorf("attempts: got %d, want 1 (incremented once)", r.Attempts)
			}
		}
	}
	if !found {
		t.Errorf("stale row (%s, %d) not returned by ReclaimStale", fileID, version)
	}
}

// TestReclaimStale_FreshRowNotReclaimed verifies that a GENERATING row with a
// recent claimed_at is NOT returned by ReclaimStale.
func TestReclaimStale_FreshRowNotReclaimed(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reclaim_fresh")
	const version = 1
	const instID = "inst-fresh"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Use a 1-hour TTL — the just-claimed row was claimed a moment ago.
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
// the GENERATING row owned by the matching instanceID. A stolen row (different
// claimed_by) must remain unchanged.
func TestMarkReady_OnlyAffectsOwnedRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markready_guard")
	const version = 1
	const realInst = "inst-real"
	const wrongInst = "inst-impostor"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, realInst)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Wrong instance tries to MarkReady: must be a no-op, no error.
	if err := store.MarkReady(ctx, fileID, version, wrongInst, "preview-xxx"); err != nil {
		t.Fatalf("MarkReady (wrong inst): unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusGenerating {
		t.Errorf("status: got %q, want GENERATING (MarkReady with wrong inst must be no-op)", row.Status)
	}
	if row.PreviewID != nil {
		t.Errorf("preview_id: got %v, want nil (no-op)", row.PreviewID)
	}

	// Correct instance can mark ready.
	if err := store.MarkReady(ctx, fileID, version, realInst, "preview-yyy"); err != nil {
		t.Fatalf("MarkReady (correct inst): %v", err)
	}
	row2, err := store.Find(ctx, fileID, version)
	if err != nil || row2 == nil {
		t.Fatalf("Find after correct MarkReady: %v", err)
	}
	if row2.Status != StatusReady {
		t.Errorf("status: got %q, want READY", row2.Status)
	}
	if row2.PreviewID == nil || *row2.PreviewID != "preview-yyy" {
		t.Errorf("preview_id: got %v, want %q", row2.PreviewID, "preview-yyy")
	}
}

// TestMarkUnsupported_TerminatesRow verifies MarkUnsupported transitions GENERATING→UNSUPPORTED.
func TestMarkUnsupported_TerminatesRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markunsup")
	const version = 1
	const instID = "inst-u"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := store.MarkUnsupported(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusUnsupported {
		t.Errorf("status: got %q, want UNSUPPORTED", row.Status)
	}
	if row.ClaimedBy != nil {
		t.Errorf("claimed_by: got %v, want nil (cleared on terminal)", row.ClaimedBy)
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

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, realInst)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := store.MarkUnsupported(ctx, fileID, version, wrongInst); err != nil {
		t.Fatalf("MarkUnsupported (wrong inst): unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusGenerating {
		t.Errorf("status: got %q, want GENERATING (no-op)", row.Status)
	}
}

// TestMarkFailed_TerminatesRow verifies MarkFailed transitions GENERATING→FAILED.
func TestMarkFailed_TerminatesRow(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "markfailed")
	const version = 1
	const instID = "inst-f"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := store.MarkFailed(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusFailed {
		t.Errorf("status: got %q, want FAILED", row.Status)
	}
	if row.ClaimedBy != nil {
		t.Errorf("claimed_by: got %v, want nil", row.ClaimedBy)
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

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Simulate what used to be a >512-char error message (e.g. multi-line
	// ffmpeg stderr / pgx connection error). MarkFailed no longer accepts (or
	// persists) any such string, so there is nothing to truncate or overflow.
	_ = strings.Repeat("x", 2000) // representative of the oversized message that used to break the UPDATE

	if err := store.MarkFailed(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkFailed: unexpected error (terminal transition must always succeed): %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusFailed {
		t.Errorf("status: got %q, want FAILED (row must not be stranded in GENERATING)", row.Status)
	}
	if row.ClaimedBy != nil {
		t.Errorf("claimed_by: got %v, want nil", row.ClaimedBy)
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

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, realInst)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := store.MarkFailed(ctx, fileID, version, wrongInst); err != nil {
		t.Fatalf("MarkFailed (wrong inst): unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusGenerating {
		t.Errorf("status: got %q, want GENERATING (no-op)", row.Status)
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

	// Insert 5 rows with explicit created_at values.
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
			`INSERT INTO video_preview
			    (file_id, version, status, owner_id, service_type, attempts, created_at, updated_at)
			 VALUES ($1, 1, 'PENDING', 'owner1', 'files', 0, $2, $2)
			 ON CONFLICT (file_id, version) DO NOTHING`,
			e.fileID, e.createdAt,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", e.fileID, err)
		}
	}

	// Request 3 — should be E, D, C (newest first).
	rows, err := store.FindPendingNewest(ctx, 3)
	if err != nil {
		t.Fatalf("FindPendingNewest: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	// Verify created_at is monotonically non-increasing.
	for i := 1; i < len(rows); i++ {
		if rows[i].CreatedAt.After(rows[i-1].CreatedAt) {
			t.Errorf("row[%d].created_at=%v > row[%d].created_at=%v (not newest-first)",
				i, rows[i].CreatedAt, i-1, rows[i-1].CreatedAt)
		}
	}
	// Newest should be the one with the highest created_at.
	if rows[0].FileID != base+"_E" {
		t.Errorf("first row file_id: got %q, want %q", rows[0].FileID, base+"_E")
	}
}

// ---------------------------------------------------------------------------
// DeleteByFileId
// ---------------------------------------------------------------------------

// TestDeleteByFileId removes a row and verifies Find returns nil afterwards.
func TestDeleteByFileId(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "delete")
	const version = 1

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	if err := store.DeleteByFileId(ctx, fileID, version); err != nil {
		t.Fatalf("DeleteByFileId: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find after delete: %v", err)
	}
	if row != nil {
		t.Errorf("expected nil after delete, got: %+v", row)
	}
}

// ---------------------------------------------------------------------------
// InsertReady / Find basic round-trip
// ---------------------------------------------------------------------------

// TestInsertReady_RoundTrip verifies InsertReady creates a READY row with the
// expected fields, and Find returns it faithfully.
func TestInsertReady_RoundTrip(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "insert_ready")
	const version = 3
	const previewID = "prev-xyz-789"

	if err := store.InsertReady(ctx, fileID, version, "owner42", "chats", previewID); err != nil {
		t.Fatalf("InsertReady: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: err=%v row=%v", err, row)
	}
	if row.Status != StatusReady {
		t.Errorf("status: got %q, want READY", row.Status)
	}
	if row.PreviewID == nil || *row.PreviewID != previewID {
		t.Errorf("preview_id: got %v, want %q", row.PreviewID, previewID)
	}
	if row.OwnerID != "owner42" {
		t.Errorf("owner_id: got %q, want %q", row.OwnerID, "owner42")
	}
	if row.ServiceType != "chats" {
		t.Errorf("service_type: got %q, want %q", row.ServiceType, "chats")
	}
}
