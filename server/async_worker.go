// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// async_worker.go implements the AsyncWorker — a background sweeper that drives
// asynchronous thumbnail generation (video first frame, office first page). It
// is a port of WSC's VideoPreviewWorker.java + VideoPreviewServiceImpl.processOne/runGenerate.
//
// Key design choices:
//   - Per-process instanceID (UUID) generated once at construction, used to
//     atomically claim and guard all DB transitions (prevents split-brain between
//     concurrent preview instances).
//   - Deps.AsyncSem bounds only the CPU-heavy steps (ffmpeg probe + first-frame
//     extraction, the Collabora conversion of an office job), so their
//     concurrency is ONE number shared between the worker and any immediate
//     on-request generation attempts in the GET handlers. Source download,
//     blob upload and DB updates take no slot.
//   - A job is the set of rows of one (file_id, version) claimed together: one
//     or two sizes of the same kind.
//   - Live jobs on this instance (claimed rows, whether downloading, waiting for
//     the ffmpeg slot or extracting) are bounded by maxInFlight.
//   - Only render failures (codec probe, frame extraction, extraction ceiling,
//     Collabora or vips errors) count attempts and end in FAILED at async.max-attempts; stale reclaim also
//     increments attempts. A connectivity failure (source download, frame upload,
//     temp file) never blocks a preview: the row is deleted as if it had never
//     been requested and the next user request starts over. A source that is
//     gone from storage (404) is FAILED for good.

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

// readIdleTimeout is the maximum silence between successive bytes on the source
// download.  A download that stalls for longer than this is cancelled and the
// row dropped so the next user request starts over.
//
// This is a package variable (not a const) so tests can override it to a tiny
// value (e.g. 50ms) for speed without touching any config/KV knob.
var readIdleTimeout = 60 * time.Second

// heartbeatInterval is how often the worker calls db.Store.Heartbeat for the
// whole attempt, to refresh claimed_at.
// Must be well below staleTTL (900s) so that a legitimately slow download, a
// job waiting for an async slot or a long Collabora conversion is never seen as
// stale by ReclaimStale.
//
// Frequency is intentionally network-speed-independent: it is purely a DB
// touch to prove liveness.  A download that is reading bytes at ANY speed keeps
// claimed_at fresh; only a truly stalled download (caught by readIdleTimeout)
// stops heartbeating.
//
// This is a package variable so tests can override it without config/KV knobs.
var heartbeatInterval = 60 * time.Second

// extractCeiling is the maximum time allowed for the POST-DOWNLOAD local stages
// (codec probe + first-frame extract).  These stages operate on a LOCAL
// temp file: decoding a single frame is file-size- and network-independent and
// should complete in seconds.  If it takes longer than extractCeiling, ffmpeg
// is broken/hung — NOT slow because of a large file.
//
// >120s extracting one frame from a local file = unequivocal process breakage.
// The ceiling is deliberately NOT applied to the download phase (that is the
// idle-watchdog's job) nor to the frame upload (bounded by the storage client's
// inactivity timeout).
//
// This is a package variable so tests can override it without config/KV knobs.
var extractCeiling = 120 * time.Second

// ---------------------------------------------------------------------------
// Constants (ported from VideoPreviewWorker.java + VideoPreviewServiceImpl.java)
// ---------------------------------------------------------------------------

const (
	// releaseTimeout bounds the DB update that returns a row whose context already ended.
	releaseTimeout = 5 * time.Second

	// maxInFlight is the maximum number of live generate jobs on this instance.
	// Matches WSC's MAX_IN_FLIGHT = 64.
	maxInFlight = 64

	// defaultSweepIntervalSeconds is the KV default for async.poll-interval-seconds.
	// Matches WSC's SWEEP_INTERVAL = 15s.
	defaultSweepIntervalSeconds = 15

	// defaultStaleTTLSeconds is the KV default for async.stuck-job-timeout-seconds.
	// Matches WSC's STALE_TTL = 15 minutes (900s). Note: the spec said 3600 but
	// the Java source uses 15 minutes; we follow the Java to keep identical semantics.
	defaultStaleTTLSeconds = 900

	// defaultMaxAttempts is the KV default for async.max-attempts.
	// 3 total attempts before a job is marked terminal FAILED (corruption / ffmpeg failure path).
	defaultMaxAttempts = 3
)

