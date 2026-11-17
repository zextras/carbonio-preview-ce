// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

func digestOf(r *db.Thumbnail) string {
	if r == nil || r.Digest == nil {
		return ""
	}
	return *r.Digest
}

func needsRegen(r *db.Thumbnail, digest string) bool {
	if r == nil || r.Status == db.StatusLegacy {
		return true
	}
	if digest == "" || digestOf(r) == digest {
		return false
	}
	switch r.Status {
	case db.StatusReady, db.StatusFailed, db.StatusUnsupported:
		return true
	}
	return false
}

func resolveThumbnail(step runner, deps Deps, worker *AsyncWorker, q thumbRequest) (cache.Entry, error) {
	store := deps.dbStore()
	var r *db.Thumbnail
	if store != nil {
		rows, err := store.FindPair(step.work, q.id, q.version)
		if err != nil {
			slog.Warn("resolveThumbnail: FindPair failed", "file_id", q.id, "version", q.version, "err", err)
			store = nil
		} else {
			r = rows[q.size]
		}
	}
	if store == nil {
		if asyncKind(q.p.kind) {
			return cache.Entry{}, errDependencyDown()
		}
		return generateSync(step, deps, worker, q, nil)
	}
	switch {
	case needsRegen(r, q.digest):
		return regenerate(step, deps, worker, q, r)
	case r.Status == db.StatusReady:
		return serveReady(step, deps, worker, q, r)
	case r.Status == db.StatusPending:
		fireAsyncAttempt(deps, worker, q.id, q.version, q.size)
		return cache.Entry{}, pendingAnswer(step, deps, q, r)
	case r.Status == db.StatusGenerating:
		return cache.Entry{}, pendingAnswer(step, deps, q, r)
	case r.Status == db.StatusUnsupported:
		if r.Codec != nil && isSupportedVideoCodec(*r.Codec) {
			slog.Info("resolveThumbnail: codec now in supported list, re-enqueueing",
				"file_id", q.id, "version", q.version, "size", q.size, "codec", *r.Codec)
			if err := store.ReenqueueUnsupported(step.work, q.id, q.version, q.size); err != nil {
				slog.Warn("resolveThumbnail: ReenqueueUnsupported failed", "err", err)
				return cache.Entry{}, errDependencyDown()
			}
			fireAsyncAttempt(deps, worker, q.id, q.version, q.size)
			return cache.Entry{}, acceptedError{}
		}
		return cache.Entry{}, errFormatNotSupported()
	default:
		if r.Kind == string(kindGIF) {
			return failedGIFFrame(step, deps, q)
		}
		return cache.Entry{}, huma.NewError(http.StatusUnprocessableEntity, "thumbnail generation failed")
	}
}

func serveReady(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, r *db.Thumbnail) (cache.Entry, error) {
	if time.Since(r.LastAccessedAt) > touchEvery {
		if err := deps.dbStore().TouchReady(step.work, q.id, q.version, q.size); err != nil {
			slog.Warn("resolveThumbnail: TouchReady failed", "file_id", q.id, "version", q.version, "err", err)
		}
	}
	if r.Kind == string(kindGIF) {
		deps.Cache.Delete(q.key(cacheTagInterim))
	}
	contentType := q.p.mime
	if r.ContentType != nil {
		contentType = *r.ContentType
	}
	if r.Original {
		return cache.Entry{}, originalAnswer{contentType: contentType}
	}
	if r.BlobID == nil {
		return regenerate(step, deps, worker, q, r)
	}
	data, err := deps.Store.RetrieveData(step.work, *r.BlobID, r.Version, r.ServiceType, r.OwnerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			slog.Warn("resolveThumbnail: blob missing, regenerating",
				"file_id", q.id, "version", q.version, "size", q.size, "blob_id", *r.BlobID)
			return regenerate(step, deps, worker, q, r)
		}
		slog.Warn("resolveThumbnail: blob read failed", "file_id", q.id, "blob_id", *r.BlobID, "err", err)
		return cache.Entry{}, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
	}
	return cache.Entry{Body: data, ContentType: contentType}, nil
}

