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
//   - Sweep uses Deps.VideoSem as the in-flight concurrency gate so total ffmpeg
//     concurrency is ONE number shared between the worker and any immediate
//     on-request generation attempts in the GET handlers.
//   - Stale reclaim is the ONLY path that increments attempts (poison-job protection).
//     A transient error in attempt() calls db.Release (returns row to PENDING) WITHOUT
//     incrementing, matching WSC exactly.

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

	"github.com/zextras/carbonio-preview-ce/v2/db"
	"github.com/zextras/carbonio-preview-ce/v2/storage"
	"github.com/zextras/carbonio-preview-ce/v2/video"
)

// readIdleTimeout is the maximum silence between successive bytes on the source
// download.  A download that stalls for longer than this is cancelled so the
// semaphore slot is freed and the job retried on the next sweep tick.
//
// This is a package variable (not a const) so tests can override it to a tiny
// value (e.g. 50ms) for speed without touching any config/KV knob.
var readIdleTimeout = 60 * time.Second

// heartbeatInterval is how often the worker calls db.Store.Heartbeat during the
// download phase to refresh claimed_at.  Must be well below staleTTL (900s) so
// that a legitimately slow download — one that IS delivering bytes — is never
// seen as stale by ReclaimStale.
//
// Frequency is intentionally network-speed-independent: it is purely a DB
// touch to prove liveness.  A download that is reading bytes at ANY speed keeps
// claimed_at fresh; only a truly stalled download (caught by readIdleTimeout)
// stops heartbeating.
//
// This is a package variable so tests can override it without config/KV knobs.
var heartbeatInterval = 60 * time.Second

// extractCeiling is the maximum time allowed for the POST-DOWNLOAD stages
// (codec probe + first-frame extract + store).  These stages operate on a LOCAL
// temp file: decoding a single frame is file-size- and network-independent and
// should complete in seconds.  If it takes longer than extractCeiling, ffmpeg
// is broken/hung — NOT slow because of a large file.
//
// >120s extracting one frame from a local file = unequivocal process breakage.
// The ceiling is deliberately NOT applied to the download phase (that is the
// idle-watchdog's job; downloads are legitimately long for large files).
//
// This is a package variable so tests can override it without config/KV knobs.
var extractCeiling = 120 * time.Second

// idleReadCloser wraps a ReadCloser and resets a timer on every successful
// read.  The timer fires dlCancel when no bytes arrive for readIdleTimeout.
// Call Close to stop the timer and release resources.
type idleReadCloser struct {
	rc       io.ReadCloser
	timer    *time.Timer
	timeout  time.Duration
	dlCancel context.CancelFunc
}

