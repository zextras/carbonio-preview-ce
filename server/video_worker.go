// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// video_worker.go implements the VideoWorker — a background sweeper that drives
// video first-frame generation. It is a port of WSC's VideoPreviewWorker.java +
// VideoPreviewServiceImpl.processOne/runGenerate.
//
// Key design choices:
//   - Per-process instanceID (UUID) generated once at construction, used to
//     atomically claim and guard all DB transitions (prevents split-brain between
//     concurrent preview instances).
//   - Deps.VideoSem bounds only the ffmpeg work (codec probe + first-frame
//     extraction), so total ffmpeg concurrency is ONE number shared between the
//     worker and any immediate on-request generation attempts in the GET
//     handlers. Source download, frame upload and DB updates take no slot.
//   - Live jobs on this instance (claimed rows, whether downloading, waiting for
//     the ffmpeg slot or extracting) are bounded by maxInFlight.
//   - Only render failures (codec probe, frame extraction, extraction ceiling)
//     count attempts and end in FAILED at video.max-attempts; stale reclaim also
//     increments attempts. A connectivity failure (source download, frame upload,
//     temp file) never blocks a preview: the row is deleted as if it had never
//     been requested and the next user request starts over. A source that is
//     gone from storage (404) is FAILED for good.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
	"github.com/zextras/carbonio-preview-ce/v4/video"
)

// readIdleTimeout is the maximum silence between successive bytes on the source
// download.  A download that stalls for longer than this is cancelled and the
// row dropped so the next user request starts over.
//
// This is a package variable (not a const) so tests can override it to a tiny
// value (e.g. 50ms) for speed without touching any config/KV knob.
var readIdleTimeout = 60 * time.Second

// heartbeatInterval is how often the worker calls db.Store.Heartbeat from the
// start of the download until the ffmpeg slot is acquired, to refresh claimed_at.
// Must be well below staleTTL (900s) so that a legitimately slow download, or a
// job waiting for an ffmpeg slot, is never seen as stale by ReclaimStale.
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

// idleReadCloser wraps a ReadCloser and resets a timer on every successful
// read.  The timer is armed by the caller before the response wait and fires
// dlCancel when no bytes arrive for readIdleTimeout.
// Call Close to stop the timer and release resources.
type idleReadCloser struct {
	rc      io.ReadCloser
	timer   *time.Timer
	timeout time.Duration
}

func newIdleReadCloser(rc io.ReadCloser, timeout time.Duration, timer *time.Timer) *idleReadCloser {
	timer.Reset(timeout)
	return &idleReadCloser{rc: rc, timer: timer, timeout: timeout}
}

func (irc *idleReadCloser) Read(p []byte) (n int, err error) {
	n, err = irc.rc.Read(p)
	if n > 0 {
		irc.timer.Reset(irc.timeout)
	}
	return n, err
}

func (irc *idleReadCloser) Close() error {
	irc.timer.Stop()
	return irc.rc.Close()
}

// ---------------------------------------------------------------------------
// Constants (ported from VideoPreviewWorker.java + VideoPreviewServiceImpl.java)
// ---------------------------------------------------------------------------

const (
	// releaseTimeout bounds the DB update that returns a row whose context already ended.
	releaseTimeout = 5 * time.Second

	// maxInFlight is the maximum number of live generate jobs on this instance.
	// Matches WSC's MAX_IN_FLIGHT = 64.
	maxInFlight = 64

	// defaultSweepIntervalSeconds is the KV default for video.poll-interval-seconds.
	// Matches WSC's SWEEP_INTERVAL = 15s.
	defaultSweepIntervalSeconds = 15

	// defaultStaleTTLSeconds is the KV default for video.stuck-generation-timeout-seconds.
	// Matches WSC's STALE_TTL = 15 minutes (900s). Note: the spec said 3600 but
	// the Java source uses 15 minutes; we follow the Java to keep identical semantics.
	defaultStaleTTLSeconds = 900

	// defaultMaxAttempts is the KV default for video.max-attempts.
	// 3 total attempts before a job is marked terminal FAILED (corruption / ffmpeg failure path).
	defaultMaxAttempts = 3
)

// ---------------------------------------------------------------------------
// VideoWorker
// ---------------------------------------------------------------------------

