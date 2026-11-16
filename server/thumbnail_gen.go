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
//  5. render failure (probe, extraction, GIF resize, Collabora error, vips) → ReleaseWithAttempt, MarkFailed at the cap.
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
				w.releaseRows(ctx, first.FileID, first.Version, sizesOf(rows))
			}
		}
	}()

	slog.Debug("AsyncWorker: attempt started",
		"file_id", first.FileID, "version", first.Version, "kind", first.Kind, "instance_id", w.instanceID)

	stopHB := w.startHeartbeat(ctx, first.FileID, first.Version, sizesOf(rows))
	defer stopHB()

	var outs map[string]produced
	var fail *jobFailure
	switch kind(first.Kind) {
	case kindVideo:
		var pair render.Pair
		if pair, fail = w.runVideo(ctx, rows); fail == nil {
			outs = fromPair(pair)
		}
	case kindOffice:
		var pair render.Pair
		if pair, fail = w.runOffice(ctx, rows); fail == nil {
			outs = fromPair(pair)
		}
	case kindGIF:
		outs, fail = w.runGIF(ctx, rows)
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
	w.finish(ctx, rows, outs)
}

func (w *AsyncWorker) startHeartbeat(ctx context.Context, fileID string, version int, sizes []string) func() {
	stop := make(chan struct{})
	stopOnce := sync.OnceFunc(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if herr := w.db().Heartbeat(ctx, fileID, version, w.instanceID, sizes); herr != nil {
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
		w.releaseRows(ctx, first.FileID, first.Version, sizesOf(rows))
	case failNotFound:
		slog.Warn("AsyncWorker: source blob not found, marking FAILED",
			"file_id", first.FileID, "version", first.Version)
		c, cancel := dbCtx(ctx)
		defer cancel()
		if merr := w.db().MarkFailed(c, first.FileID, first.Version, w.instanceID, sizesOf(rows)); merr != nil {
			slog.Warn("AsyncWorker: MarkFailed (not found) failed", "err", merr)
		}
	case failConnectivity:
		w.dropRows(ctx, first.FileID, first.Version, sizesOf(rows), f.err)
	case failUnsupported:
		c, cancel := dbCtx(ctx)
		defer cancel()
		if merr := w.db().MarkUnsupported(c, first.FileID, first.Version, w.instanceID, sizesOf(rows)); merr != nil {
			slog.Warn("AsyncWorker: MarkUnsupported failed", "err", merr)
		}
	default:
		w.releaseOrFail(ctx, rows, f.err)
	}
}

// finish uploads the claimed renditions and marks the rows READY. Blobs that
// no row ended up referencing (upload failure, rows deleted meanwhile) are
// deleted at once.
func (w *AsyncWorker) finish(ctx context.Context, rows []db.Thumbnail, outs map[string]produced) {
	first := rows[0]
	done := make([]db.Thumbnail, 0, len(rows))
	for _, row := range rows {
		out := outs[row.Size]
		ct := out.contentType
		row.ContentType = &ct
		if out.original {
			row.Original = true
			done = append(done, row)
			continue
		}
		blobID, err := storeBlob(ctx, w.deps.slotLimits(), w.deps.Store, out.body, row.Version, row.ServiceType, row.OwnerID)
		if err != nil {
			w.deleteBlobs(ctx, done)
			if ctx.Err() != nil {
				w.releaseRows(ctx, first.FileID, first.Version, sizesOf(rows))
				return
			}
			w.dropRows(ctx, first.FileID, first.Version, sizesOf(rows), err)
			return
		}
		row.BlobID = &blobID
		done = append(done, row)
	}

	readyCtx, cancel := dbCtx(ctx)
	defer cancel()
	written, err := w.db().MarkReady(readyCtx, w.instanceID, done)
	if err != nil {
		slog.Warn("AsyncWorker: MarkReady failed", "file_id", first.FileID, "version", first.Version, "err", err)
		w.deleteUnreferenced(ctx, done)
		if ctx.Err() != nil {
			w.releaseRows(ctx, first.FileID, first.Version, sizesOf(rows))
			return
		}
		w.dropRows(ctx, first.FileID, first.Version, sizesOf(rows), err)
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

// deleteBlobs deletes each blob on the live job context and falls back to a bounded one once it is done.
func (w *AsyncWorker) deleteBlobs(ctx context.Context, rows []db.Thumbnail) {
	for i := range rows {
		dctx, cancel := ctx, context.CancelFunc(func() {})
		if ctx.Err() != nil {
			dctx, cancel = dbCtx(ctx)
		}
		deleteRowBlob(dctx, w.deps.Store, &rows[i], "AsyncWorker")
		cancel()
	}
}

// deleteUnreferenced is for an ambiguous MarkReady: a commit that was applied but not acknowledged keeps its blobs.
func (w *AsyncWorker) deleteUnreferenced(ctx context.Context, rows []db.Thumbnail) {
	var orphans []db.Thumbnail
	for _, row := range rows {
		if row.BlobID == nil || !blobReferenced(ctx, w.db(), row.FileID, row.Version, *row.BlobID) {
			orphans = append(orphans, row)
		}
	}
	w.deleteBlobs(ctx, orphans)
}

// storeBlob uploads data under a fresh node id with the given version, service type and owner, so it
// counts toward the owner's quota. On failure it deletes the possibly partial node: before returning
// when ctx ended, in a tracked background goroutine otherwise; both are bounded by releaseTimeout.
func storeBlob(ctx context.Context, lim *limits, store storage.Client, data []byte, version int, serviceType, ownerID string) (string, error) {
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
	lim.background(func() {
		dctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		defer cancel()
		_ = store.Delete(dctx, nodeID, version, serviceType, ownerID)
	})
	return "", err
}

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

	rc, _, err := openSource(ctx, w.deps.Store, row.FileID, row.Version, row.ServiceType, row.OwnerID, readIdleTimeout)
	if err != nil {
		return render.Pair{}, sourceFailure(ctx, err)
	}

	tmp, err := os.CreateTemp("", "carbonio-preview-worker-*.bin")
	if err != nil {
		rc.Close()
		return render.Pair{}, failure(failConnectivity, err)
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
		if serr := w.db().SetCodec(stateCtx, row.FileID, row.Version, w.instanceID, sizesOf(rows), res.codec); serr != nil {
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

// runGIF downloads the source, writes the sizes that fit as originals and resizes the others with ffmpeg
// under the async slot: M from the source, S from the M output when M was produced in this job.
func (w *AsyncWorker) runGIF(ctx context.Context, rows []db.Thumbnail) (map[string]produced, *jobFailure) {
	row := rows[0]
	data, err := w.deps.Store.RetrieveData(ctx, row.FileID, row.Version, row.ServiceType, row.OwnerID)
	if err != nil {
		return nil, sourceFailure(ctx, err)
	}
	info, fail := w.probe(ctx, data)
	if fail != nil {
		return nil, fail
	}
	outs := map[string]produced{}
	var resize []string
	for _, r := range rows {
		if longSide(info) <= sideOf(r.Size) {
			outs[r.Size] = produced{body: data, contentType: mimeGIF, original: true}
		} else {
			resize = append(resize, r.Size)
		}
	}
	if len(resize) == 0 {
		return outs, nil
	}

	dir, err := os.MkdirTemp("", "carbonio-preview-gif-*")
	if err != nil {
		return nil, failure(failConnectivity, err)
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.gif")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return nil, failure(failConnectivity, err)
	}

	if err := w.acquireSem(ctx); err != nil {
		return nil, failure(failReleased, err)
	}
	defer w.releaseSem()
	input := src
	for _, size := range []string{db.SizeM, db.SizeS} {
		if !containsSize(resize, size) {
			continue
		}
		out, fail := w.gifPass(ctx, input, info, size, filepath.Join(dir, size))
		if fail != nil {
			return nil, fail
		}
		outs[size] = produced{body: out, contentType: mimeGIF}
		if size == db.SizeM {
			input = filepath.Join(dir, "m.gif")
			if err := os.WriteFile(input, out, 0o600); err != nil {
				return nil, failure(failConnectivity, err)
			}
		}
	}
	return outs, nil
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

type produced struct {
	body        []byte
	contentType string
	original    bool
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

func longSide(i render.Info) int { return max(i.Width, i.Height) }

func isAnimatedGIF(p pipeline, i render.Info) bool {
	return p.mime == mimeGIF && i.IsGIF() && i.Pages > 1
}

func fromPair(pair render.Pair) map[string]produced {
	return map[string]produced{
		db.SizeS: {body: pair.S.Body, contentType: pair.S.ContentType},
		db.SizeM: {body: pair.M.Body, contentType: pair.M.ContentType},
	}
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

func (r runner) imagePair(data []byte) (render.Pair, error) {
	if err := r.slot(r.l.render); err != nil {
		return render.Pair{}, err
	}
	defer r.l.render.release()
	pair, err := imagePairFunc(data)
	if err != nil {
		slog.Warn(r.op+" render", "err", err)
		return render.Pair{}, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return pair, nil
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

func (r runner) pdfRenditions(data []byte) (map[string]produced, error) {
	if err := r.slot(r.l.render); err != nil {
		return nil, err
	}
	defer r.l.render.release()
	pair, err := pdfPairFunc(r.wait, data)
	if err != nil {
		slog.Warn(r.op+" PDFPage1Pair", "err", err)
		return nil, pdfRenderError(err)
	}
	return fromPair(pair), nil
}

func (r runner) imageInfo(p pipeline, data []byte) (render.Info, error) {
	if !p.original {
		return render.Info{}, nil
	}
	return r.probe(data)
}

// A size at or below the target of a source that libvips reads as unrotated JPEG/PNG/GIF is the source itself; the pair is only rendered when a size needs it.
func (r runner) staticRenditions(p pipeline, data []byte, info render.Info) (map[string]produced, error) {
	set := map[string]produced{}
	for _, size := range []string{db.SizeS, db.SizeM} {
		if p.original && info.Original() && longSide(info) <= sideOf(size) {
			set[size] = produced{body: data, contentType: p.mime, original: true}
		}
	}
	if len(set) == 2 {
		return set, nil
	}
	rendered, err := r.frameRenditions(data)
	if err != nil {
		return nil, err
	}
	for size, g := range rendered {
		if _, ok := set[size]; !ok {
			set[size] = g
		}
	}
	return set, nil
}

func (r runner) frameRenditions(data []byte) (map[string]produced, error) {
	pair, err := r.imagePair(data)
	if err != nil {
		return nil, err
	}
	return fromPair(pair), nil
}
