// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
	"github.com/zextras/carbonio-preview-ce/v4/video"
)

type failKind int

const (
	failReleased failKind = iota
	failNotFound
	failConnectivity
	failUnsupported
	failContent
)

type jobFailure struct {
	kind failKind
	err  error
}

func failure(kind failKind, err error) *jobFailure { return &jobFailure{kind: kind, err: err} }

// attempt runs a single generate job for rows this instance has claimed: the
// rows of one (file_id, version), one or two sizes of the same kind.
//
// Outcome mapping:
//  1. ctx ended (graceful shutdown)                     → rows back to PENDING, no attempt.
//  2. source gone from storage (404)                    → MarkFailed (terminal).
//  3. connectivity failure (source download, temp file, blob upload,
//     Collabora unreachable)                            → rows deleted, no attempt counted.
//  4. video codec not in the supported list             → MarkUnsupported (terminal until the list grows).
//  5. render failure (probe, extraction, Collabora error, vips) → ReleaseWithAttempt, MarkFailed at the cap.
//  6. success                                           → blobs uploaded under the row's owner, MarkReady.
//
// The heartbeat refreshes claimed_at for the whole attempt, whatever the kind.
func (w *AsyncWorker) attempt(ctx context.Context, rows []db.Thumbnail) {
	first := rows[0]
	defer func() {
		if r := recover(); r != nil {
			slog.Error("AsyncWorker: attempt panicked",
				"file_id", first.FileID, "version", first.Version, "panic", r,
				"stack", string(debug.Stack()))
			if w.db() != nil {
				w.releaseRows(ctx, first.FileID, first.Version)
			}
		}
	}()

	slog.Debug("AsyncWorker: attempt started",
		"file_id", first.FileID, "version", first.Version, "kind", first.Kind, "instance_id", w.instanceID)

	stopHB := w.startHeartbeat(ctx, first.FileID, first.Version)
	defer stopHB()

	var pair render.Pair
	var fail *jobFailure
	switch kind(first.Kind) {
	case kindVideo:
		pair, fail = w.runVideo(ctx, rows)
	case kindOffice:
		pair, fail = w.runOffice(ctx, rows)
	default:
		fail = failure(failContent, fmt.Errorf("unsupported job kind %q", first.Kind))
	}
	if fail == nil && ctx.Err() != nil {
		fail = failure(failReleased, ctx.Err())
	}
	if fail != nil {
		w.settleFailure(ctx, rows, fail)
		return
	}
	w.finish(ctx, rows, pair)
}