// VideoWorker drives background video first-frame generation. Construct with
// NewVideoWorker and launch with Start. Safe to share across goroutines.
type VideoWorker struct {
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
	// even if EnableVideoDB is called multiple times (e.g. background init retry
	// races). 0 = not started, 1 = started.
	started atomic.Bool
}

// db resolves the current video-preview DB store via the readiness gate (or the
// static deps.DB fallback in tests). Returns nil while the DB is not ready.
// Every worker DB access goes through this so the store can flip after boot.
func (w *VideoWorker) db() *db.Store {
	return w.deps.videoStore()
}

// NewVideoWorker creates a VideoWorker from deps. cfg fields
// VideoSweepIntervalSeconds/VideoStaleTTLSeconds/VideoMaxAttempts are read from
// deps.Cfg; zero values fall back to the Java-matching defaults above.
// Concurrent ffmpeg work is controlled by deps.VideoSem (sized by VideoConcurrency).
//
// instanceID is a fresh UUID minted per-process, used to own DB rows atomically.
func NewVideoWorker(deps Deps) *VideoWorker {
	sweepSec := defaultSweepIntervalSeconds
	if deps.Cfg != nil && deps.Cfg.VideoSweepIntervalSeconds > 0 {
		sweepSec = deps.Cfg.VideoSweepIntervalSeconds
	}

	staleSec := defaultStaleTTLSeconds
	if deps.Cfg != nil && deps.Cfg.VideoStaleTTLSeconds > 0 {
		staleSec = deps.Cfg.VideoStaleTTLSeconds
	}

	maxAttempts := defaultMaxAttempts
	if deps.Cfg != nil && deps.Cfg.VideoMaxAttempts > 0 {
		maxAttempts = deps.Cfg.VideoMaxAttempts
	}

	return &VideoWorker{
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
func (w *VideoWorker) StartOnce(ctx context.Context) {
	if w.started.CompareAndSwap(false, true) {
		w.Start(ctx)
	}
}

// Start launches the background sweep goroutine. It stops when ctx is
// cancelled. Returns immediately.
func (w *VideoWorker) Start(ctx context.Context) {
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
				slog.Info("VideoWorker: context cancelled, stopping sweep")
				return
			}
		}
	}()
	slog.Info("VideoWorker started",
		"instance_id", w.instanceID,
		"sweep_interval", w.sweepInterval,
		"stale_ttl", w.staleTTL,
		"max_attempts", w.maxAttempts,
	)
}

func (w *VideoWorker) bind(ctx context.Context) {
	w.mu.Lock()
	w.jobCtx = ctx
	w.mu.Unlock()
}

func (w *VideoWorker) jobContext() context.Context {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.jobCtx
}

func (w *VideoWorker) track() bool {
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
func (w *VideoWorker) Wait() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.jobs.Wait()
}

