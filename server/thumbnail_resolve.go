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

func otherSize(size string) string {
	if size == db.SizeS {
		return db.SizeM
	}
	return db.SizeS
}

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
	var rows map[string]*db.Thumbnail
	if store != nil {
		var err error
		if rows, err = store.FindPair(step.work, q.id, q.version); err != nil {
			slog.Warn("resolveThumbnail: FindPair failed", "file_id", q.id, "version", q.version, "err", err)
			store = nil
		}
	}
	if store == nil {
		if asyncKind(q.p.kind) {
			return cache.Entry{}, errDependencyDown()
		}
		return generateWithoutDB(step, deps, q)
	}
	r := rows[q.size]
	switch {
	case needsRegen(r, q.digest):
		sizes := []string{q.size}
		if needsRegen(rows[otherSize(q.size)], q.digest) {
			sizes = append(sizes, otherSize(q.size))
		}
		return regenerate(step, deps, worker, q, rows, sizes)
	case r.Status == db.StatusReady:
		return serveReady(step, deps, worker, q, rows)
	case r.Status == db.StatusPending:
		fireAsyncAttempt(deps, worker, q.id, q.version)
		return cache.Entry{}, pendingAnswer(step, deps, q, r)
	case r.Status == db.StatusGenerating:
		return cache.Entry{}, pendingAnswer(step, deps, q, r)
	case r.Status == db.StatusUnsupported:
		if r.Codec != nil && isSupportedVideoCodec(*r.Codec) {
			slog.Info("resolveThumbnail: codec now in supported list, re-enqueueing",
				"file_id", q.id, "version", q.version, "codec", *r.Codec)
			if err := store.ReenqueueUnsupported(step.work, q.id, q.version); err != nil {
				slog.Warn("resolveThumbnail: ReenqueueUnsupported failed", "err", err)
				return cache.Entry{}, errDependencyDown()
			}
			fireAsyncAttempt(deps, worker, q.id, q.version)
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

func serveReady(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, rows map[string]*db.Thumbnail) (cache.Entry, error) {
	r := rows[q.size]
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
		data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
		if err != nil {
			return cache.Entry{}, err
		}
		return cache.Entry{Body: data, ContentType: contentType}, nil
	}
	if r.BlobID == nil {
		return regenerate(step, deps, worker, q, rows, []string{q.size})
	}
	data, err := deps.Store.RetrieveData(step.work, *r.BlobID, r.Version, r.ServiceType, r.OwnerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			slog.Warn("resolveThumbnail: blob missing, regenerating",
				"file_id", q.id, "version", q.version, "size", q.size, "blob_id", *r.BlobID)
			return regenerate(step, deps, worker, q, rows, []string{q.size})
		}
		slog.Warn("resolveThumbnail: blob read failed", "file_id", q.id, "blob_id", *r.BlobID, "err", err)
		return cache.Entry{}, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
	}
	return cache.Entry{Body: data, ContentType: contentType}, nil
}

func pendingRow(q thumbRequest, size, rowKind string) db.Thumbnail {
	lang := q.lang
	row := db.Thumbnail{
		FileID: q.id, Version: q.version, Size: size, Kind: rowKind,
		Lang: &lang, OwnerID: q.owner, ServiceType: q.serviceType,
	}
	if q.digest != "" {
		digest := q.digest
		row.Digest = &digest
	}
	return row
}

func regenerate(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string) (cache.Entry, error) {
	switch q.p.kind {
	case kindPDF:
		return regeneratePDF(step, deps, q, rows, sizes)
	case kindImage:
		return regenerateImage(step, deps, worker, q, rows, sizes)
	}
	return regenerateAsync(step, deps, worker, q, rows, sizes)
}

func regenerateAsync(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string) (cache.Entry, error) {
	for _, size := range sizes {
		if err := enqueueRow(step, deps, q, rows, size, string(q.p.kind)); err != nil {
			return cache.Entry{}, err
		}
	}
	fireAsyncAttempt(deps, worker, q.id, q.version)
	return cache.Entry{}, acceptedError{}
}

func enqueueRow(step runner, deps Deps, q thumbRequest, rows map[string]*db.Thumbnail, size, rowKind string) error {
	expected := rows[size]
	won, err := deps.dbStore().Enqueue(step.work, pendingRow(q, size, rowKind), expected)
	if err != nil {
		slog.Warn("regenerate: Enqueue failed", "file_id", q.id, "version", q.version, "size", size, "err", err)
		return errDependencyDown()
	}
	if won && expected != nil {
		deleteRowBlob(step.work, deps.Store, expected, "regenerate")
	}
	return nil
}

func regeneratePDF(step runner, deps Deps, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string) (cache.Entry, error) {
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		return cache.Entry{}, err
	}
	set, err := step.pdfRenditions(data)
	if err != nil {
		return cache.Entry{}, err
	}
	return writeReady(step, deps, q, rows, sizes, set), nil
}

func regenerateImage(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string) (cache.Entry, error) {
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		return cache.Entry{}, err
	}
	info, err := step.imageInfo(q.p, data)
	if err != nil {
		return cache.Entry{}, err
	}
	if isAnimatedGIF(q.p, info) {
		return regenerateGIF(step, deps, worker, q, rows, sizes, data, info)
	}
	set, err := step.staticRenditions(q.p, data, info)
	if err != nil {
		return cache.Entry{}, err
	}
	return writeReady(step, deps, q, rows, sizes, set), nil
}