func (w *AsyncWorker) startHeartbeat(ctx context.Context, fileID string, version int) func() {
	stop := make(chan struct{})
	stopOnce := sync.OnceFunc(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if herr := w.db().Heartbeat(ctx, fileID, version, w.instanceID); herr != nil {
					slog.Warn("AsyncWorker: heartbeat failed (non-fatal)", "err", herr, "file_id", fileID, "version", version)
				} else {
					slog.Debug("AsyncWorker: heartbeat sent", "file_id", fileID, "version", version)
				}
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return stopOnce
}

func (w *AsyncWorker) settleFailure(ctx context.Context, rows []db.Thumbnail, f *jobFailure) {
	first := rows[0]
	if f.kind != failReleased && ctx.Err() != nil {
		f = failure(failReleased, ctx.Err())
	}
	switch f.kind {
	case failReleased:
		slog.Debug("AsyncWorker: context ended, releasing without attempt increment",
			"file_id", first.FileID, "version", first.Version)
		w.releaseRows(ctx, first.FileID, first.Version)
	case failNotFound:
		slog.Warn("AsyncWorker: source blob not found, marking FAILED",
			"file_id", first.FileID, "version", first.Version)
		c, cancel := dbCtx(ctx)
		defer cancel()
		if merr := w.db().MarkFailed(c, first.FileID, first.Version, w.instanceID); merr != nil {
			slog.Warn("AsyncWorker: MarkFailed (not found) failed", "err", merr)
		}
	case failConnectivity:
		w.dropRows(ctx, first.FileID, first.Version, f.err)
	case failUnsupported:
		c, cancel := dbCtx(ctx)
		defer cancel()
		if merr := w.db().MarkUnsupported(c, first.FileID, first.Version, w.instanceID); merr != nil {
			slog.Warn("AsyncWorker: MarkUnsupported failed", "err", merr)
		}
	default:
		w.releaseOrFail(ctx, rows, f.err)
	}
}

// finish uploads the claimed renditions and marks the rows READY. Blobs that
// no row ended up referencing (upload failure, rows deleted meanwhile) are
// deleted at once.
func (w *AsyncWorker) finish(ctx context.Context, rows []db.Thumbnail, pair render.Pair) {
	first := rows[0]
	done := make([]db.Thumbnail, 0, len(rows))
	for _, row := range rows {
		rend := pair.M
		if row.Size == db.SizeS {
			rend = pair.S
		}
		blobID, err := storeBlob(ctx, w.deps.Store, rend.Body, row.Version, row.ServiceType, row.OwnerID)
		if err != nil {
			w.deleteBlobs(ctx, done)
			if ctx.Err() != nil {
				w.releaseRows(ctx, first.FileID, first.Version)
				return
			}
			w.dropRows(ctx, first.FileID, first.Version, err)
			return
		}
		row.BlobID = &blobID
		ct := rend.ContentType
		row.ContentType = &ct
		done = append(done, row)
	}

	readyCtx, cancel := dbCtx(ctx)
	defer cancel()
	written, err := w.db().MarkReady(readyCtx, w.instanceID, done)
	if err != nil {
		slog.Warn("AsyncWorker: MarkReady failed", "file_id", first.FileID, "version", first.Version, "err", err)
		w.deleteBlobs(ctx, done)
		return
	}
	var orphans []db.Thumbnail
	for _, row := range done {
		if !containsSize(written, row.Size) {
			orphans = append(orphans, row)
		}
	}
	w.deleteBlobs(ctx, orphans)
	if len(written) > 0 {
		slog.Info("AsyncWorker: thumbnails generated", "file_id", first.FileID, "version", first.Version,
			"kind", first.Kind, "sizes", written)
	}
}

func containsSize(sizes []string, size string) bool {
	for _, s := range sizes {
		if s == size {
			return true
		}
	}
	return false
}

func (w *AsyncWorker) deleteBlobs(ctx context.Context, rows []db.Thumbnail) {
	if len(rows) == 0 {
		return
	}
	dctx, cancel := dbCtx(ctx)
	defer cancel()
	for i := range rows {
		deleteRowBlob(dctx, w.deps.Store, &rows[i], "AsyncWorker")
	}
}

// storeBlob uploads data under a fresh node id with the given version, service type and owner, so it
// counts toward the owner's quota. On failure it deletes the possibly partial node: before returning
// when ctx ended (bounded by releaseTimeout), in the background otherwise.
func storeBlob(ctx context.Context, store storage.Client, data []byte, version int, serviceType, ownerID string) (string, error) {
	nodeID := uuid.New().String()
	storedID, err := store.StoreData(ctx, nodeID, version, serviceType, ownerID, data)
	if err == nil {
		return storedID, nil
	}
	if ctx.Err() != nil {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer cancel()
		_ = store.Delete(dctx, nodeID, version, serviceType, ownerID)
		return "", err
	}
	go func() {
		_ = store.Delete(context.Background(), nodeID, version, serviceType, ownerID)
	}()
	return "", err
}

func (w *AsyncWorker) renderPair(ctx context.Context, data []byte) (render.Pair, *jobFailure) {
	lim := w.deps.slotLimits()
	if err := lim.acquire(ctx, lim.render, false); err != nil {
		return render.Pair{}, failure(failReleased, err)
	}
	defer lim.render.release()
	pair, err := imagePairFunc(data)
	if err != nil {
		return render.Pair{}, failure(failContent, fmt.Errorf("image pair: %w", err))
	}
	return pair, nil
}

func sourceFailure(ctx context.Context, err error) *jobFailure {
	switch {
	case ctx.Err() != nil:
		return failure(failReleased, ctx.Err())
	case isStorageNotFound(err):
		return failure(failNotFound, err)
	}
	return failure(failConnectivity, err)
}

// runVideo streams the source into a sparse temp file (head and tail only), then probes the codec and
// extracts the first frame under the async slot, and builds both renditions from the frame.
func (w *AsyncWorker) runVideo(ctx context.Context, rows []db.Thumbnail) (render.Pair, *jobFailure) {
	row := rows[0]

	// The download runs under a separate child context scoped ONLY to the copy
	// phase: an idleReadCloser fires dlCancel if no bytes arrive for
	// readIdleTimeout, abandoning a stalled download. The probe/extract stages
	// run under ctx and are bounded by extractCeiling.
	dlCtx, dlCancel := context.WithCancel(ctx)
	idleTimer := time.AfterFunc(readIdleTimeout, dlCancel)
	rc, err := w.deps.Store.RetrieveDataStreaming(dlCtx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		idleTimer.Stop()
		dlCancel()
		return render.Pair{}, sourceFailure(ctx, err)
	}

	tmp, err := os.CreateTemp("", "carbonio-preview-worker-*.bin")
	if err != nil {
		rc.Close()
		idleTimer.Stop()
		dlCancel()
		return render.Pair{}, failure(failConnectivity, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	guard := newIdleReadCloser(rc, readIdleTimeout, idleTimer)
	// Stream the source into a SPARSE temp file: only the head (frame-0 sample
	// + faststart moov) and the tail (trailing moov/index) are retained; the
	// middle is discarded so a multi-GB source costs ~head+tail of temp disk,
	// not its full size. The full body is still transferred (storage origin
	// does not honor HTTP Range), and the idle-read watchdog + heartbeat still
	// see byte progress through `guard`. If head+tail is insufficient, the
	// probe/extract below fail and the job is treated exactly like a corrupt
	// file (no full-download fallback — deliberate).
	_, copyErr := video.StreamToSparseTemp(tmp, guard)
	guard.Close()
	dlCancel()
	if syncErr := tmp.Sync(); syncErr != nil && copyErr == nil {
		copyErr = syncErr
	}
	tmp.Close()
	if copyErr != nil {
		slog.Warn("AsyncWorker: download failed (possibly stalled idle watchdog)",
			"file_id", row.FileID, "version", row.Version, "err", copyErr)
		return render.Pair{}, sourceFailure(ctx, copyErr)
	}

	// Probe + extract operate on the LOCAL temp file: decoding one frame is size-
	// and network-independent and should take seconds. Beyond extractCeiling ffmpeg
	// is hung, which is a render failure (NOT UNSUPPORTED). The ceiling covers
	// neither the download nor the wait for the slot.
	if err := w.acquireSem(ctx); err != nil {
		return render.Pair{}, failure(failReleased, err)
	}
	res := w.runFFmpeg(ctx, row, tmpName)
	if ctx.Err() != nil {
		return render.Pair{}, failure(failReleased, ctx.Err())
	}
	if res.probeErr != nil {
		slog.Warn("AsyncWorker: codec probe failed", "file_id", row.FileID, "version", row.Version, "err", res.probeErr)
		return render.Pair{}, failure(failContent, fmt.Errorf("codec probe failed: %w", res.probeErr))
	}
	if res.probed {
		stateCtx, cancel := dbCtx(ctx)
		if serr := w.db().SetCodec(stateCtx, row.FileID, row.Version, w.instanceID, res.codec); serr != nil {
			slog.Warn("AsyncWorker: SetCodec failed (non-fatal)", "err", serr)
		}
		cancel()
	}
	if !isSupportedVideoCodec(res.codec) {
		slog.Warn("AsyncWorker: codec not in supported list, marking UNSUPPORTED",
			"file_id", row.FileID, "version", row.Version, "codec", res.codec)
		return render.Pair{}, failure(failUnsupported, nil)
	}
	if res.extractErr != nil {
		if errors.Is(res.extractErr, video.ErrExtractFailed) || errors.Is(res.extractErr, video.ErrExtractTimeout) {
			slog.Warn("AsyncWorker: frame extraction failed for supported codec",
				"file_id", row.FileID, "version", row.Version, "codec", res.codec, "err", res.extractErr)
		}
		return render.Pair{}, failure(failContent, res.extractErr)
	}
	return w.renderPair(ctx, res.jpg)
}

type ffmpegResult struct {
	codec      string
	probed     bool
	jpg        []byte
	probeErr   error
	extractErr error
}

// runFFmpeg takes over the caller's AsyncSem slot and releases it when the ffmpeg work ends, even on panic.
func (w *AsyncWorker) runFFmpeg(ctx context.Context, row db.Thumbnail, tmpName string) ffmpegResult {
	defer w.releaseSem()
	extractCtx, extractCancel := context.WithTimeout(ctx, extractCeiling)
	defer extractCancel()

	var res ffmpegResult
	if row.Codec != nil && *row.Codec != "" {
		res.codec = *row.Codec
		slog.Debug("AsyncWorker: using stored codec (skip re-probe)",
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

// runOffice downloads the whole document, converts it to a PNG of the first page under the async slot
// (the wait is bounded by the job context, the call by Collabora's inactivity window) and builds both
// renditions from it.
func (w *AsyncWorker) runOffice(ctx context.Context, rows []db.Thumbnail) (render.Pair, *jobFailure) {
	row := rows[0]
	data, err := w.deps.Store.RetrieveData(ctx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		return render.Pair{}, sourceFailure(ctx, err)
	}
	lang := "en-US"
	if row.Lang != nil && *row.Lang != "" {
		lang = *row.Lang
	}
	cfg := w.deps.Cfg
	if err := w.acquireSem(ctx); err != nil {
		return render.Pair{}, failure(failReleased, err)
	}
	png, err := func() ([]byte, error) {
		defer w.releaseSem()
		return collaboraConvertFunc(ctx, data, lang, cfg.DocumentConversionFullConvertAddress+"/png",
			time.Duration(cfg.ServiceDocsTimeout)*time.Second)
	}()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return render.Pair{}, failure(failReleased, ctx.Err())
		case errors.Is(err, render.ErrCollaboraUnavailable):
			return render.Pair{}, failure(failConnectivity, err)
		}
		return render.Pair{}, failure(failContent, fmt.Errorf("collabora convert: %w", err))
	}
	return w.renderPair(ctx, png)
}
