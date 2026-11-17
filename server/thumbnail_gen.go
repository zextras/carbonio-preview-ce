// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
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

// attempt runs a single generate job for a row this instance has claimed: one size of one (file_id, version).
//
// Outcome mapping:
//  1. ctx ended (graceful shutdown)                     → row back to PENDING, no attempt.
//  2. source gone from storage (404)                    → MarkFailed (terminal).
//  3. connectivity failure (source download, temp file, blob upload,
//     Collabora unreachable)                            → row deleted, no attempt counted.
//  4. video codec not in the supported list             → MarkUnsupported (terminal until the list grows).
//  5. render failure (probe, extraction, GIF resize, Collabora error, vips) → ReleaseWithAttempt, MarkFailed at the cap.
//  6. success                                           → rendition cached, blob uploaded under the row's owner, MarkReady.
//
// The heartbeat refreshes claimed_at for the whole attempt, whatever the kind.
func (w *AsyncWorker) attempt(ctx context.Context, row db.Thumbnail) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("AsyncWorker: attempt panicked",
				"file_id", row.FileID, "version", row.Version, "size", row.Size, "panic", r,
				"stack", string(debug.Stack()))
			if w.db() != nil {
				w.releaseRow(ctx, row)
			}
		}
	}()

	slog.Debug("AsyncWorker: attempt started",
		"file_id", row.FileID, "version", row.Version, "size", row.Size, "kind", row.Kind, "instance_id", w.instanceID)

	stopHB := w.startHeartbeat(ctx, row)
	defer stopHB()

	var out produced
	var fail *jobFailure
	switch kind(row.Kind) {
	case kindVideo:
		out, fail = w.runVideo(ctx, row, w.ownSlot())
	case kindOffice:
		out, fail = w.runOffice(ctx, row, w.ownSlot())
	case kindGIF:
		out, fail = w.runGIF(ctx, row, w.ownSlot())
	default:
		fail = failure(failContent, fmt.Errorf("unsupported job kind %q", row.Kind))
	}
	if fail == nil && ctx.Err() != nil {
		fail = failure(failReleased, ctx.Err())
	}
	if fail != nil {
		w.settleFailure(ctx, row, fail)
		return
	}
	w.finish(ctx, row, out)
}