func writeReady(step runner, deps Deps, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string, set map[string]produced) cache.Entry {
	store := deps.dbStore()
	for _, size := range sizes {
		g := set[size]
		contentType := g.contentType
		row := pendingRow(q, size, string(q.p.kind))
		row.Original, row.ContentType = g.original, &contentType
		if !g.original {
			blobID, err := storeBlob(step.work, deps.Store, g.body, q.version, q.serviceType, q.owner)
			if err != nil {
				slog.Warn("writeReady: blob upload failed, serving without persisting",
					"file_id", q.id, "version", q.version, "size", size, "err", err)
				continue
			}
			row.BlobID = &blobID
		}
		expected := rows[size]
		won, err := store.PutReady(step.work, row, expected)
		if err != nil || !won {
			if err != nil {
				slog.Warn("writeReady: PutReady failed", "file_id", q.id, "version", q.version, "size", size, "err", err)
			}
			deleteRowBlob(step.work, deps.Store, &row, "writeReady")
			continue
		}
		if expected != nil {
			deleteRowBlob(step.work, deps.Store, expected, "writeReady")
		}
	}
	return cacheSet(deps, q, set)
}

func cacheSet(deps Deps, q thumbRequest, set map[string]produced) cache.Entry {
	for _, size := range []string{db.SizeS, db.SizeM} {
		deps.Cache.Put(q.keyFor(cacheTagThumbnail, size), set[size].entry())
	}
	return set[q.size].entry()
}

func generateWithoutDB(step runner, deps Deps, q thumbRequest) (cache.Entry, error) {
	data, err := step.fetch(deps.Store, q.id, q.version, q.serviceType, q.owner)
	if err != nil {
		return cache.Entry{}, err
	}
	var set map[string]produced
	if q.p.kind == kindPDF {
		set, err = step.pdfRenditions(data)
	} else {
		var info render.Info
		if info, err = step.imageInfo(q.p, data); err == nil {
			if isAnimatedGIF(q.p, info) {
				return cache.Entry{}, errDependencyDown()
			}
			set, err = step.staticRenditions(q.p, data, info)
		}
	}
	if err != nil {
		return cache.Entry{}, err
	}
	return cacheSet(deps, q, set), nil
}

func regenerateGIF(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string, data []byte, info render.Info) (cache.Entry, error) {
	store := deps.dbStore()
	enqueued := false
	originals := map[string]bool{}
	for _, size := range sizes {
		if longSide(info) > sideOf(size) {
			if err := enqueueRow(step, deps, q, rows, size, string(kindGIF)); err != nil {
				return cache.Entry{}, err
			}
			enqueued = true
			continue
		}
		contentType := mimeGIF
		row := pendingRow(q, size, string(kindGIF))
		row.Original, row.ContentType = true, &contentType
		expected := rows[size]
		won, err := store.PutReady(step.work, row, expected)
		if err != nil {
			slog.Warn("regenerateGIF: PutReady failed", "file_id", q.id, "version", q.version, "size", size, "err", err)
			return cache.Entry{}, errDependencyDown()
		}
		if won && expected != nil {
			deleteRowBlob(step.work, deps.Store, expected, "regenerateGIF")
		}
		originals[size] = true
	}
	if enqueued {
		fireAsyncAttempt(deps, worker, q.id, q.version)
	}
	for size := range originals {
		deps.Cache.Put(q.keyFor(cacheTagThumbnail, size), cache.Entry{Body: data, ContentType: mimeGIF})
	}
	if originals[q.size] {
		return cache.Entry{Body: data, ContentType: mimeGIF}, nil
	}
	return cache.Entry{}, cacheInterim(step, deps, q, data)
}

// A failed frame still answers an empty 202: the job is queued either way.
func cacheInterim(step runner, deps Deps, q thumbRequest, data []byte) acceptedError {
	set, err := step.frameRenditions(data)
	if err != nil {
		slog.Warn("interim frame failed", "file_id", q.id, "version", q.version, "err", err)
		return acceptedError{}
	}
	for _, size := range []string{db.SizeS, db.SizeM} {
		deps.Cache.Put(q.keyFor(cacheTagInterim, size), set[size].entry())
	}
	f := set[q.size]
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
	set, err := step.frameRenditions(data)
	if err != nil {
		return cache.Entry{}, err
	}
	deps.Cache.Delete(q.key(cacheTagInterim))
	return set[q.size].entry(), nil
}

func deleteRowBlob(ctx context.Context, store storage.Client, row *db.Thumbnail, op string) {
	if row.BlobID == nil || row.Original {
		return
	}
	if err := store.Delete(ctx, *row.BlobID, row.Version, row.ServiceType, row.OwnerID); err != nil && !errors.Is(err, storage.ErrNotFound) {
		slog.Warn(op+": blob delete failed", "file_id", row.FileID, "blob_id", *row.BlobID, "err", err)
	}
}

// Best effort: a worker that is not running, a full instance or a lost claim leave the rows for the next sweep tick.
func fireAsyncAttempt(deps Deps, worker *AsyncWorker, fileID string, version int) {
	if worker == nil || deps.dbStore() == nil {
		return
	}
	go worker.runOne(fileID, version)
}
