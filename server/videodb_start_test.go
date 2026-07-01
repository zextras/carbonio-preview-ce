// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// videodb_start_test.go covers StartVideoDBAsync — the shared helper that
// encapsulates the video-preview DB background startup (capped-backoff retry
// + gate flip + worker start) so both CE's and Advanced's main can call the
// exact same code and never diverge.
package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/cache"
)

// startVideoPostgresDSN spins up a throwaway Postgres 16 container (mirroring
// startVideoPostgres in video_api_test.go) but returns the raw DSN instead of
// an already-opened *db.Store, so StartVideoDBAsync can perform its OWN
// db.New + Migrate exactly as it does in production.
func startVideoPostgresDSN(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH — skipping video DB integration tests")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable (%v): %s", err, out)
	}

	const (
		pgUser  = "preview"
		pgPass  = "preview"
		pgDB    = "preview_video_test"
		pgPort  = "5432"
		imgName = "postgres:16-alpine"
	)

	out, err := exec.Command("docker", "run", "--rm", "-d",
		"-e", "POSTGRES_USER="+pgUser,
		"-e", "POSTGRES_PASSWORD="+pgPass,
		"-e", "POSTGRES_DB="+pgDB,
		"-P",
		imgName,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v — %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
	})

	portOut, err := exec.Command(
		"docker", "inspect",
		"--format", fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%s/tcp") 0).HostPort}}`, pgPort),
		containerID,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect port: %v — %s", err, portOut)
	}
	hostPort := strings.TrimSpace(string(portOut))

	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%s/%s?sslmode=disable",
		pgUser, pgPass, hostPort, pgDB)
}

// pollUntil polls cond every interval until it returns true or timeout elapses.
// Returns false on timeout. Deterministic: no fixed sleeps beyond the poll tick.
func pollUntil(timeout, interval time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// TestStartVideoDBAsync_EmptyDSN_NoWorkerNoGate verifies that with no DSN
// configured, StartVideoDBAsync returns promptly (synchronously — no
// goroutine spawned), the gate stays not-ready, and the worker is never
// started. The video path must keep returning 424 forever, not just until a
// retry eventually happens.
func TestStartVideoDBAsync_EmptyDSN_NoWorkerNoGate(t *testing.T) {
	cfg := testCfg()
	cfg.DBDSN = ""

	s := New(cfg, &mockStore{blob: []byte("src")}, cache.New(0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.StartVideoDBAsync(ctx, cfg)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartVideoDBAsync with empty DSN must return promptly, it blocked")
	}

	if s.dbGate.Ready() {
		t.Error("gate must stay not-ready when DBDSN is empty")
	}

	// buildMux constructs s.worker; StartVideoDBAsync itself must not have
	// started it (StartOnce is idempotent so re-invoking here would only prove
	// harmlessness, not absence — check the "started" flag directly instead).
	_ = s.buildMux(nil)
	if s.worker != nil && s.worker.started.Load() {
		t.Error("worker must not be started when DBDSN is empty")
	}

	// Give any stray goroutine a moment to misbehave, then confirm the gate is
	// still not ready — proves no background retry loop was left running.
	time.Sleep(50 * time.Millisecond)
	if s.dbGate.Ready() {
		t.Error("gate flipped ready with no DSN — a background goroutine must have leaked")
	}
}

// TestStartVideoDBAsync_ValidDSN_EnablesGateAndWorker verifies the success
// path end-to-end against a real Postgres: after the async init completes,
// the readiness gate is set (EnableVideoDB fired) AND the video worker has
// been started, matching the current CE main.go behaviour exactly.
func TestStartVideoDBAsync_ValidDSN_EnablesGateAndWorker(t *testing.T) {
	dsn := startVideoPostgresDSN(t)

	cfg := testCfg()
	cfg.DBDSN = dsn
	cfg.DBPoolMaxConns = 5

	s := New(cfg, &mockStore{blob: []byte("src")}, cache.New(0))
	// buildMux constructs s.worker (gate-aware) exactly like production's
	// srv.Handler()/Run() does before the DB becomes ready.
	_ = s.buildMux(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.StartVideoDBAsync(ctx, cfg)

	ok := pollUntil(15*time.Second, 100*time.Millisecond, func() bool {
		return s.dbGate.Ready()
	})
	if !ok {
		t.Fatal("gate never became ready within timeout — StartVideoDBAsync did not enable the video DB")
	}

	if s.worker == nil {
		t.Fatal("worker must exist (constructed by buildMux)")
	}
	if !s.worker.started.Load() {
		t.Error("worker must be started once the DB becomes ready")
	}

	// Cancel and confirm the pool close goroutine doesn't panic / hang the test.
	cancel()
}

// TestStartVideoDBAsync_ConfigValues_MatchMainGo pins the retry backoff
// constants used inside StartVideoDBAsync to the values previously hardcoded
// in cmd/carbonio-preview/main.go's initVideoDBWithRetry (initial 1s, max
// 60s), so a future edit cannot silently change production retry behaviour
// without a test failing.
func TestStartVideoDBAsync_ConfigValues_MatchMainGo(t *testing.T) {
	if videoDBInitialBackoff != 1*time.Second {
		t.Errorf("videoDBInitialBackoff = %v, want 1s", videoDBInitialBackoff)
	}
	if videoDBMaxBackoff != 60*time.Second {
		t.Errorf("videoDBMaxBackoff = %v, want 60s", videoDBMaxBackoff)
	}
}
