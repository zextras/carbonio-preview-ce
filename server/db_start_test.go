// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// db_start_test.go covers StartDBAsync — the shared helper that
// encapsulates the video-preview DB background startup (capped-backoff retry
// + gate flip + worker start) so both CE's and Advanced's main can call the
// exact same code and never diverge.
package server

import (
	"context"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
)

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

// TestStartDBAsync_EmptyDSN_NoWorkerNoGate verifies that with no DSN
// configured, StartDBAsync returns promptly (synchronously — no
// goroutine spawned), the gate stays not-ready, and the worker is never
// started. The video path must keep returning 424 forever, not just until a
// retry eventually happens.
func TestStartDBAsync_EmptyDSN_NoWorkerNoGate(t *testing.T) {
	cfg := testCfg()
	cfg.DBDSN = ""

	s := New(cfg, &mockStore{blob: []byte("src")}, cache.New(0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.StartDBAsync(ctx, cfg)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartDBAsync with empty DSN must return promptly, it blocked")
	}

	if s.dbGate.Ready() {
		t.Error("gate must stay not-ready when DBDSN is empty")
	}

	// buildMux constructs s.worker; StartDBAsync itself must not have
	// started it (StartOnce is idempotent so re-invoking here would only prove
	// harmlessness, not absence — check the "started" flag directly instead).
	_ = s.buildMux()
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

// TestStartDBAsync_ValidDSN_EnablesGateAndWorker verifies the success
// path end-to-end against a real Postgres: after the async init completes,
// the readiness gate is set (EnableDB fired) AND the video worker has
// been started, matching the current CE main.go behaviour exactly.
func TestStartDBAsync_ValidDSN_EnablesGateAndWorker(t *testing.T) {
	dsn := startPostgresDSN(t)

	cfg := testCfg()
	cfg.DBDSN = dsn
	cfg.DBPoolMaxConns = 5

	s := New(cfg, &mockStore{blob: []byte("src")}, cache.New(0))
	// buildMux constructs s.worker (gate-aware) exactly like production's
	// srv.Handler()/Run() does before the DB becomes ready.
	_ = s.buildMux()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.StartDBAsync(ctx, cfg)

	ok := pollUntil(15*time.Second, 100*time.Millisecond, func() bool {
		return s.dbGate.Ready()
	})
	if !ok {
		t.Fatal("gate never became ready within timeout — StartDBAsync did not enable the video DB")
	}

	if s.worker == nil {
		t.Fatal("worker must exist (constructed by buildMux)")
	}
	if !s.worker.started.Load() {
		t.Error("worker must be started once the DB becomes ready")
	}

	stopAsyncChecked(t, s)
}

// TestStartDBAsync_ConfigValues_MatchMainGo pins the retry backoff
// constants used inside StartDBAsync to the values previously hardcoded
// in cmd/carbonio-preview/main.go's initDBWithRetry (initial 1s, max
// 60s), so a future edit cannot silently change production retry behaviour
// without a test failing.
func TestStartDBAsync_ConfigValues_MatchMainGo(t *testing.T) {
	if dbInitialBackoff != 1*time.Second {
		t.Errorf("dbInitialBackoff = %v, want 1s", dbInitialBackoff)
	}
	if dbMaxBackoff != 60*time.Second {
		t.Errorf("dbMaxBackoff = %v, want 60s", dbMaxBackoff)
	}
}

// TestStartDBAsync_DBReadyBeforeBuildMux_WorkerStillStarts is the
// REGRESSION test for the real production race: main.go's ordering is
//
//	srv := server.New(...)          // s.worker is nil
//	srv.StartDBAsync(ctx, cfg) // background: DB opens+migrates, calls EnableDB
//	srv.Run()                       // calls buildMux(), which constructs s.worker
//
// If the DB becomes ready (EnableDB fires) BEFORE Run()/buildMux()
// constructs the worker, StartAsyncWorker's `if s.worker != nil` check is
// false and the start request is silently dropped. buildMux later assigns
// s.worker, but nothing ever calls StartOnce on it — the sweep goroutine never
// launches and PENDING rows are never processed, even though the DB gate
// reports ready.
//
// This test reproduces that exact ordering (StartDBAsync completes and
// EnableDB fires BEFORE buildMux is called) and asserts the worker is
// nonetheless started. It must FAIL on the pre-fix code (s.worker.started ==
// false, or s.worker == nil at assertion time in the worst timing) and PASS
// once the deferred-start latch is implemented.
func TestStartDBAsync_DBReadyBeforeBuildMux_WorkerStillStarts(t *testing.T) {
	dsn := startPostgresDSN(t)

	cfg := testCfg()
	cfg.DBDSN = dsn
	cfg.DBPoolMaxConns = 5

	s := New(cfg, &mockStore{blob: []byte("src")}, cache.New(0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the async DB init FIRST, with NO worker constructed yet
	// (s.worker == nil at this point, matching main.go's real ordering: New()
	// then StartDBAsync() then Run()).
	s.StartDBAsync(ctx, cfg)

	// Wait for the DB to actually become ready (EnableDB fired) BEFORE
	// building the mux — this is the crux of the race: enable-before-construct.
	ok := pollUntil(15*time.Second, 100*time.Millisecond, func() bool {
		return s.dbGate.Ready()
	})
	if !ok {
		t.Fatal("gate never became ready within timeout — StartDBAsync did not enable the video DB")
	}

	// ONLY NOW construct the worker, exactly like Run()/Handler() calling
	// buildMux after the DB already came up.
	_ = s.buildMux()

	if s.worker == nil {
		t.Fatal("worker must exist (constructed by buildMux)")
	}

	// The worker must end up started even though EnableDB fired before
	// the worker existed. Poll briefly: StartOnce (if reached) launches
	// synchronously, but allow a short grace window for the latch to fire.
	started := pollUntil(2*time.Second, 20*time.Millisecond, func() bool {
		return s.worker.started.Load()
	})
	if !started {
		t.Error("worker was never started: DB became ready BEFORE buildMux constructed the worker, " +
			"and the pending start request was lost — this is the main.go ordering race")
	}

	stopAsyncChecked(t, s)
}

// stopAsyncChecked runs stopAsync and checks that it returns (the worker's
// context was cancelled and its jobs ended), the worker is closed and the pool
// recorded by the DB init is closed.
func stopAsyncChecked(t *testing.T, s *Server) {
	t.Helper()
	s.dbMu.Lock()
	store := s.dbPool
	s.dbMu.Unlock()
	if store == nil {
		t.Fatal("StartDBAsync did not record the pool")
	}
	done := make(chan struct{})
	go func() {
		s.stopAsync()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(slotWatchdog):
		t.Fatal("stopAsync did not return: the worker context was not cancelled by it")
	}
	s.worker.mu.Lock()
	closed := s.worker.closed
	s.worker.mu.Unlock()
	if !closed {
		t.Error("worker not closed by stopAsync")
	}
	if _, err := store.FindPair(context.Background(), "x", 1); err == nil {
		t.Error("video DB pool still open after stopAsync")
	}
}