// ---------------------------------------------------------------------------
// AsyncWorker
// ---------------------------------------------------------------------------

// AsyncWorker drives background asynchronous thumbnail generation. Construct with
// NewAsyncWorker and launch with Start. Safe to share across goroutines.
type AsyncWorker struct {
	deps       Deps
	instanceID string

	// config knobs (read once at construction from deps.Cfg)
	sweepInterval time.Duration
	staleTTL      time.Duration
	maxAttempts   int

	// live is the per-instance set of jobs whose goroutine is still running.
	// Key format: fileID + "\x00" + strconv.Itoa(version).
	// Guards against ReclaimStale re-spawning a row whose goroutine is alive
	// on this instance, and bounds the live jobs by maxInFlight.
	mu   sync.Mutex
	live map[string]struct{}

	// jobs counts every reserved job plus the sweep and cleanup loops; closed
	// (under mu) stops new ones once Wait is called. jobCtx is the context of
	// Start, on which jobs started from GET requests run.
	jobs   sync.WaitGroup
	closed bool

	slotWaiters atomic.Int32
	jobCtx      context.Context

	// started guards StartOnce so the sweep goroutine is launched at most once
	// even if EnableDB is called multiple times (e.g. background init retry
	// races). 0 = not started, 1 = started.
	started atomic.Bool
}

// db resolves the current thumbnail DB store via the readiness gate (or the
// static deps.DB fallback in tests). Returns nil while the DB is not ready.
// Every worker DB access goes through this so the store can flip after boot.
func (w *AsyncWorker) db() *db.Store {
	return w.deps.dbStore()
}

// NewAsyncWorker creates a AsyncWorker from deps. cfg fields
// AsyncPollIntervalSeconds/AsyncStuckJobTimeoutSeconds/AsyncMaxAttempts are read from
// deps.Cfg; zero values fall back to the Java-matching defaults above.
// Concurrent ffmpeg work is controlled by deps.AsyncSem (sized by AsyncConcurrency).
//
// instanceID is a fresh UUID minted per-process, used to own DB rows atomically.
func NewAsyncWorker(deps Deps) *AsyncWorker {
	sweepSec := defaultSweepIntervalSeconds
	if deps.Cfg != nil && deps.Cfg.AsyncPollIntervalSeconds > 0 {
		sweepSec = deps.Cfg.AsyncPollIntervalSeconds
	}

	staleSec := defaultStaleTTLSeconds
	if deps.Cfg != nil && deps.Cfg.AsyncStuckJobTimeoutSeconds > 0 {
		staleSec = deps.Cfg.AsyncStuckJobTimeoutSeconds
	}

	maxAttempts := defaultMaxAttempts
	if deps.Cfg != nil && deps.Cfg.AsyncMaxAttempts > 0 {
		maxAttempts = deps.Cfg.AsyncMaxAttempts
	}

	return &AsyncWorker{
		deps:          deps,
		instanceID:    uuid.New().String(),
		sweepInterval: time.Duration(sweepSec) * time.Second,
		staleTTL:      time.Duration(staleSec) * time.Second,
		maxAttempts:   maxAttempts,
		live:          make(map[string]struct{}),
	}
}

// StartOnce launches the sweep goroutine at most once, regardless of how many
// times it is called. Safe to call from the background DB-init goroutine (which
// may retry) once the DB becomes ready.
func (w *AsyncWorker) StartOnce(ctx context.Context) {
	if w.started.CompareAndSwap(false, true) {
		w.Start(ctx)
	}
}

