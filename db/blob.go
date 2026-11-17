// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	BlobPending   = "PENDING"
	BlobTombstone = "TOMBSTONE"

	TombstoneMaxAttempts = 3
)

type Blob struct {
	BlobID      string
	OwnerID     string
	ServiceType string
	Version     int
	Attempts    int
	CreatedAt   time.Time
}

func tombstoneBlob(ctx context.Context, tx pgx.Tx, blobID, ownerID, serviceType string, version int) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status)
         VALUES ($1, $2, $3, $4, 'TOMBSTONE')
         ON CONFLICT (blob_id) DO UPDATE SET status='TOMBSTONE', updated_at=now()`,
		blobID, ownerID, serviceType, version)
	return err
}

// tombstoneRowBlob tombstones the blob of a row that stops pointing at it; "use original" rows have none.
func tombstoneRowBlob(ctx context.Context, tx pgx.Tx, r *Thumbnail) error {
	if r.BlobID == nil || r.Original {
		return nil
	}
	return tombstoneBlob(ctx, tx, *r.BlobID, r.OwnerID, r.ServiceType, r.Version)
}

// settleBlobs finishes the blob side of a row write: a won write tombstones the replaced blob, a lost one the new blob.
func settleBlobs(ctx context.Context, tx pgx.Tx, won bool, row Thumbnail, replaced *Thumbnail) error {
	if won {
		if replaced != nil {
			return tombstoneRowBlob(ctx, tx, replaced)
		}
		return nil
	}
	return tombstoneRowBlob(ctx, tx, &row)
}

// consumeReservation takes the reservation of a blob about to be published. It reports false when the reservation already expired into a tombstone, so the blob must not be published.
func consumeReservation(ctx context.Context, tx pgx.Tx, r *Thumbnail) (bool, error) {
	if r.BlobID == nil || r.Original {
		return true, nil
	}
	tag, err := tx.Exec(ctx, `DELETE FROM thumbnail_blob WHERE blob_id=$1 AND status='PENDING'`, *r.BlobID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var expired int
	err = tx.QueryRow(ctx, `SELECT 1 FROM thumbnail_blob WHERE blob_id=$1 AND status='TOMBSTONE'`, *r.BlobID).Scan(&expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// Reserve records a PENDING reservation; the caller uploads only when it succeeds.
func (s *Store) Reserve(ctx context.Context, blobID, ownerID, serviceType string, version int) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO thumbnail_blob (blob_id, owner_id, service_type, version, status) VALUES ($1, $2, $3, $4, 'PENDING')`,
		blobID, ownerID, serviceType, version); err != nil {
		return fmt.Errorf("db.Reserve: %w", err)
	}
	return nil
}

// Abandon turns the reservation of a blob whose upload failed into a tombstone.
func (s *Store) Abandon(ctx context.Context, blobID string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE thumbnail_blob SET status='TOMBSTONE', updated_at=now() WHERE blob_id=$1 AND status='PENDING'`, blobID); err != nil {
		return fmt.Errorf("db.Abandon: %w", err)
	}
	return nil
}

// DeleteTombstone forgets a tombstone whose blob is gone.
func (s *Store) DeleteTombstone(ctx context.Context, blobID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM thumbnail_blob WHERE blob_id=$1 AND status='TOMBSTONE'`, blobID); err != nil {
		return fmt.Errorf("db.DeleteTombstone: %w", err)
	}
	return nil
}

// ExpireReservations turns the PENDING reservations older than maxAge into tombstones in one statement.
func (s *CleanupConn) ExpireReservations(ctx context.Context, maxAge time.Duration) (int64, error) {
	tag, err := s.conn.Exec(ctx,
		`UPDATE thumbnail_blob SET status='TOMBSTONE', updated_at=now()
         WHERE status='PENDING' AND created_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(maxAge.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("db.ExpireReservations: %w", err)
	}
	return tag.RowsAffected(), nil
}

// FindTombstones returns up to limit tombstones still worth retrying, oldest first and strictly after the cursor.
func (s *CleanupConn) FindTombstones(ctx context.Context, after *Blob, limit int) ([]Blob, error) {
	var (
		rows pgx.Rows
		err  error
	)
	const cols = `blob_id, owner_id, service_type, version, attempts, created_at`
	if after == nil {
		rows, err = s.conn.Query(ctx,
			`SELECT `+cols+` FROM thumbnail_blob WHERE status='TOMBSTONE' AND attempts < $2
             ORDER BY created_at, blob_id LIMIT $1`, limit, TombstoneMaxAttempts)
	} else {
		rows, err = s.conn.Query(ctx,
			`SELECT `+cols+` FROM thumbnail_blob WHERE status='TOMBSTONE' AND attempts < $2
               AND (created_at, blob_id) > ($3, $4)
             ORDER BY created_at, blob_id LIMIT $1`, limit, TombstoneMaxAttempts, after.CreatedAt, after.BlobID)
	}
	if err != nil {
		return nil, fmt.Errorf("db.FindTombstones: %w", err)
	}
	defer rows.Close()
	var out []Blob
	for rows.Next() {
		var b Blob
		if err := rows.Scan(&b.BlobID, &b.OwnerID, &b.ServiceType, &b.Version, &b.Attempts, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("db.FindTombstones: scan: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *CleanupConn) DeleteTombstone(ctx context.Context, blobID string) error {
	if _, err := s.conn.Exec(ctx, `DELETE FROM thumbnail_blob WHERE blob_id=$1 AND status='TOMBSTONE'`, blobID); err != nil {
		return fmt.Errorf("db.DeleteTombstone: %w", err)
	}
	return nil
}

// RecordAttempt counts a real storage failure and returns the attempts so far.
func (s *CleanupConn) RecordAttempt(ctx context.Context, blobID string) (int, error) {
	var n int
	if err := s.conn.QueryRow(ctx,
		`UPDATE thumbnail_blob SET attempts=attempts+1, updated_at=now() WHERE blob_id=$1 AND status='TOMBSTONE' RETURNING attempts`,
		blobID).Scan(&n); err != nil {
		return 0, fmt.Errorf("db.RecordAttempt: %w", err)
	}
	return n, nil
}