func pendingRow(q thumbRequest, rowKind string) db.Thumbnail {
	lang := q.lang
	row := db.Thumbnail{
		FileID: q.id, Version: q.version, Size: q.size, Kind: rowKind,
		Lang: &lang, OwnerID: q.owner, ServiceType: q.serviceType,
	}
	if q.digest != "" {
		digest := q.digest
		row.Digest = &digest
	}
	return row
}

func regenerate(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, expected *db.Thumbnail) (cache.Entry, error) {
	if asyncKind(q.p.kind) {
		if err := enqueueRow(step, deps, q, expected, string(q.p.kind)); err != nil {
			return cache.Entry{}, err
		}
		fireAsyncAttempt(deps, worker, q.id, q.version, q.size)
		return cache.Entry{}, acceptedError{}
	}
	return generateSync(step, deps, worker, q, expected)
}

// The old blob goes before the row is replaced: a row left pointing at a deleted blob regenerates on its next read, a blob without a row is never reclaimed.
func enqueueRow(step runner, deps Deps, q thumbRequest, expected *db.Thumbnail, rowKind string) error {
	if expected != nil && !deleteRowBlob(step.work, deps.Store, expected, "regenerate") {
		return nil
	}
	if _, err := deps.dbStore().Enqueue(step.work, pendingRow(q, rowKind), expected); err != nil {
		slog.Warn("regenerate: Enqueue failed", "file_id", q.id, "version", q.version, "size", q.size, "err", err)
		return errDependencyDown()
	}
	return nil
}

// generateSync renders only the requested size from the source and answers before it is persisted.
func generateSync(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, expected *db.Thumbnail) (cache.Entry, error) {
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		return cache.Entry{}, err
	}
	info, err := step.imageInfo(q.p, data)
	if err != nil {
		return cache.Entry{}, err
	}
	if isAnimatedGIF(q.p, info) {
		if deps.dbStore() == nil {
			return cache.Entry{}, errDependencyDown()
		}
		return regenerateGIF(step, deps, worker, q, expected, data, info)
	}
	var g produced
	if q.p.kind == kindPDF {
		g, err = step.pdfThumb(data, q.size)
	} else {
		g, err = step.staticThumb(q.p, data, info, q.size)
	}
	if err != nil {
		return cache.Entry{}, err
	}
	return answer(step, deps, q, string(q.p.kind), expected, g)
}

// answer puts the rendition in the memory cache under its final key and returns it; the row and the blob are saved afterwards in the background.
// Originals are never cached: the handler streams the source instead.
func answer(step runner, deps Deps, q thumbRequest, rowKind string, expected *db.Thumbnail, g produced) (cache.Entry, error) {
	if !g.original {
		deps.Cache.Put(q.key(cacheTagThumbnail), g.entry())
	}
	if store := deps.dbStore(); store != nil {
		deps.slotLimits().background(func() { persist(step.work, deps, store, q, rowKind, expected, g) })
	}
	if g.original {
		return cache.Entry{}, originalAnswer{contentType: g.contentType}
	}
	return g.entry(), nil
}

// persist keeps the anti-orphan order: the expected old blob goes first, the upload follows, the row is a compare-and-set and a lost or failed write deletes the new blob. A failed save leaves nothing: the next miss regenerates.
func persist(ctx context.Context, deps Deps, store *db.Store, q thumbRequest, rowKind string, expected *db.Thumbnail, g produced) {
	contentType := g.contentType
	row := pendingRow(q, rowKind)
	row.Original, row.ContentType = g.original, &contentType
	if expected != nil && !deleteRowBlob(ctx, deps.Store, expected, "persist") {
		return
	}
	if !g.original {
		blobID, err := storeBlob(ctx, deps.slotLimits(), deps.Store, g.body, q.version, q.serviceType, q.owner)
		if err != nil {
			slog.Warn("persist: blob upload failed, not persisting",
				"file_id", q.id, "version", q.version, "size", q.size, "err", err)
			return
		}
		row.BlobID = &blobID
	}
	won, err := store.PutReady(ctx, row, expected)
	if err != nil || !won {
		if err != nil {
			slog.Warn("persist: PutReady failed", "file_id", q.id, "version", q.version, "size", q.size, "err", err)
		}
		if row.BlobID != nil && (err == nil || !blobReferenced(ctx, store, q.id, q.version, *row.BlobID)) {
			deleteRowBlob(ctx, deps.Store, &row, "persist")
		}
	}
}