// Start launches the background sweep goroutine. It stops when ctx is
// cancelled. Returns immediately.
func (w *AsyncWorker) Start(ctx context.Context) {
	w.bind(ctx)
	if w.track() {
		go func() {
			defer w.jobs.Done()
			w.runCleanupLoop(ctx)
		}()
	}
	if !w.track() {
		return
	}
	go func() {
		defer w.jobs.Done()
		ticker := time.NewTicker(w.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.tick(ctx)
			case <-ctx.Done():
				slog.Info("AsyncWorker: context cancelled, stopping sweep")
				return
			}
		}
	}()
	slog.Info("AsyncWorker started",
		"instance_id", w.instanceID,
		"sweep_interval", w.sweepInterval,
		"stale_ttl", w.staleTTL,
		"max_attempts", w.maxAttempts,
	)
}

func (w *AsyncWorker) bind(ctx context.Context) {
	w.mu.Lock()
	w.jobCtx = ctx
	w.mu.Unlock()
}

func (w *AsyncWorker) jobContext() context.Context {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.jobCtx
}

func (w *AsyncWorker) track() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.jobs.Add(1)
	return true
}

// Wait blocks until the sweep, the cleanup loop and every job have finished and
// refuses new ones. Cancel the context given to Start first: each job then
// returns its row to PENDING and ends.
func (w *AsyncWorker) Wait() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.jobs.Wait()
}

// tick is the body of one sweep cycle. It is also called directly from the
// resolve() fast-path (non-blocking, best-effort).
func (w *AsyncWorker) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("AsyncWorker: tick panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	// DB not ready (absent creds, or a transient outage after boot): skip this
	// sweep entirely. The worker self-resumes on the next tick once the gate is
	// ready, with no restart.
	if w.db() == nil {
		return
	}

	// (a) PENDING newest-first: fetch up to MAX_IN_FLIGHT*4 candidates and claim
	//     them while fewer than MAX_IN_FLIGHT jobs are live on this instance.
	//     The two sizes of one file version are one job.
	rows, err := w.db().FindPendingNewest(ctx, maxInFlight*4)
	if err != nil {
		slog.Warn("AsyncWorker: FindPendingNewest failed", "err", err)
	} else {
		for _, row := range rows {
			key := liveKey(row.FileID, row.Version)
			if r := w.reserve(key); r == reserveFull || r == reserveClosed {
				break
			} else if r == reserveDuplicate {
				continue
			}
			claimCtx, cancelClaim := dbCtx(ctx)
			claimed, cerr := w.db().Claim(claimCtx, row.FileID, row.Version, w.instanceID)
			cancelClaim()
			if cerr != nil {
				slog.Warn("AsyncWorker: Claim failed", "file_id", row.FileID, "version", row.Version, "err", cerr)
			}
			if cerr != nil || len(claimed) == 0 {
				w.unreserve(key)
				continue
			}
			go func() {
				defer w.unreserve(key)
				w.attempt(ctx, claimed)
			}()
		}
	}

	// (b) Stale GENERATING reclaim (crash recovery) — only when not fully saturated.
	if w.liveCount() >= maxInFlight {
		return
	}
	reclaimCtx, cancelReclaim := dbCtx(ctx)
	staleRows, serr := w.db().ReclaimStale(reclaimCtx, w.instanceID, w.staleTTL)
	cancelReclaim()
	if serr != nil {
		slog.Warn("AsyncWorker: ReclaimStale failed", "err", serr)
		return
	}
	for _, group := range groupByJob(staleRows) {
		first := group[0]
		// LIVE-SET CHECK FIRST — before any cap/MarkFailed logic.
		//
		// If a goroutine for this job is still alive on this instance, leave the
		// rows in GENERATING untouched.  Do NOT call Release (→ PENDING) here:
		//   • Release would let the PENDING sweep immediately re-claim the rows,
		//     creating a duplicate attempt.
		//   • The live goroutine's own terminal transition (MarkReady / MarkFailed /
		//     ReleaseWithAttempt) is the authoritative outcome; interfering with it
		//     would either no-op (wrong claimed_by) or cause a race.
		//   • With the heartbeat, the idle-watchdog (60s) and extractCeiling (120s),
		//     no LIVE job should ever legitimately reach staleTTL (900s).  This guard
		//     is defence-in-depth for delayed-kill edges only.
		staleKey := liveKey(first.FileID, first.Version)
		w.mu.Lock()
		_, isLive := w.live[staleKey]
		w.mu.Unlock()
		if isLive {
			slog.Debug("AsyncWorker: stale-reclaim skipped (goroutine still alive)",
				"file_id", first.FileID, "version", first.Version)
			continue
		}
		// Rows are genuinely dead (crashed instance or this instance lost the goroutine).
		// ReclaimStale already incremented attempts. If cap is reached, mark FAILED.
		if maxAttempts(group) >= w.maxAttempts {
			slog.Warn("AsyncWorker: max attempts reached after stale reclaim, marking FAILED",
				"file_id", first.FileID, "version", first.Version, "max_attempts", w.maxAttempts)
			failCtx, cancelFail := dbCtx(ctx)
			ferr := w.db().MarkFailed(failCtx, first.FileID, first.Version, w.instanceID)
			cancelFail()
			if ferr != nil {
				slog.Warn("AsyncWorker: MarkFailed (stale cap) failed", "err", ferr)
			}
			continue
		}
		if ctx.Err() != nil {
			w.releaseRows(ctx, first.FileID, first.Version)
			continue
		}
		switch w.reserve(staleKey) {
		case reserveFull, reserveClosed, reserveDuplicate:
			slog.Debug("AsyncWorker: instance at maxInFlight on stale-reclaimed rows, releasing without attempt increment",
				"file_id", first.FileID, "version", first.Version)
			w.releaseRows(ctx, first.FileID, first.Version)
			continue
		}
		job := group
		go func() {
			defer w.unreserve(staleKey)
			w.attempt(ctx, job)
		}()
	}
}

