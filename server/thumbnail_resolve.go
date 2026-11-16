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
	if store == nil {
		return cache.Entry{}, errDependencyDown()
	}
	rows, err := store.FindPair(step.work, q.id, q.version)
	if err != nil {
		slog.Warn("resolveThumbnail: FindPair failed", "file_id", q.id, "version", q.version, "err", err)
		return cache.Entry{}, errDependencyDown()
	}
	r := rows[q.size]
	switch {
	case needsRegen(r, q.digest):
		sizes := []string{q.size}
		if needsRegen(rows[otherSize(q.size)], q.digest) {
			sizes = append(sizes, otherSize(q.size))
		}
		return regenerateAsync(step, deps, worker, q, rows, sizes)
	case r.Status == db.StatusReady:
		return serveReady(step, deps, worker, q, rows)
	case r.Status == db.StatusPending:
		fireAsyncAttempt(deps, worker, q.id, q.version)
		return cache.Entry{}, acceptedError{}
	case r.Status == db.StatusGenerating:
		return cache.Entry{}, acceptedError{}
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
		return regenerateAsync(step, deps, worker, q, rows, []string{q.size})
	}
	data, err := deps.Store.RetrieveData(step.work, *r.BlobID, r.Version, r.ServiceType, r.OwnerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			slog.Warn("resolveThumbnail: blob missing, regenerating",
				"file_id", q.id, "version", q.version, "size", q.size, "blob_id", *r.BlobID)
			return regenerateAsync(step, deps, worker, q, rows, []string{q.size})
		}
		slog.Warn("resolveThumbnail: blob read failed", "file_id", q.id, "blob_id", *r.BlobID, "err", err)
		return cache.Entry{}, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
	}
	return cache.Entry{Body: data, ContentType: contentType}, nil
}

func regenerateAsync(step runner, deps Deps, worker *AsyncWorker, q thumbRequest, rows map[string]*db.Thumbnail, sizes []string) (cache.Entry, error) {
	store := deps.dbStore()
	lang := q.lang
	var digest *string
	if q.digest != "" {
		digest = &q.digest
	}
	for _, size := range sizes {
		expected := rows[size]
		row := db.Thumbnail{
			FileID: q.id, Version: q.version, Size: size, Kind: string(q.p.kind),
			Digest: digest, Lang: &lang, OwnerID: q.owner, ServiceType: q.serviceType,
		}
		won, err := store.Enqueue(step.work, row, expected)
		if err != nil {
			slog.Warn("regenerateAsync: Enqueue failed", "file_id", q.id, "version", q.version, "size", size, "err", err)
			return cache.Entry{}, errDependencyDown()
		}
		if won && expected != nil {
			deleteRowBlob(step.work, deps.Store, expected, "regenerateAsync")
		}
	}
	fireAsyncAttempt(deps, worker, q.id, q.version)
	return cache.Entry{}, acceptedError{}
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