func (w *AsyncWorker) startHeartbeat(ctx context.Context, row db.Thumbnail) func() {
	stop := make(chan struct{})
	stopOnce := sync.OnceFunc(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if herr := w.db().Heartbeat(ctx, row.FileID, row.Version, w.instanceID, []string{row.Size}); herr != nil {
					slog.Warn("AsyncWorker: heartbeat failed (non-fatal)", "err", herr, "file_id", row.FileID, "version", row.Version, "size", row.Size)
				} else {
					slog.Debug("AsyncWorker: heartbeat sent", "file_id", row.FileID, "version", row.Version, "size", row.Size)
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

func (w *AsyncWorker) settleFailure(ctx context.Context, row db.Thumbnail, f *jobFailure) {
	sizes := []string{row.Size}
	if f.kind != failReleased && ctx.Err() != nil {
		f = failure(failReleased, ctx.Err())
	}
	switch f.kind {
	case failReleased:
		slog.Debug("AsyncWorker: context ended, releasing without attempt increment",
			"file_id", row.FileID, "version", row.Version, "size", row.Size)
		w.releaseRow(ctx, row)
	case failNotFound:
		slog.Warn("AsyncWorker: source blob not found, marking FAILED",
			"file_id", row.FileID, "version", row.Version, "size", row.Size)
		c, cancel := dbCtx(ctx)
		defer cancel()
		if merr := w.db().MarkFailed(c, row.FileID, row.Version, w.instanceID, sizes); merr != nil {
			slog.Warn("AsyncWorker: MarkFailed (not found) failed", "err", merr)
		}
	case failConnectivity:
		w.dropRow(ctx, row, f.err)
	case failUnsupported:
		c, cancel := dbCtx(ctx)
		defer cancel()
		if merr := w.db().MarkUnsupported(c, row.FileID, row.Version, w.instanceID, sizes); merr != nil {
			slog.Warn("AsyncWorker: MarkUnsupported failed", "err", merr)
		}
	default:
		w.releaseOrFail(ctx, row, f.err)
	}
}

// finish caches the rendition under its final key, reserves and uploads the blob and marks the row READY, so a poll that
// arrives during the upload is answered from the cache. A confirm that fails leaves the reservation, which expires into a tombstone.
func (w *AsyncWorker) finish(ctx context.Context, row db.Thumbnail, out produced) {
	ct := out.contentType
	row.ContentType = &ct
	w.deps.Cache.Put(thumbKey(cacheTagThumbnail, row.ServiceType, row.OwnerID, row.FileID, row.Version, row.Size, digestOf(&row)), out.entry())
	blobID, err := storeReserved(ctx, w.deps, w.db(), out.body, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		if ctx.Err() != nil {
			w.releaseRow(ctx, row)
			return
		}
		w.dropRow(ctx, row, err)
		return
	}
	row.BlobID = &blobID
	if row.Kind == string(kindGIF) {
		w.deps.Cache.Delete(thumbKey(cacheTagInterim, row.ServiceType, row.OwnerID, row.FileID, row.Version, row.Size, digestOf(&row)))
	}

	readyCtx, cancel := dbCtx(ctx)
	defer cancel()
	if _, err := w.db().MarkReady(readyCtx, w.instanceID, []db.Thumbnail{row}); err != nil {
		slog.Warn("AsyncWorker: MarkReady failed", "file_id", row.FileID, "version", row.Version, "size", row.Size, "err", err)
		if ctx.Err() != nil {
			w.releaseRow(ctx, row)
			return
		}
		w.dropRow(ctx, row, err)
		return
	}
	slog.Info("AsyncWorker: thumbnail generated", "file_id", row.FileID, "version", row.Version,
		"kind", row.Kind, "size", row.Size)
}

// storeReserved reserves a fresh node id, uploads data under it with the given version, service type and owner (so it
// counts toward the owner's quota) and returns the id. Without a reservation nothing is uploaded; a failed upload turns the reservation into a tombstone.
func storeReserved(ctx context.Context, deps Deps, store *db.Store, data []byte, version int, serviceType, ownerID string) (string, error) {
	if store == nil {
		return "", errNoDB
	}
	nodeID := uuid.New().String()
	if err := store.Reserve(ctx, nodeID, ownerID, serviceType, version); err != nil {
		return "", err
	}
	storedID, err := deps.Store.StoreData(ctx, nodeID, version, serviceType, ownerID, data)
	if err != nil {
		actx, cancel := dbCtx(ctx)
		defer cancel()
		if aerr := store.Abandon(actx, nodeID); aerr != nil {
			slog.Warn("storeReserved: abandon failed, the reservation will expire", "blob_id", nodeID, "err", aerr)
		}
		return "", err
	}
	return storedID, nil
}

var errNoDB = errors.New("thumbnail DB not available")

func (w *AsyncWorker) probe(ctx context.Context, data []byte) (render.Info, *jobFailure) {
	lim := w.deps.slotLimits()
	if err := lim.acquire(ctx, lim.render, false); err != nil {
		return render.Info{}, failure(failReleased, err)
	}
	defer lim.render.release()
	info, err := probeFunc(data)
	if err != nil {
		return render.Info{}, failure(failContent, fmt.Errorf("probe: %w", err))
	}
	return info, nil
}

func (w *AsyncWorker) renderThumb(ctx context.Context, data []byte, size string) (produced, *jobFailure) {
	lim := w.deps.slotLimits()
	if err := lim.acquire(ctx, lim.render, false); err != nil {
		return produced{}, failure(failReleased, err)
	}
	defer lim.render.release()
	out, err := imageThumbFunc(data, sideOf(size))
	if err != nil {
		return produced{}, failure(failContent, fmt.Errorf("image thumbnail: %w", err))
	}
	return fromRendition(out), nil
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
// extracts the first frame under the async slot, and builds the row's size from the frame.
func (w *AsyncWorker) runVideo(ctx context.Context, row db.Thumbnail, slot jobSlot) (produced, *jobFailure) {

	rc, _, err := openSource(ctx, w.deps.Store, row.FileID, row.Version, row.ServiceType, row.OwnerID, readIdleTimeout)
	if err != nil {
		return produced{}, sourceFailure(ctx, err)
	}

	tmp, err := os.CreateTemp("", "carbonio-preview-worker-*.bin")
	if err != nil {
		rc.Close()
		return produced{}, failure(failConnectivity, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Stream the source into a SPARSE temp file: only the head (frame-0 sample
	// + faststart moov) and the tail (trailing moov/index) are retained; the
	// middle is discarded so a multi-GB source costs ~head+tail of temp disk,
	// not its full size. The full body is still transferred (storage origin
	// does not honor HTTP Range), and the idle-read watchdog + heartbeat still
	// see byte progress through the reader. If head+tail is insufficient, the
	// probe/extract below fail and the job is treated exactly like a corrupt
	// file (no full-download fallback — deliberate).
	_, copyErr := video.StreamToSparseTemp(tmp, rc)
	rc.Close()
	if syncErr := tmp.Sync(); syncErr != nil && copyErr == nil {
		copyErr = syncErr
	}
	tmp.Close()
	if copyErr != nil {
		slog.Warn("AsyncWorker: download failed (possibly stalled idle watchdog)",
			"file_id", row.FileID, "version", row.Version, "err", copyErr)
		return produced{}, sourceFailure(ctx, copyErr)
	}

	// Probe + extract operate on the LOCAL temp file: decoding one frame is size-
	// and network-independent and should take seconds. Beyond extractCeiling ffmpeg
	// is hung, which is a render failure (NOT UNSUPPORTED). The ceiling covers
	// neither the download nor the wait for the slot.
	if err := slot.acquire(ctx); err != nil {
		return produced{}, failure(failReleased, err)
	}
	res := w.runFFmpeg(ctx, row, tmpName, slot)
	if ctx.Err() != nil {
		return produced{}, failure(failReleased, ctx.Err())
	}
	if res.probeErr != nil {
		slog.Warn("AsyncWorker: codec probe failed", "file_id", row.FileID, "version", row.Version, "err", res.probeErr)
		return produced{}, failure(failContent, fmt.Errorf("codec probe failed: %w", res.probeErr))
	}
	if res.probed && row.ClaimedBy != nil && w.db() != nil {
		stateCtx, cancel := dbCtx(ctx)
		if serr := w.db().SetCodec(stateCtx, row.FileID, row.Version, w.instanceID, []string{row.Size}, res.codec); serr != nil {
			slog.Warn("AsyncWorker: SetCodec failed (non-fatal)", "err", serr)
		}
		cancel()
	}
	if !isSupportedVideoCodec(res.codec) {
		slog.Warn("AsyncWorker: codec not in supported list, marking UNSUPPORTED",
			"file_id", row.FileID, "version", row.Version, "codec", res.codec)
		return produced{}, failure(failUnsupported, nil)
	}
	if res.extractErr != nil {
		if errors.Is(res.extractErr, video.ErrExtractFailed) || errors.Is(res.extractErr, video.ErrExtractTimeout) {
			slog.Warn("AsyncWorker: frame extraction failed for supported codec",
				"file_id", row.FileID, "version", row.Version, "codec", res.codec, "err", res.extractErr)
		}
		return produced{}, failure(failContent, res.extractErr)
	}
	return w.renderThumb(ctx, res.jpg, row.Size)
}

type ffmpegResult struct {
	codec      string
	probed     bool
	jpg        []byte
	probeErr   error
	extractErr error
}

// runFFmpeg takes over the caller's AsyncSem slot and releases it when the ffmpeg work ends, even on panic.
func (w *AsyncWorker) runFFmpeg(ctx context.Context, row db.Thumbnail, tmpName string, slot jobSlot) ffmpegResult {
	defer slot.release()
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
// (the wait is bounded by the job context, the call by Collabora's inactivity window) and builds the
// row's size from it.
func (w *AsyncWorker) runOffice(ctx context.Context, row db.Thumbnail, slot jobSlot) (produced, *jobFailure) {
	data, err := w.deps.Store.RetrieveData(ctx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		return produced{}, sourceFailure(ctx, err)
	}
	lang := "en-US"
	if row.Lang != nil && *row.Lang != "" {
		lang = *row.Lang
	}
	cfg := w.deps.Cfg
	if err := slot.acquire(ctx); err != nil {
		return produced{}, failure(failReleased, err)
	}
	png, err := func() ([]byte, error) {
		defer slot.release()
		return collaboraConvertFunc(ctx, data, lang, cfg.DocumentConversionFullConvertAddress+"/png",
			time.Duration(cfg.ServiceDocsTimeout)*time.Second)
	}()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return produced{}, failure(failReleased, ctx.Err())
		case errors.Is(err, render.ErrCollaboraUnavailable):
			return produced{}, failure(failConnectivity, err)
		}
		return produced{}, failure(failContent, fmt.Errorf("collabora convert: %w", err))
	}
	return w.renderThumb(ctx, png, row.Size)
}

// runGIF downloads the source and builds the row's size from it.
func (w *AsyncWorker) runGIF(ctx context.Context, row db.Thumbnail, slot jobSlot) (produced, *jobFailure) {
	data, err := w.deps.Store.RetrieveData(ctx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		return produced{}, sourceFailure(ctx, err)
	}
	info, fail := w.probe(ctx, data)
	if fail != nil {
		return produced{}, fail
	}
	return w.gifOutput(ctx, data, info, row.Size, slot)
}

// gifOutput resizes the source with ffmpeg, straight from the source, under the async slot.
func (w *AsyncWorker) gifOutput(ctx context.Context, data []byte, info render.Info, size string, slot jobSlot) (produced, *jobFailure) {
	dir, err := os.MkdirTemp("", "carbonio-preview-gif-*")
	if err != nil {
		return produced{}, failure(failConnectivity, err)
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.gif")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return produced{}, failure(failConnectivity, err)
	}

	if err := slot.acquire(ctx); err != nil {
		return produced{}, failure(failReleased, err)
	}
	defer slot.release()
	out, fail := w.gifPass(ctx, src, info, size, filepath.Join(dir, size))
	if fail != nil {
		return produced{}, fail
	}
	return produced{body: out, contentType: mimeGIF}, nil
}

func (w *AsyncWorker) gifPass(ctx context.Context, input string, info render.Info, size, workDir string) ([]byte, *jobFailure) {
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return nil, failure(failConnectivity, err)
	}
	passCtx, cancel := context.WithTimeout(ctx, extractCeiling)
	defer cancel()
	out, err := animatedGIFFunc(passCtx, input, info, sideOf(size), workDir)
	switch {
	case ctx.Err() != nil:
		return nil, failure(failReleased, ctx.Err())
	case err != nil:
		return nil, failure(failContent, fmt.Errorf("gif resize: %w", err))
	}
	return out, nil
}

// jobSlot is how a job takes the async slot: its own for a worker job, or none for an inline generation whose caller already holds one.
type jobSlot struct {
	acquire func(context.Context) error
	release func()
}

func (w *AsyncWorker) ownSlot() jobSlot { return jobSlot{acquire: w.acquireSem, release: w.releaseSem} }

var heldSlot = jobSlot{acquire: func(context.Context) error { return nil }, release: func() {}}

type produced struct {
	body        []byte
	contentType string
}

func (p produced) entry() cache.Entry {
	return cache.Entry{Body: p.body, ContentType: p.contentType}
}

const mimeGIF = "image/gif"

func sideOf(size string) int {
	if size == db.SizeS {
		return render.SideS
	}
	return render.SideM
}

func isAnimatedGIF(p pipeline, i render.Info) bool {
	return p.mime == mimeGIF && i.IsGIF() && i.Pages > 1
}

func fromRendition(r render.Rendition) produced {
	return produced{body: r.Body, contentType: r.ContentType}
}

func (r runner) probe(data []byte) (render.Info, error) {
	if err := r.slot(r.l.render); err != nil {
		return render.Info{}, err
	}
	defer r.l.render.release()
	info, err := probeFunc(data)
	if err != nil {
		slog.Warn(r.op+" probe", "err", err)
		return render.Info{}, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return info, nil
}

func (r runner) imageThumb(data []byte, size string) (produced, error) {
	if err := r.slot(r.l.render); err != nil {
		return produced{}, err
	}
	defer r.l.render.release()
	out, err := imageThumbFunc(data, sideOf(size))
	if err != nil {
		slog.Warn(r.op+" render", "err", err)
		return produced{}, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return fromRendition(out), nil
}

func (r runner) fullImage(data []byte) (render.Rendition, error) {
	if err := r.slot(r.l.render); err != nil {
		return render.Rendition{}, err
	}
	defer r.l.render.release()
	out, err := fullImageFunc(data)
	if err != nil {
		slog.Warn(r.op+" render", "err", err)
		return render.Rendition{}, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return out, nil
}

func (r runner) pdfThumb(data []byte, size string) (produced, error) {
	if err := r.slot(r.l.render); err != nil {
		return produced{}, err
	}
	defer r.l.render.release()
	out, err := pdfThumbFunc(r.wait, data, sideOf(size))
	if err != nil {
		slog.Warn(r.op+" PDFPage1", "err", err)
		return produced{}, pdfRenderError(err)
	}
	return fromRendition(out), nil
}

func (r runner) imageInfo(p pipeline, data []byte) (render.Info, error) {
	if p.mime != mimeGIF {
		return render.Info{}, nil
	}
	return r.probe(data)
}
