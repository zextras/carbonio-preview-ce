// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// videodb_start.go implements StartVideoDBAsync — the SHARED video-preview DB
// background startup helper.
//
// It used to live as two unexported funcs in CE's cmd/carbonio-preview/main.go
// (initVideoDBWithRetry + openAndMigrateDB) plus the DSN-empty/else branch in
// main() itself. Because it was private to package main, the Advanced binary
// (a separate repo that imports this CE server package) could not call it and
// silently never started its background VideoWorker. Moving the whole thing
// here as an exported method means CE's main and Advanced's main call the
// EXACT same code and can never diverge again.
//
// Behaviour, log lines, and backoff values are IDENTICAL to the original CE
// main.go implementation.

import (
	"context"
	"log/slog"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/db"
)

const (
	// videoDBInitialBackoff is the first retry delay after a failed DB open/
	// migrate attempt.
	videoDBInitialBackoff = 1 * time.Second
	// videoDBMaxBackoff caps the exponential backoff between retries.
	videoDBMaxBackoff = 60 * time.Second
)

// StartVideoDBAsync wires up the video-preview DB the same way for every
// caller: if cfg.DBDSN is empty it logs a WARN and returns immediately — no
// goroutine is spawned, video previews stay disabled (424) forever, and
// image/pdf/document/health are unaffected. Otherwise it launches a
// background goroutine that retries opening the pool and running migrations
// with capped exponential backoff (initial 1s, max 60s) until it succeeds or
// ctx is cancelled; on success it calls s.EnableVideoDB (flips the readiness
// gate + starts the video worker) and closes the pool when ctx is done.
//
// Both CE's and Advanced's main should call this right after server.New,
// instead of hand-rolling the DSN-empty check and the retry loop themselves.
func (s *Server) StartVideoDBAsync(ctx context.Context, cfg *config.Config) {
	if cfg.DBDSN == "" {
		// No credentials: run WITHOUT a video DB. Video previews return 424
		// (unsupported/dependency-down), the worker stays off; all other previews
		// and health are unaffected. A single clear WARN — not a fatal.
		slog.Warn("video-preview DB not configured; video previews disabled " +
			"(returning failed-dependency); image/pdf/document previews and health unaffected. " +
			"Run carbonio-preview-db-bootstrap to enable video previews.")
		return
	}

	// Initialise the DB in the BACKGROUND with capped-backoff retry so a
	// transient failure (e.g. the Consul mesh upstream not yet up at boot — the
	// race that previously caused a FATAL + systemd restart loop) is absorbed
	// and self-heals with no crash. On success the video DB is enabled and the
	// worker started; the pool is closed on shutdown.
	go s.initVideoDBWithRetry(ctx, cfg)
}

// initVideoDBWithRetry opens the video-preview DB pool and runs migrations,
// retrying with capped exponential backoff until it succeeds or ctx is
// cancelled. On success it enables the video DB on the server (flips the
// readiness gate + starts the worker) and closes the pool when ctx is done.
//
// It NEVER exits the process: a persistently-unreachable DB simply means video
// previews stay disabled (424) while everything else keeps serving.
func (s *Server) initVideoDBWithRetry(ctx context.Context, cfg *config.Config) {
	backoff := videoDBInitialBackoff
	attempt := 0
	for {
		attempt++
		store, err := openAndMigrateVideoDB(ctx, cfg)
		if err == nil {
			slog.Info("video-preview DB ready; enabling video previews", "attempts", attempt)
			s.EnableVideoDB(ctx, store)
			// Close the pool on shutdown.
			go func() {
				<-ctx.Done()
				store.Close()
			}()
			return
		}

		if ctx.Err() != nil {
			// Shutting down — stop retrying.
			return
		}
		slog.Warn("video-preview DB not ready yet; retrying "+
			"(other previews unaffected, video returns failed-dependency until DB is up)",
			"attempt", attempt, "retry_in", backoff.String(), "err", err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < videoDBMaxBackoff {
			backoff *= 2
			if backoff > videoDBMaxBackoff {
				backoff = videoDBMaxBackoff
			}
		}
	}
}

// openAndMigrateVideoDB opens the pool (with a ping) and applies migrations.
// Both steps are bounded by a per-attempt timeout so a hung connect cannot
// wedge the retry loop.
func openAndMigrateVideoDB(ctx context.Context, cfg *config.Config) (*db.Store, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	store, err := db.New(attemptCtx, cfg.DBDSN, db.PoolConfig{
		MaxConns:        cfg.DBPoolMaxConns,
		MinConns:        cfg.DBPoolMinConns,
		MaxConnLifetime: time.Duration(cfg.DBConnMaxLifetime) * time.Second,
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