func newIdleReadCloser(rc io.ReadCloser, timeout time.Duration, dlCancel context.CancelFunc) *idleReadCloser {
	irc := &idleReadCloser{
		rc:       rc,
		timeout:  timeout,
		dlCancel: dlCancel,
	}
	irc.timer = time.AfterFunc(timeout, dlCancel)
	return irc
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
	// maxInFlight is the maximum number of concurrent generate jobs per tick.
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
	// on this instance (which would double-spend a second semaphore slot).
	mu   sync.Mutex
	live map[string]struct{}

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
// Total ffmpeg concurrency is controlled by deps.VideoSem (sized by VideoConcurrency).
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
	go func() {
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

	inFlight := 0

	// (a) PENDING newest-first: fetch up to MAX_IN_FLIGHT*4 candidates, claim
	//     each, and submit bounded by VideoSem (shared ffmpeg concurrency slot).
	rows, err := w.db().FindPendingNewest(ctx, maxInFlight*4)
	if err != nil {
		slog.Warn("VideoWorker: FindPendingNewest failed", "err", err)
	} else {
		for _, row := range rows {
			if inFlight >= maxInFlight {
				break
			}
			claimed, cerr := w.db().Claim(ctx, row.FileID, row.Version, w.instanceID)
			if cerr != nil {
				slog.Warn("VideoWorker: Claim failed", "file_id", row.FileID, "version", row.Version, "err", cerr)
				continue
			}
			if !claimed {
				continue
			}
			// Try to acquire a semaphore slot. Non-blocking: if all slots are
			// busy, stop feeding — remaining rows stay PENDING for the next tick.
			if !w.tryAcquireSem() {
				// Return the row to PENDING without counting an attempt.
				slog.Debug("VideoWorker: worker busy, releasing without attempt increment",
					"file_id", row.FileID, "version", row.Version)
				if rerr := w.db().Release(ctx, row.FileID, row.Version, w.instanceID); rerr != nil {
					slog.Warn("VideoWorker: Release (busy) failed", "err", rerr)
				}
				break
			}
			inFlight++
			// Local copy for goroutine capture.
			job := row
			key := liveKey(job.FileID, job.Version)
			w.mu.Lock()
			w.live[key] = struct{}{}
			w.mu.Unlock()
			go func() {
				defer func() {
					w.mu.Lock()
					delete(w.live, key)
					w.mu.Unlock()
				}()
				defer w.releaseSem()
				w.attempt(ctx, job)
			}()
		}
	}

	// (b) Stale GENERATING reclaim (crash recovery) — only when not fully saturated.
	if inFlight >= maxInFlight {
		return
	}
	staleRows, serr := w.db().ReclaimStale(ctx, w.instanceID, w.staleTTL)
	if serr != nil {
		slog.Warn("VideoWorker: ReclaimStale failed", "err", serr)
		return
	}
	for _, stale := range staleRows {
		if inFlight >= maxInFlight {
			break
		}
		// LIVE-SET CHECK FIRST — before any cap/MarkFailed logic.
		//
		// If a goroutine for this row is still alive on this instance, leave the
		// row in GENERATING untouched.  Do NOT call Release (→ PENDING) here:
		//   • Release would let the PENDING sweep immediately re-claim the row, wasting
		//     a second semaphore slot and creating a duplicate attempt.
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
			if ferr := w.db().MarkFailed(ctx, stale.FileID, stale.Version, w.instanceID); ferr != nil {
				slog.Warn("VideoWorker: MarkFailed (stale cap) failed", "err", ferr)
			}
			continue
		}
		if !w.tryAcquireSem() {
			// Return to PENDING, don't count as attempt.
			slog.Debug("VideoWorker: worker busy on stale-reclaimed row, releasing without attempt increment",
				"file_id", stale.FileID, "version", stale.Version)
			if rerr := w.db().Release(ctx, stale.FileID, stale.Version, w.instanceID); rerr != nil {
				slog.Warn("VideoWorker: Release (stale busy) failed", "err", rerr)
			}
			break
		}
		inFlight++
		job := stale
		staleJobKey := staleKey
		w.mu.Lock()
		w.live[staleJobKey] = struct{}{}
		w.mu.Unlock()
		go func() {
			defer func() {
				w.mu.Lock()
				delete(w.live, staleJobKey)
				w.mu.Unlock()
			}()
			defer w.releaseSem()
			w.attempt(ctx, job)
		}()
	}
}