// tick is the body of one sweep cycle. It is also called directly from the
// resolve() fast-path (non-blocking, best-effort).
func (w *VideoWorker) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("VideoWorker: tick panicked", "panic", r, "stack", string(debug.Stack()))
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
	rows, err := w.db().FindPendingNewest(ctx, maxInFlight*4)
	if err != nil {
		slog.Warn("VideoWorker: FindPendingNewest failed", "err", err)
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
				slog.Warn("VideoWorker: Claim failed", "file_id", row.FileID, "version", row.Version, "err", cerr)
			}
			if cerr != nil || !claimed {
				w.unreserve(key)
				continue
			}
			job := row
			go func() {
				defer w.unreserve(key)
				w.attempt(ctx, job)
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
		slog.Warn("VideoWorker: ReclaimStale failed", "err", serr)
		return
	}
	for _, stale := range staleRows {
		// LIVE-SET CHECK FIRST — before any cap/MarkFailed logic.
		//
		// If a goroutine for this row is still alive on this instance, leave the
		// row in GENERATING untouched.  Do NOT call Release (→ PENDING) here:
		//   • Release would let the PENDING sweep immediately re-claim the row,
		//     creating a duplicate attempt.
		//   • The live goroutine's own terminal transition (MarkReady / MarkFailed /
		//     ReleaseWithAttempt) is the authoritative outcome; interfering with it
		//     would either no-op (wrong claimed_by) or cause a race.
		//   • With the download heartbeat + idle-watchdog (60s) + extractCeiling (120s),
		//     no LIVE job should ever legitimately reach staleTTL (900s).  This guard
		//     is defence-in-depth for delayed-kill edges only.
		staleKey := liveKey(stale.FileID, stale.Version)
		w.mu.Lock()
		_, isLive := w.live[staleKey]
		w.mu.Unlock()
		if isLive {
			slog.Debug("VideoWorker: stale-reclaim skipped (goroutine still alive)",
				"file_id", stale.FileID, "version", stale.Version)
			// Leave the row as GENERATING — do NOT call Release.
			// The live goroutine will call its own terminal transition.
			continue
		}
		// Row is genuinely dead (crashed instance or this instance lost the goroutine).
		// ReclaimStale already incremented attempts. If cap is reached, mark FAILED.
		if stale.Attempts >= w.maxAttempts {
			slog.Warn("VideoWorker: max attempts reached after stale reclaim, marking FAILED",
				"file_id", stale.FileID, "version", stale.Version, "max_attempts", w.maxAttempts)
			failCtx, cancelFail := dbCtx(ctx)
			ferr := w.db().MarkFailed(failCtx, stale.FileID, stale.Version, w.instanceID)
			cancelFail()
			if ferr != nil {
				slog.Warn("VideoWorker: MarkFailed (stale cap) failed", "err", ferr)
			}
			continue
		}
		if ctx.Err() != nil {
			w.releaseRow(ctx, stale)
			continue
		}
		switch w.reserve(staleKey) {
		case reserveFull, reserveClosed, reserveDuplicate:
			slog.Debug("VideoWorker: instance at maxInFlight on stale-reclaimed row, releasing without attempt increment",
				"file_id", stale.FileID, "version", stale.Version)
			w.releaseRow(ctx, stale)
			continue
		}
		job := stale
		go func() {
			defer w.unreserve(staleKey)
			w.attempt(ctx, job)
		}()
	}
}

type reserveResult int

const (
	reserveOK reserveResult = iota
	reserveFull
	reserveDuplicate
	reserveClosed
)

func (w *VideoWorker) liveCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.live)
}

func (w *VideoWorker) reserve(key string) reserveResult {
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

func (w *VideoWorker) unreserve(key string) {
	w.mu.Lock()
	delete(w.live, key)
	w.mu.Unlock()
	w.jobs.Done()
}

// dbCtx keeps DB updates alive after the worker context ended, bounded by releaseTimeout.
func dbCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
}

// releaseRow returns a row this instance claimed to PENDING without counting an attempt.
func (w *VideoWorker) releaseRow(ctx context.Context, row db.VideoPreview) {
	c, cancel := dbCtx(ctx)
	defer cancel()
	if rerr := w.db().Release(c, row.FileID, row.Version, w.instanceID); rerr != nil {
		slog.Warn("VideoWorker: Release failed", "file_id", row.FileID, "version", row.Version, "err", rerr)
	}
}

