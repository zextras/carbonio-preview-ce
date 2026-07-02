// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// video_gate.go implements the DB-resilience layer for the video-preview
// endpoints.
//
// carbonio-preview must boot and serve image/pdf/document/health even when the
// video-preview database is absent or its connection is temporarily down (e.g.
// the Consul mesh upstream is not up yet at boot). In that state:
//   - the video preview + thumbnail endpoints return HTTP 424 (Failed
//     Dependency): an OPERATIONAL "the DB dependency is down" signal, distinct
//     from 415 (which means the file's codec is genuinely unsupported and
//     requires a working DB to have been probed);
//   - the video worker does not run;
//   - copy/delete no-op with success so the fire-and-forget WSC caller is happy.
//
// The gate is the single readiness signal consulted at REQUEST time (not at
// construction time), so a DB that comes up after boot re-enables video with no
// process restart. main.go initialises the DB in a background goroutine with
// capped-backoff retry and calls videoGate.Set on success.

import (
	"strings"
	"sync/atomic"

	"github.com/zextras/carbonio-preview-ce/v2/db"
)

// videoGate holds the video-preview DB store behind an atomic pointer so the
// readiness state can flip after boot without racing the request handlers.
//
// A nil store means "video DB not ready" (absent creds, or the background init
// has not yet succeeded). Handlers and the worker must consult Store()/Ready()
// on every use and degrade gracefully when it is nil.
type videoGate struct {
	store atomic.Pointer[db.Store]
}

// newVideoGate returns a gate in the not-ready (nil store) state.
func newVideoGate() *videoGate {
	return &videoGate{}
}

// Set publishes the ready DB store. Passing nil returns the gate to not-ready.
func (g *videoGate) Set(s *db.Store) {
	g.store.Store(s)
}

// Store returns the current DB store, or nil when video is disabled/not ready.
func (g *videoGate) Store() *db.Store {
	return g.store.Load()
}

// Ready reports whether a DB store is currently available.
func (g *videoGate) Ready() bool {
	return g.store.Load() != nil
}

// videoStore resolves the DB store the video path should use for THIS request.
//
// Resolution order:
//  1. If a gate is attached, its current store wins (nil ⇒ not ready ⇒ video
//     disabled). This is the production path: main.go flips the gate from a
//     background retry loop.
//  2. Otherwise fall back to the statically-injected deps.DB. This keeps the
//     existing unit/integration tests (which build Deps{DB: store} and call the
//     handlers directly) working unchanged.
//
// A nil return means "video is not available right now".
func (d Deps) videoStore() *db.Store {
	if d.DBGate != nil {
		return d.DBGate.Store()
	}
	return d.DB
}

// isDBConnError reports whether err looks like a database CONNECTION-level
// failure (pool closed, connection refused/reset, DNS failure, network timeout,
// EOF/broken pipe) as opposed to a logical/query error.
//
// It is intentionally string-based: pgx/pgxpool surface these as a wide variety
// of wrapped concrete types (net.OpError, pgconn errors, puddle "closed pool",
// io.ErrUnexpectedEOF, …) and matching on substrings is far more robust across
// pgx versions than enumerating every concrete type. A connection-level error
// at runtime means the DB dependency is down → the video path degrades to 424
// rather than surfacing a 500/503.
func isDBConnError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"closed pool",
		"conn closed",
		"connection refused",
		"connection reset",
		"broken pipe",
		"no such host",
		"i/o timeout",
		"unexpected eof",
		"eof", // bare EOF (leaf-cert expiry / mesh upstream drop)
		"dial tcp",
		"network is unreachable",
		"no route to host",
		"server closed the connection",
		"failed to connect",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