// attempt runs a single generate job. It acquires no additional semaphore slot
// (the caller already acquired one via tryAcquireSem / the slot is released by
// defer in the caller's goroutine).
//
// Probe-first outcome mapping:
//  1. Download source video to a temp file (shared for probe + extract).
//  2. If row.Codec already set (re-enqueued UNSUPPORTED), skip re-probe.
//     Otherwise probe to detect codec and persist via SetCodec.
//  3. probe fails (unreadable / no video stream)  → ReleaseWithAttempt / cap → MarkFailed.
//  4. codec ∉ supported list                      → MarkUnsupported (terminal, no retry).
//  5. codec ∈ supported list, extract OK          → MarkReady.
//  6. codec ∈ supported list, ErrExtractFailed    → ReleaseWithAttempt / cap → MarkFailed
//     (supported codec but corrupt — treat as transient until cap).
//  7. storage.ErrNotFound (source gone)           → MarkFailed (terminal, no retry).
//  8. other transient error                       → ReleaseWithAttempt / cap → MarkFailed.
func (w *VideoWorker) attempt(ctx context.Context, row db.VideoPreview) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("VideoWorker: attempt panicked",
				"file_id", row.FileID, "version", row.Version, "panic", r,
				"stack", string(debug.Stack()))
			if store := w.db(); store != nil {
				_ = store.Release(ctx, row.FileID, row.Version, w.instanceID)
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
	// transport read so the semaphore slot is freed and the job retried.
	// After io.Copy returns (success or stall), the watchdog timer is stopped and
	// dlCtx is released; the subsequent probe/extract/store stages run under the
	// original ctx (no watchdog — those phases are bounded by extractCeiling below).
	//
	// Heartbeat: a separate goroutine fires db.Heartbeat every heartbeatInterval
	// WHILE the download is in progress.  This keeps claimed_at fresh so that
	// ReclaimStale never treats a legitimately-slow-but-progressing download as
	// stale.  Frequency is network-speed-independent: it fires on wall-clock time,
	// not on bytes transferred.  The goroutine is stopped (via hbStop close) as
	// soon as io.Copy returns — heartbeat is ONLY for the download phase.
	dlCtx, dlCancel := context.WithCancel(ctx)
	rc, err := w.deps.Store.RetrieveDataStreaming(dlCtx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		dlCancel()
		if isStorageNotFound(err) {
			slog.Warn("VideoWorker: source blob not found, marking FAILED",
				"file_id", row.FileID, "version", row.Version)
			if merr := w.db().MarkFailed(ctx, row.FileID, row.Version, w.instanceID); merr != nil {
				slog.Warn("VideoWorker: MarkFailed (not found) failed", "err", merr)
			}
			return
		}
		w.releaseOrFail(ctx, row, err)
		return
	}

	tmp, err := os.CreateTemp("", "carbonio-preview-worker-*.bin")
	if err != nil {
		rc.Close()
		dlCancel()
		w.releaseOrFail(ctx, row, err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Start download heartbeat goroutine.  Fires db.Heartbeat every
	// heartbeatInterval; stopped when hbStop is closed (after io.Copy returns).
	hbStop := make(chan struct{})
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
	guard := newIdleReadCloser(rc, readIdleTimeout, dlCancel)
	// Stream the source into a SPARSE temp file: only the head (frame-0 sample
	// + faststart moov) and the tail (trailing moov/index) are retained; the
	// middle is discarded so a multi-GB source costs ~head+tail of temp disk,
	// not its full size. The full body is still transferred (storage origin
	// does not honor HTTP Range), and the idle-read watchdog + heartbeat still
	// see byte progress through `guard`. If head+tail is insufficient, the
	// probe/extract below fail and this row is treated exactly like a corrupt
	// file (no full-download fallback — deliberate).
	_, copyErr := video.StreamToSparseTemp(tmp, guard)
	// Stop the heartbeat goroutine and the watchdog, release dlCtx immediately.
	// Heartbeat is ONLY for the download phase; probe/extract/store are bounded
	// by extractCeiling (size/network-independent, applied below).
	close(hbStop)
	guard.Close()
	dlCancel()

	if syncErr := tmp.Sync(); syncErr != nil && copyErr == nil {
		copyErr = syncErr
	}
	tmp.Close()
	if copyErr != nil {
		slog.Warn("VideoWorker: download failed (possibly stalled idle watchdog)",
			"file_id", row.FileID, "version", row.Version, "err", copyErr)
		w.releaseOrFail(ctx, row, copyErr)
		return
	}

	// --- Steps 2-5: Probe + extract + store (post-download, local file) ---
	//
	// These stages operate entirely on the local temp file.  Decoding ONE frame
	// from a local file is size- and network-independent: it should complete in
	// seconds.  If it takes longer than extractCeiling (120s), ffmpeg is broken
	// or hung — NOT legitimately slow because of a large file.  Cancel via ctx
	// cancellation → exec.CommandContext SIGKILLs ffmpeg → returns an error →
	// treated as transient via releaseOrFail (NOT UNSUPPORTED).
	//
	// extractCeiling is intentionally NOT applied to the download phase above:
	// the download can legitimately be long for large files on slow links.
	extractCtx, extractCancel := context.WithTimeout(ctx, extractCeiling)
	defer extractCancel()

	// --- Step 2: Probe codec (skip if already known from a prior UNSUPPORTED row) ---
	codec := ""
	if row.Codec != nil && *row.Codec != "" {
		// Re-enqueued UNSUPPORTED row — codec already stored, skip re-probe.
		codec = *row.Codec
		slog.Debug("VideoWorker: using stored codec (skip re-probe)",
			"file_id", row.FileID, "version", row.Version, "codec", codec)
	} else {
		codec, err = videoDetectCodecFromFileFunc(extractCtx, tmpName)
		if err != nil {
			// Cannot determine codec — treat as transient (not as UNSUPPORTED).
			slog.Warn("VideoWorker: codec probe failed, releasing with attempt increment",
				"file_id", row.FileID, "version", row.Version, "err", err)
			w.releaseOrFail(ctx, row, fmt.Errorf("codec probe failed: %w", err))
			return
		}
		// Persist the codec immediately so it survives future re-enqueue cycles.
		if serr := w.db().SetCodec(ctx, row.FileID, row.Version, w.instanceID, codec); serr != nil {
			slog.Warn("VideoWorker: SetCodec failed (non-fatal)", "err", serr)
		}
	}

	// --- Step 3: Check codec support ---
	if !isSupportedVideoCodec(codec) {
		slog.Warn("VideoWorker: codec not in supported list, marking UNSUPPORTED",
			"file_id", row.FileID, "version", row.Version, "codec", codec)
		if merr := w.db().MarkUnsupported(ctx, row.FileID, row.Version, w.instanceID); merr != nil {
			slog.Warn("VideoWorker: MarkUnsupported failed", "err", merr)
		}
		return
	}

	// --- Step 4: Extract first frame from the already-downloaded temp file ---
	pngBytes, err := videoFirstFrameFromFileFunc(extractCtx, tmpName)
	if err != nil {
		if errors.Is(err, video.ErrExtractFailed) || errors.Is(err, video.ErrExtractTimeout) {
			// Supported codec but could not decode — treat as transient until cap,
			// then FAILED (never UNSUPPORTED: the codec IS known-good).
			slog.Warn("VideoWorker: frame extraction failed for supported codec, releasing with attempt increment",
				"file_id", row.FileID, "version", row.Version, "codec", codec, "err", err)
		}
		w.releaseOrFail(ctx, row, err)
		return
	}

	// --- Step 5: Re-encode PNG→JPEG and store ---
	_, err = encodePNGToJPEGAndStore(extractCtx, w.deps.Store, pngBytes, row.Version, row.ServiceType, row.OwnerID, previewID)
	if err != nil {
		w.releaseOrFail(ctx, row, err)
		return
	}

	if merr := w.db().MarkReady(ctx, row.FileID, row.Version, w.instanceID, previewID); merr != nil {
		slog.Warn("VideoWorker: MarkReady failed", "file_id", row.FileID, "version", row.Version, "err", merr)
	} else {
		slog.Info("VideoWorker: frame generated", "file_id", row.FileID, "version", row.Version, "preview_id", previewID, "codec", codec)
	}
}

// releaseOrFail increments attempts and either releases to PENDING or marks FAILED at cap.
// The triggering error is logged here (in full, untruncated) for observability;
// it is never persisted to the DB.
func (w *VideoWorker) releaseOrFail(ctx context.Context, row db.VideoPreview, err error) {
	slog.Warn("VideoWorker: transient error, releasing to PENDING with attempt increment",
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

// tryAcquireSem attempts a non-blocking acquire of the VideoSem.
// Returns true if a slot was acquired, false if the semaphore is full.
// When VideoSem is nil (unlimited / tests), always returns true.
func (w *VideoWorker) tryAcquireSem() bool {
	if w.deps.VideoSem == nil {
		return true
	}
	select {
	case w.deps.VideoSem <- struct{}{}:
		return true
	default:
		return false
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