func groupByJob(rows []db.Thumbnail) [][]db.Thumbnail {
	var groups [][]db.Thumbnail
	index := map[string]int{}
	for _, r := range rows {
		k := liveKey(r.FileID, r.Version)
		i, ok := index[k]
		if !ok {
			i = len(groups)
			index[k] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], r)
	}
	return groups
}

func maxAttempts(rows []db.Thumbnail) int {
	m := 0
	for _, r := range rows {
		m = max(m, r.Attempts)
	}
	return m
}

type reserveResult int

const (
	reserveOK reserveResult = iota
	reserveFull
	reserveDuplicate
	reserveClosed
)

func (w *AsyncWorker) liveCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.live)
}

func (w *AsyncWorker) reserve(key string) reserveResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.live[key]; ok {
		return reserveDuplicate
	}
	if w.closed {
		return reserveClosed
	}
	if len(w.live) >= maxInFlight {
		return reserveFull
	}
	w.live[key] = struct{}{}
	w.jobs.Add(1)
	return reserveOK
}

func (w *AsyncWorker) unreserve(key string) {
	w.mu.Lock()
	delete(w.live, key)
	w.mu.Unlock()
	w.jobs.Done()
}

// dbCtx keeps DB updates alive after the worker context ended, bounded by releaseTimeout.
func dbCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
}

// runOne claims the rows of (fileID, version) and runs one attempt on the
// worker context. A worker that is not running, a full instance or a lost claim
// leaves the rows for the next sweep tick.
func (w *AsyncWorker) runOne(fileID string, version int) {
	store := w.db()
	ctx := w.jobContext()
	if store == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	key := liveKey(fileID, version)
	if w.reserve(key) != reserveOK {
		return
	}
	claimCtx, cancel := dbCtx(ctx)
	claimed, err := store.Claim(claimCtx, fileID, version, w.instanceID)
	cancel()
	if err != nil {
		slog.Warn("AsyncWorker: Claim failed", "file_id", fileID, "version", version, "err", err)
	}
	if err != nil || len(claimed) == 0 {
		w.unreserve(key)
		return
	}
	go func() {
		defer w.unreserve(key)
		w.attempt(ctx, claimed)
	}()
}

