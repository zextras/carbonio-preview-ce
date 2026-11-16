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

const cleanupBatchSize = 500

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

// cleanupOnce deletes the blobs and rows of READY and LEGACY thumbnails not
// accessed for 30 days. "Use original" rows have no blob: only the row goes.
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
		return w.cleanupExpired(ctx, conn)
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
			row := rows[i]
			if ctx.Err() != nil {
				break
			}
			if row.BlobID != nil && !row.Original {
				berr := w.deps.Store.Delete(ctx, *row.BlobID, row.Version, row.ServiceType, row.OwnerID)
				if berr != nil && !errors.Is(berr, storage.ErrNotFound) {
					slog.Warn("AsyncWorker: cleanup blob delete failed, keeping row",
						"file_id", row.FileID, "version", row.Version, "size", row.Size, "blob_id", *row.BlobID, "err", berr)
					continue
				}
			}
			if derr := store.DeleteExpired(ctx, row.FileID, row.Version, row.Size); derr != nil {
				slog.Warn("AsyncWorker: cleanup row delete failed",
					"file_id", row.FileID, "version", row.Version, "size", row.Size, "err", derr)
				continue
			}
			deleted++
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
