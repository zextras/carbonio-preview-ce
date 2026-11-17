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
			return generateInline(step, deps, worker, q)
		}
		return generateSync(step, deps, worker, nil, q, nil)
	}
	switch {
	case needsRegen(r, q.digest):
		return regenerate(step, deps, worker, store, q, r)
	case r.Status == db.StatusReady:
		return serveReady(step, deps, worker, store, q, r)
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

func serveReady(step runner, deps Deps, worker *AsyncWorker, store *db.Store, q thumbRequest, r *db.Thumbnail) (cache.Entry, error) {
	if time.Since(r.LastAccessedAt) > touchEvery {
		if err := store.TouchReady(step.work, q.id, q.version, q.size); err != nil {
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
		return regenerate(step, deps, worker, store, q, r)
	}
	data, err := deps.Store.RetrieveData(step.work, *r.BlobID, r.Version, r.ServiceType, r.OwnerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			slog.Warn("resolveThumbnail: blob missing, regenerating",
				"file_id", q.id, "version", q.version, "size", q.size, "blob_id", *r.BlobID)
			return regenerate(step, deps, worker, store, q, r)
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

func regenerate(step runner, deps Deps, worker *AsyncWorker, store *db.Store, q thumbRequest, expected *db.Thumbnail) (cache.Entry, error) {
	if asyncKind(q.p.kind) {
		if err := enqueueRow(step, store, q, expected, string(q.p.kind)); err != nil {
			if isDBConnError(err) {
				return generateInline(step, deps, worker, q)
			}
			return cache.Entry{}, errDependencyDown()
		}
		fireAsyncAttempt(deps, worker, q.id, q.version, q.size)
		return cache.Entry{}, acceptedError{}
	}
	return generateSync(step, deps, worker, store, q, expected)
}

func enqueueRow(step runner, store *db.Store, q thumbRequest, expected *db.Thumbnail, rowKind string) error {
	_, err := store.Enqueue(step.work, pendingRow(q, rowKind), expected)
	if err != nil {
		slog.Warn("regenerate: Enqueue failed", "file_id", q.id, "version", q.version, "size", q.size, "err", err)
	}
	return err
}

// generateSync renders only the requested size from the source and answers before it is persisted.
func generateSync(step runner, deps Deps, worker *AsyncWorker, store *db.Store, q thumbRequest, expected *db.Thumbnail) (cache.Entry, error) {
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		return cache.Entry{}, err
	}
	info, err := step.imageInfo(q.p, data)
	if err != nil {
		return cache.Entry{}, err
	}
	if isAnimatedGIF(q.p, info) {
		if store == nil {
			return generateGIFInline(step, deps, worker, q, data, info)
		}
		return regenerateGIF(step, deps, worker, store, q, expected, data, info)
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
	return answer(step, deps, store, q, string(q.p.kind), expected, g)
}

// answer puts the rendition in the memory cache under its final key and returns it; the row and the blob are saved afterwards in the background.
// Originals are never cached: the handler streams the source instead.
func answer(step runner, deps Deps, store *db.Store, q thumbRequest, rowKind string, expected *db.Thumbnail, g produced) (cache.Entry, error) {
	if !g.original {
		deps.Cache.Put(q.key(cacheTagThumbnail), g.entry())
	}
	if store != nil {
		deps.slotLimits().save(func(ctx context.Context) { persist(ctx, deps, store, q, rowKind, expected, g) })
	}
	if g.original {
		return cache.Entry{}, originalAnswer{contentType: g.contentType}
	}
	return g.entry(), nil
}

// persist reserves the blob, uploads it and confirms the row in one transaction. A failed save leaves no row: the next miss regenerates.
func persist(ctx context.Context, deps Deps, store *db.Store, q thumbRequest, rowKind string, expected *db.Thumbnail, g produced) {
	contentType := g.contentType
	row := pendingRow(q, rowKind)
	row.Original, row.ContentType = g.original, &contentType
	if !g.original {
		blobID, err := storeReserved(ctx, deps, store, g.body, q.version, q.serviceType, q.owner)
		if err != nil {
			slog.Warn("persist: blob upload failed, not persisting",
				"file_id", q.id, "version", q.version, "size", q.size, "err", err)
			return
		}
		row.BlobID = &blobID
	}
	if _, err := store.PutReady(ctx, row, expected); err != nil {
		slog.Warn("persist: PutReady failed", "file_id", q.id, "version", q.version, "size", q.size, "err", err)
	}
}

// generateInline builds an office or video thumbnail inside the request, under the async slot, when there is no usable DB.
func generateInline(step runner, deps Deps, worker *AsyncWorker, q thumbRequest) (cache.Entry, error) {
	if worker == nil {
		worker = NewAsyncWorker(deps)
	}
	sem := slots(deps.AsyncSem)
	if err := step.slot(sem); err != nil {
		return cache.Entry{}, err
	}
	defer sem.release()
	row := db.Thumbnail{FileID: q.id, Version: q.version, Size: q.size, Kind: string(q.p.kind), Lang: &q.lang, OwnerID: q.owner, ServiceType: q.serviceType}
	var out produced
	var fail *jobFailure
	if q.p.kind == kindVideo {
		out, fail = worker.runVideo(step.work, row, heldSlot)
	} else {
		out, fail = worker.runOffice(step.work, row, heldSlot)
	}
	return inlineAnswer(deps, q, out, fail)
}

func generateGIFInline(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, data []byte, info render.Info) (cache.Entry, error) {
	if worker == nil {
		worker = NewAsyncWorker(deps)
	}
	sem := slots(deps.AsyncSem)
	if err := step.slot(sem); err != nil {
		return cache.Entry{}, err
	}
	defer sem.release()
	out, fail := worker.gifOutput(step.work, data, info, q.size, heldSlot)
	return inlineAnswer(deps, q, out, fail)
}

func inlineAnswer(deps Deps, q thumbRequest, out produced, fail *jobFailure) (cache.Entry, error) {
	if fail != nil {
		return cache.Entry{}, inlineError(fail)
	}
	if out.original {
		return cache.Entry{}, originalAnswer{contentType: out.contentType}
	}
	deps.Cache.Put(q.key(cacheTagThumbnail), out.entry())
	return out.entry(), nil
}

func inlineError(f *jobFailure) error {
	switch f.kind {
	case failReleased:
		return errServerBusy()
	case failNotFound:
		return huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
	case failConnectivity:
		return huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
	case failUnsupported:
		return errFormatNotSupported()
	}
	slog.Warn("thumbnail generation failed", "err", f.err)
	return huma.NewError(http.StatusUnprocessableEntity, "thumbnail generation failed")
}

func regenerateGIF(step runner, deps Deps, worker *AsyncWorker, store *db.Store, q thumbRequest, expected *db.Thumbnail, data []byte, info render.Info) (cache.Entry, error) {
	if longSide(info) <= sideOf(q.size) {
		g := produced{contentType: mimeGIF, original: true}
		return answer(step, deps, store, q, string(kindGIF), expected, g)
	}
	if err := enqueueRow(step, store, q, expected, string(kindGIF)); err != nil {
		if isDBConnError(err) {
			return generateGIFInline(step, deps, worker, q, data, info)
		}
		return cache.Entry{}, errDependencyDown()
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

// deleteRowBlob deletes the blob of a row already removed from the DB and reports whether it is gone (a missing blob counts), so its tombstone can go.
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
