// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// db_start.go implements StartDBAsync — the SHARED thumbnail DB
// background startup helper.
//
// It used to live as two unexported funcs in CE's cmd/carbonio-preview/main.go
// (initDBWithRetry + openAndMigrateDB) plus the DSN-empty/else branch in
// main() itself. Because it was private to package main, the Advanced binary
// (a separate repo that imports this CE server package) could not call it and
// silently never started its background AsyncWorker. Moving the whole thing
// here as an exported method means CE's main and Advanced's main call the
// EXACT same code and can never diverge again.
//
// Behaviour, log lines, and backoff values are IDENTICAL to the original CE
// main.go implementation.

import (
	"context"
	"log/slog"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/db"
)

const (
	// dbInitialBackoff is the first retry delay after a failed DB open/
	// migrate attempt.
	dbInitialBackoff = 1 * time.Second
	// dbMaxBackoff caps the exponential backoff between retries.
	dbMaxBackoff = 60 * time.Second
)

// StartDBAsync wires up the thumbnail DB the same way for every
// caller: if cfg.DBDSN is empty it logs a WARN and returns immediately — no
// goroutine is spawned, thumbnails are generated per request without persistence, and
// image/pdf/document/health are unaffected. Otherwise it launches a
// background goroutine that retries opening the pool and running migrations
// with capped exponential backoff (initial 1s, max 60s) until it succeeds or
// ctx is cancelled; on success it calls s.EnableDB (flips the readiness
// gate + starts the async worker). The pool is closed by Run on shutdown, after
// the worker's jobs have returned their rows to PENDING.
//
// Both CE's and Advanced's main should call this right after server.New,
// instead of hand-rolling the DSN-empty check and the retry loop themselves.
func (s *Server) StartDBAsync(ctx context.Context, cfg *config.Config) {
	if cfg.DBDSN == "" {
		// No credentials: run WITHOUT a thumbnail DB. Thumbnails are generated per request and cached
		// in memory only, the worker stays off. A single clear WARN, not a fatal.
		slog.Warn("thumbnail DB not configured; thumbnails are generated per request and never persisted. " +
			"Run carbonio-preview-db-bootstrap to enable persistence and asynchronous generation.")
		return
	}

	// Initialise the DB in the BACKGROUND with capped-backoff retry so a
	// transient failure (e.g. the Consul mesh upstream not yet up at boot — the
	// race that previously caused a FATAL + systemd restart loop) is absorbed
	// and self-heals with no crash. On success the thumbnail DB is enabled and the
	// worker started; the pool is closed on shutdown.
	ctx, cancel := context.WithCancel(ctx)
	s.dbMu.Lock()
	s.dbCancel = cancel
	s.dbMu.Unlock()
	s.dbInit.Add(1)
	go func() {
		defer s.dbInit.Done()
		s.initDBWithRetry(ctx, cfg)
	}()
}

// initDBWithRetry opens the thumbnail DB pool and runs migrations,
// retrying with capped exponential backoff until it succeeds or ctx is
// cancelled. On success it enables the thumbnail DB on the server (flips the
// readiness gate + starts the worker) and records the pool for stopAsync.
//
// It NEVER exits the process: a persistently-unreachable DB simply means asynchronous
// thumbnails are generated per request without persistence while everything else keeps serving.
func (s *Server) initDBWithRetry(ctx context.Context, cfg *config.Config) {
	backoff := dbInitialBackoff
	attempt := 0
	for {
		attempt++
		store, err := openAndMigrateDB(ctx, cfg)
		if err == nil {
			slog.Info("thumbnail DB ready; enabling asynchronous thumbnails", "attempts", attempt)
			s.dbMu.Lock()
			s.dbPool = store
			s.dbMu.Unlock()
			s.EnableDB(ctx, store)
			return
		}

		if ctx.Err() != nil {
			// Shutting down — stop retrying.
			return
		}
		slog.Warn("thumbnail DB not ready yet; retrying "+
			"(synchronous thumbnails and previews unaffected, asynchronous thumbnails return failed-dependency until DB is up)",
			"attempt", attempt, "retry_in", backoff.String(), "err", err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < dbMaxBackoff {
			backoff *= 2
			if backoff > dbMaxBackoff {
				backoff = dbMaxBackoff
			}
		}
	}
}

// openAndMigrateDB opens the pool (with a ping) and applies migrations.
// Both steps are bounded by a per-attempt timeout so a hung connect cannot
// wedge the retry loop.
func openAndMigrateDB(ctx context.Context, cfg *config.Config) (*db.Store, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	store, err := db.New(attemptCtx, cfg.DBDSN, db.PoolConfig{
		MaxConns:        cfg.DBPoolMaxConns,
		MinConns:        cfg.DBPoolMinConns,
		MaxConnLifetime: time.Duration(cfg.DBConnMaxLifetimeMs) * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(attemptCtx); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}
