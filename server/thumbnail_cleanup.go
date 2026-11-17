// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

const (
	cleanupBatchSize = 500
	reservationTTL   = 24 * time.Hour
)

var cleanupInterval = time.Hour

func (w *AsyncWorker) runCleanupLoop(ctx context.Context) {
	for {
		wait := cleanupInterval
		if !w.cleanupOnce(ctx) {
			wait = w.sweepInterval
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

// cleanupOnce sweeps the READY and LEGACY thumbnails not accessed for 30 days (rows and blob tombstones in one
// transaction, blobs after the commit), then purges the tombstones. "Use original" rows have no blob.
// FAILED, UNSUPPORTED, PENDING and GENERATING rows are never swept. It reports false only when the DB is not ready. A run is skipped when
// another instance holds the advisory lock.
func (w *AsyncWorker) cleanupOnce(ctx context.Context) bool {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("AsyncWorker: cleanup panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	store := w.db()
	if store == nil {
		return false
	}
	locked, err := store.WithCleanupLock(ctx, func(ctx context.Context, conn *db.CleanupConn) error {
		if err := w.cleanupExpired(ctx, conn); err != nil {
			return err
		}
		return w.purgeTombstones(ctx, conn)
	})
	switch {
	case err != nil && ctx.Err() == nil:
		slog.Warn("AsyncWorker: cleanup failed", "err", err)
	case !locked:
		slog.Debug("AsyncWorker: cleanup skipped, another instance holds the lock")
	}
	return true
}

func (w *AsyncWorker) cleanupExpired(ctx context.Context, store *db.CleanupConn) error {
	var cursor *db.Thumbnail
	deleted := 0
	for ctx.Err() == nil {
		rows, err := store.FindExpired(ctx, cursor, cleanupBatchSize)
		if err != nil {
			return err
		}
		for i := range rows {
			if ctx.Err() != nil {
				break
			}
			gone, derr := store.DeleteExpired(ctx, rows[i].FileID, rows[i].Version, rows[i].Size)
			if derr != nil {
				slog.Warn("AsyncWorker: cleanup row delete failed",
					"file_id", rows[i].FileID, "version", rows[i].Version, "size", rows[i].Size, "err", derr)
				continue
			}
			if gone == nil {
				continue
			}
			deleted++
			if gone.BlobID != nil && !gone.Original && deleteRowBlob(ctx, w.deps.Store, gone, "AsyncWorker") {
				if terr := store.DeleteTombstone(ctx, *gone.BlobID); terr != nil {
					slog.Warn("AsyncWorker: cleanup tombstone delete failed", "blob_id", *gone.BlobID, "err", terr)
				}
			}
		}
		if len(rows) < cleanupBatchSize {
			break
		}
		cursor = &rows[len(rows)-1]
	}
	if deleted > 0 {
		slog.Info("AsyncWorker: cleanup removed expired thumbnails", "count", deleted)
	}
	return nil
}

// purgeTombstones turns the reservations older than reservationTTL into tombstones and deletes the blob of every tombstone.
// A missing blob counts as deleted; only a rejection by a reachable storage counts an attempt, an outage never does.
func (w *AsyncWorker) purgeTombstones(ctx context.Context, store *db.CleanupConn) error {
	if n, err := store.ExpireReservations(ctx, reservationTTL); err != nil {
		return err
	} else if n > 0 {
		slog.Warn("AsyncWorker: unconfirmed thumbnail blob reservations expired into tombstones", "count", n)
	}
	var cursor *db.Blob
	purged := 0
	for ctx.Err() == nil {
		batch, err := store.FindTombstones(ctx, cursor, cleanupBatchSize)
		if err != nil {
			return err
		}
		for _, b := range batch {
			if ctx.Err() != nil {
				break
			}
			berr := w.deps.Store.Delete(ctx, b.BlobID, b.Version, b.ServiceType, b.OwnerID)
			switch {
			case berr == nil || errors.Is(berr, storage.ErrNotFound):
				if derr := store.DeleteTombstone(ctx, b.BlobID); derr != nil {
					return derr
				}
				purged++
			case errors.Is(berr, storage.ErrRejected):
				n, aerr := store.RecordAttempt(ctx, b.BlobID)
				if aerr != nil {
					return aerr
				}
				if n >= db.TombstoneMaxAttempts {
					slog.Warn("AsyncWorker: tombstone purge giving up, the blob stays on storages", "blob_id", b.BlobID, "attempts", n, "err", berr)
				}
			default:
				slog.Warn("AsyncWorker: tombstone purge deferred", "blob_id", b.BlobID, "err", berr)
			}
		}
		if len(batch) < cleanupBatchSize {
			break
		}
		cursor = &batch[len(batch)-1]
	}
	if purged > 0 {
		slog.Info("AsyncWorker: purged thumbnail blob tombstones", "count", purged)
	}
	return nil
}