// attempt runs a single generate job for a row this instance has claimed. It
// takes a VideoSem slot only around the codec probe and the first-frame
// extraction, after the source is downloaded.
//
// Probe-first outcome mapping:
//  1. Download source video to a temp file (shared for probe + extract), no slot,
//     then wait for a VideoSem slot; if ctx ends first the row returns to
//     PENDING without an attempt.
//  2. If row.Codec already set (re-enqueued UNSUPPORTED), skip re-probe.
//     Otherwise probe to detect codec and persist via SetCodec.
//  3. probe fails (unreadable / no video stream)  → ReleaseWithAttempt / cap → MarkFailed.
//  4. codec ∉ supported list                      → MarkUnsupported (terminal, no retry).
//  5. codec ∈ supported list, extract + store OK  → MarkReady.
//  6. codec ∈ supported list, extraction fails    → ReleaseWithAttempt / cap → MarkFailed
//     (supported codec but corrupt, or over extractCeiling).
//  7. storage.ErrNotFound (source gone)           → MarkFailed (terminal, no retry).
//  8. connectivity failure (download, upload, temp file) → row deleted, no attempt counted.
func (w *VideoWorker) attempt(ctx context.Context, row db.VideoPreview) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("VideoWorker: attempt panicked",
				"file_id", row.FileID, "version", row.Version, "panic", r,
				"stack", string(debug.Stack()))
			if w.db() != nil {
				w.releaseRow(ctx, row)
			}
		}
	}()

	previewID := uuid.New().String()
	slog.Debug("VideoWorker: attempt started",
		"file_id", row.FileID, "version", row.Version,
		"preview_id", previewID, "instance_id", w.instanceID)

	// --- Step 1: Download source video to a seekable temp file ---
	//
	// The download runs under a separate child context (dlCtx) scoped ONLY to
	// the copy phase.  An idleReadCloser wraps the source body and fires
	// dlCancel if no bytes arrive for readIdleTimeout — cancelling the in-flight
	// transport read so the stalled download is abandoned.
	// After io.Copy returns (success or stall), the watchdog timer is stopped and
	// dlCtx is released; the subsequent probe/extract/store stages run under the
	// original ctx (no watchdog; probe/extract are bounded by extractCeiling below,
	// the upload by the storage client's inactivity timeout).
	//
	// Heartbeat: a separate goroutine fires db.Heartbeat every heartbeatInterval
	// from the download start until the ffmpeg slot is acquired.  This keeps
	// claimed_at fresh so that ReclaimStale never treats a
	// legitimately-slow-but-progressing download, or a job waiting for an ffmpeg
	// slot, as stale.  Frequency is network-speed-independent: it fires on
	// wall-clock time, not on bytes transferred.
	dlCtx, dlCancel := context.WithCancel(ctx)
	idleTimer := time.AfterFunc(readIdleTimeout, dlCancel)
	rc, err := w.deps.Store.RetrieveDataStreaming(dlCtx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		idleTimer.Stop()
		dlCancel()
		if ctx.Err() != nil {
			w.releaseRow(ctx, row)
			return
		}
		if isStorageNotFound(err) {
			slog.Warn("VideoWorker: source blob not found, marking FAILED",
				"file_id", row.FileID, "version", row.Version)
			notFoundCtx, cancelNotFound := dbCtx(ctx)
			defer cancelNotFound()
			if merr := w.db().MarkFailed(notFoundCtx, row.FileID, row.Version, w.instanceID); merr != nil {
				slog.Warn("VideoWorker: MarkFailed (not found) failed", "err", merr)
			}
			return
		}
		w.dropRow(ctx, row, err)
		return
	}

	tmp, err := os.CreateTemp("", "carbonio-preview-worker-*.bin")
	if err != nil {
		rc.Close()
		idleTimer.Stop()
		dlCancel()
		w.dropRow(ctx, row, err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Start download heartbeat goroutine.  Fires db.Heartbeat every
	// heartbeatInterval; stopped through stopHB once the ffmpeg slot is held or
	// the attempt ends.
	hbStop := make(chan struct{})
	stopHB := sync.OnceFunc(func() { close(hbStop) })
	defer stopHB()
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if herr := w.db().Heartbeat(ctx, row.FileID, row.Version, w.instanceID); herr != nil {
					slog.Warn("VideoWorker: download heartbeat failed (non-fatal)", "err", herr,
						"file_id", row.FileID, "version", row.Version)
				} else {
					slog.Debug("VideoWorker: download heartbeat sent",
						"file_id", row.FileID, "version", row.Version)
				}
			case <-hbStop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wrap rc in the idle watchdog for the duration of the copy only.
	guard := newIdleReadCloser(rc, readIdleTimeout, idleTimer)
	// Stream the source into a SPARSE temp file: only the head (frame-0 sample
	// + faststart moov) and the tail (trailing moov/index) are retained; the
	// middle is discarded so a multi-GB source costs ~head+tail of temp disk,
	// not its full size. The full body is still transferred (storage origin
	// does not honor HTTP Range), and the idle-read watchdog + heartbeat still
	// see byte progress through `guard`. If head+tail is insufficient, the
	// probe/extract below fail and this row is treated exactly like a corrupt
	// file (no full-download fallback — deliberate).
	_, copyErr := video.StreamToSparseTemp(tmp, guard)
	// Stop the watchdog and release dlCtx immediately; the heartbeat keeps
	// running until the ffmpeg slot is acquired.
	guard.Close()
	dlCancel()

	if syncErr := tmp.Sync(); syncErr != nil && copyErr == nil {
		copyErr = syncErr
	}
	tmp.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			w.releaseRow(ctx, row)
			return
		}
		slog.Warn("VideoWorker: download failed (possibly stalled idle watchdog)",
			"file_id", row.FileID, "version", row.Version, "err", copyErr)
		w.dropRow(ctx, row, copyErr)
		return
	}

	// --- Steps 2-4: Probe + extract (post-download, local file), under the ffmpeg slot ---
	//
	// These stages operate entirely on the local temp file.  Decoding ONE frame
	// from a local file is size- and network-independent: it should complete in
	// seconds.  If it takes longer than extractCeiling (120s), ffmpeg is broken
	// or hung — NOT legitimately slow because of a large file.  Cancel via ctx
	// cancellation → exec.CommandContext SIGKILLs ffmpeg → returns an error →
	// counted as a render failure via releaseOrFail (NOT UNSUPPORTED).
	//
	// extractCeiling is intentionally NOT applied to the download phase above
	// nor to the wait for the ffmpeg slot.
	if serr := w.acquireSem(ctx); serr != nil {
		stopHB()
		slog.Debug("VideoWorker: context ended while waiting for an ffmpeg slot, releasing without attempt increment",
			"file_id", row.FileID, "version", row.Version)
		w.releaseRow(ctx, row)
		return
	}
	stopHB()
	res := w.runFFmpeg(ctx, row, tmpName)
	if ctx.Err() != nil {
		w.releaseRow(ctx, row)
		return
	}
	stateCtx, stateCancel := dbCtx(ctx)
	defer stateCancel()

	if res.probeErr != nil {
		slog.Warn("VideoWorker: codec probe failed, releasing with attempt increment",
			"file_id", row.FileID, "version", row.Version, "err", res.probeErr)
		w.releaseOrFail(ctx, row, fmt.Errorf("codec probe failed: %w", res.probeErr))
		return
	}
	codec := res.codec
	if res.probed {
		// Persist the codec immediately so it survives future re-enqueue cycles.
		if serr := w.db().SetCodec(stateCtx, row.FileID, row.Version, w.instanceID, codec); serr != nil {
			slog.Warn("VideoWorker: SetCodec failed (non-fatal)", "err", serr)
		}
	}

	// --- Step 3: Check codec support ---
	if !isSupportedVideoCodec(codec) {
		slog.Warn("VideoWorker: codec not in supported list, marking UNSUPPORTED",
			"file_id", row.FileID, "version", row.Version, "codec", codec)
		if merr := w.db().MarkUnsupported(stateCtx, row.FileID, row.Version, w.instanceID); merr != nil {
			slog.Warn("VideoWorker: MarkUnsupported failed", "err", merr)
		}
		return
	}

	if res.extractErr != nil {
		if errors.Is(res.extractErr, video.ErrExtractFailed) || errors.Is(res.extractErr, video.ErrExtractTimeout) {
			slog.Warn("VideoWorker: frame extraction failed for supported codec, releasing with attempt increment",
				"file_id", row.FileID, "version", row.Version, "codec", codec, "err", res.extractErr)
		}
		w.releaseOrFail(ctx, row, res.extractErr)
		return
	}
	jpgBytes := res.jpg

	// --- Step 5: Store the frame (connectivity failure drops the row) ---
	if _, err = storeFrame(ctx, w.deps.Store, jpgBytes, row.Version, row.ServiceType, row.OwnerID, previewID); err != nil {
		if ctx.Err() != nil {
			w.releaseRow(ctx, row)
			return
		}
		w.dropRow(ctx, row, err)
		return
	}

	readyCtx, readyCancel := dbCtx(ctx)
	defer readyCancel()
	if merr := w.db().MarkReady(readyCtx, row.FileID, row.Version, w.instanceID, previewID); merr != nil {
		slog.Warn("VideoWorker: MarkReady failed", "file_id", row.FileID, "version", row.Version, "err", merr)
	} else {
		slog.Info("VideoWorker: frame generated", "file_id", row.FileID, "version", row.Version, "preview_id", previewID, "codec", codec)
	}
}