// blobReferenced is true when a row points at the blob or the rows cannot be read: an ambiguous write must not delete a blob a row may use.
func blobReferenced(ctx context.Context, store *db.Store, fileID string, version int, blobID string) bool {
	rctx, cancel := dbCtx(ctx)
	defer cancel()
	rows, err := store.FindPair(rctx, fileID, version)
	if err != nil {
		slog.Warn("blobReferenced: FindPair failed, keeping the blob", "file_id", fileID, "blob_id", blobID, "err", err)
		return true
	}
	for _, r := range rows {
		if r.BlobID != nil && *r.BlobID == blobID {
			return true
		}
	}
	return false
}

func regenerateGIF(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, expected *db.Thumbnail, data []byte, info render.Info) (cache.Entry, error) {
	if longSide(info) <= sideOf(q.size) {
		g := produced{contentType: mimeGIF, original: true}
		return answer(step, deps, q, string(kindGIF), expected, g)
	}
	if err := enqueueRow(step, deps, q, expected, string(kindGIF)); err != nil {
		return cache.Entry{}, err
	}
	fireAsyncAttempt(deps, worker, q.id, q.version, q.size)
	return cache.Entry{}, cacheInterim(step, deps, q, data)
}

// A failed frame still answers an empty 202: the job is queued either way.
func cacheInterim(step runner, deps Deps, q thumbRequest, data []byte) acceptedError {
	f, err := step.imageThumb(data, q.size)
	if err != nil {
		slog.Warn("interim frame failed", "file_id", q.id, "version", q.version, "size", q.size, "err", err)
		return acceptedError{}
	}
	deps.Cache.Put(q.key(cacheTagInterim), f.entry())
	return acceptedError{body: f.body, contentType: f.contentType}
}

func pendingAnswer(step runner, deps Deps, q thumbRequest, r *db.Thumbnail) error {
	if r.Kind != string(kindGIF) {
		return acceptedError{}
	}
	if e, ok := deps.Cache.Get(q.key(cacheTagInterim)); ok {
		return acceptedError{body: e.Body, contentType: e.ContentType}
	}
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		slog.Warn("interim frame: source unavailable", "file_id", q.id, "version", q.version, "err", err)
		return acceptedError{}
	}
	return cacheInterim(step, deps, q, data)
}

func failedGIFFrame(step runner, deps Deps, q thumbRequest) (cache.Entry, error) {
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		return cache.Entry{}, err
	}
	f, err := step.imageThumb(data, q.size)
	if err != nil {
		return cache.Entry{}, err
	}
	deps.Cache.Delete(q.key(cacheTagInterim))
	return f.entry(), nil
}

// deleteRowBlob reports whether the row's blob is gone (a missing blob counts), so the caller may drop or replace the row.
func deleteRowBlob(ctx context.Context, store storage.Client, row *db.Thumbnail, op string) bool {
	if row.BlobID == nil || row.Original {
		return true
	}
	if err := store.Delete(ctx, *row.BlobID, row.Version, row.ServiceType, row.OwnerID); err != nil && !errors.Is(err, storage.ErrNotFound) {
		slog.Warn(op+": blob delete failed", "file_id", row.FileID, "blob_id", *row.BlobID, "err", err)
		return false
	}
	return true
}

// Best effort: a worker that is not running, a full instance or a lost claim leave the rows for the next sweep tick.
func fireAsyncAttempt(deps Deps, worker *AsyncWorker, fileID string, version int, size string) {
	if worker == nil || deps.dbStore() == nil {
		return
	}
	go worker.runOne(fileID, version, size)
}