// releaseRows returns the rows of a job this instance claimed to PENDING without counting an attempt.
func (w *AsyncWorker) releaseRows(ctx context.Context, fileID string, version int) {
	c, cancel := dbCtx(ctx)
	defer cancel()
	if rerr := w.db().Release(c, fileID, version, w.instanceID); rerr != nil {
		slog.Warn("AsyncWorker: Release failed", "file_id", fileID, "version", version, "err", rerr)
	}
}

// dropRows handles a connectivity failure (source download, blob upload, temp
// file, Collabora unreachable): the GENERATING rows owned by this instance are
// deleted as if the thumbnail had never been requested, so the next user
// request starts over. No attempt is counted and no backoff applies.
func (w *AsyncWorker) dropRows(ctx context.Context, fileID string, version int, err error) {
	slog.Warn("AsyncWorker: connectivity failure, dropping rows",
		"file_id", fileID, "version", version, "err", err)
	c, cancel := dbCtx(ctx)
	defer cancel()
	if derr := w.db().DeleteClaimed(c, fileID, version, w.instanceID); derr != nil {
		slog.Warn("AsyncWorker: DeleteClaimed failed", "file_id", fileID, "version", version, "err", derr)
	}
}

// releaseOrFail handles a render failure: it increments attempts and either
// releases to PENDING or marks FAILED at cap.
// The triggering error is logged here (in full, untruncated) for observability;
// it is never persisted to the DB.
func (w *AsyncWorker) releaseOrFail(ctx context.Context, rows []db.Thumbnail, err error) {
	ctx, cancel := dbCtx(ctx)
	defer cancel()
	first := rows[0]
	slog.Warn("AsyncWorker: render failure, releasing to PENDING with attempt increment",
		"file_id", first.FileID, "version", first.Version, "err", err,
		"attempts_after", maxAttempts(rows)+1)
	if maxAttempts(rows)+1 >= w.maxAttempts {
		slog.Warn("AsyncWorker: max attempts reached, marking FAILED",
			"file_id", first.FileID, "version", first.Version, "max_attempts", w.maxAttempts, "err", err)
		if merr := w.db().MarkFailed(ctx, first.FileID, first.Version, w.instanceID); merr != nil {
			slog.Warn("AsyncWorker: MarkFailed (transient cap) failed", "err", merr)
		}
	} else {
		if rerr := w.db().ReleaseWithAttempt(ctx, first.FileID, first.Version, w.instanceID); rerr != nil {
			slog.Warn("AsyncWorker: ReleaseWithAttempt failed", "err", rerr)
		}
	}
}

// acquireSem blocks until a AsyncSem slot is free or ctx is done; nil AsyncSem is unlimited.
func (w *AsyncWorker) acquireSem(ctx context.Context) error {
	if w.deps.AsyncSem == nil {
		return nil
	}
	w.slotWaiters.Add(1)
	defer w.slotWaiters.Add(-1)
	select {
	case w.deps.AsyncSem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-w.deps.AsyncSem
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseSem releases one slot back to AsyncSem. No-op when AsyncSem is nil.
func (w *AsyncWorker) releaseSem() {
	if w.deps.AsyncSem == nil {
		return
	}
	<-w.deps.AsyncSem
}

// isStorageNotFound returns true if err wraps storage.ErrNotFound.
func isStorageNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// liveKey returns the map key used to track a running goroutine in AsyncWorker.live.
func liveKey(fileID string, version int) string {
	return fileID + "\x00" + strconv.Itoa(version)
}