type ffmpegResult struct {
	codec      string
	probed     bool
	jpg        []byte
	probeErr   error
	extractErr error
}

// runFFmpeg takes over the caller's VideoSem slot and releases it when the ffmpeg work ends, even on panic.
func (w *VideoWorker) runFFmpeg(ctx context.Context, row db.VideoPreview, tmpName string) ffmpegResult {
	defer w.releaseSem()
	extractCtx, extractCancel := context.WithTimeout(ctx, extractCeiling)
	defer extractCancel()

	var res ffmpegResult
	if row.Codec != nil && *row.Codec != "" {
		res.codec = *row.Codec
		slog.Debug("VideoWorker: using stored codec (skip re-probe)",
			"file_id", row.FileID, "version", row.Version, "codec", res.codec)
	} else {
		codec, err := videoDetectCodecFromFileFunc(extractCtx, tmpName)
		if err != nil {
			res.probeErr = err
			return res
		}
		res.codec, res.probed = codec, true
	}
	if !isSupportedVideoCodec(res.codec) {
		return res
	}
	res.jpg, res.extractErr = videoFirstFrameFromFileFunc(extractCtx, tmpName)
	return res
}

// dropRow handles a connectivity failure (source download, frame upload, temp
// file): the GENERATING row owned by this instance is deleted as if the preview
// had never been requested, so the next user request starts over. No attempt is
// counted and no backoff applies.
func (w *VideoWorker) dropRow(ctx context.Context, row db.VideoPreview, err error) {
	slog.Warn("VideoWorker: connectivity failure, dropping row",
		"file_id", row.FileID, "version", row.Version, "err", err)
	c, cancel := dbCtx(ctx)
	defer cancel()
	if derr := w.db().DeleteClaimed(c, row.FileID, row.Version, w.instanceID); derr != nil {
		slog.Warn("VideoWorker: DeleteClaimed failed", "file_id", row.FileID, "version", row.Version, "err", derr)
	}
}

// releaseOrFail handles a render failure: it increments attempts and either
// releases to PENDING or marks FAILED at cap.
// The triggering error is logged here (in full, untruncated) for observability;
// it is never persisted to the DB.
func (w *VideoWorker) releaseOrFail(ctx context.Context, row db.VideoPreview, err error) {
	ctx, cancel := dbCtx(ctx)
	defer cancel()
	slog.Warn("VideoWorker: render failure, releasing to PENDING with attempt increment",
		"file_id", row.FileID, "version", row.Version, "err", err,
		"attempts_after", row.Attempts+1)
	if row.Attempts+1 >= w.maxAttempts {
		slog.Warn("VideoWorker: max attempts reached, marking FAILED",
			"file_id", row.FileID, "version", row.Version, "max_attempts", w.maxAttempts, "err", err)
		if merr := w.db().MarkFailed(ctx, row.FileID, row.Version, w.instanceID); merr != nil {
			slog.Warn("VideoWorker: MarkFailed (transient cap) failed", "err", merr)
		}
	} else {
		if rerr := w.db().ReleaseWithAttempt(ctx, row.FileID, row.Version, w.instanceID); rerr != nil {
			slog.Warn("VideoWorker: ReleaseWithAttempt failed", "err", rerr)
		}
	}
}

// acquireSem blocks until a VideoSem slot is free or ctx is done; nil VideoSem is unlimited.
func (w *VideoWorker) acquireSem(ctx context.Context) error {
	if w.deps.VideoSem == nil {
		return nil
	}
	w.slotWaiters.Add(1)
	defer w.slotWaiters.Add(-1)
	select {
	case w.deps.VideoSem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-w.deps.VideoSem
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseSem releases one slot back to VideoSem. No-op when VideoSem is nil.
func (w *VideoWorker) releaseSem() {
	if w.deps.VideoSem == nil {
		return
	}
	<-w.deps.VideoSem
}

// isStorageNotFound returns true if err wraps storage.ErrNotFound.
func isStorageNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// liveKey returns the map key used to track a running goroutine in VideoWorker.live.
func liveKey(fileID string, version int) string {
	return fileID + "\x00" + strconv.Itoa(version)
}
