// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StatusPending     = "PENDING"
	StatusGenerating  = "GENERATING"
	StatusReady       = "READY"
	StatusUnsupported = "UNSUPPORTED"
	StatusFailed      = "FAILED"
	StatusLegacy      = "LEGACY"

	SizeS = "s"
	SizeM = "m"
)

var AllSizes = []string{SizeS, SizeM}

// ---------------------------------------------------------------------------
// PoolConfig holds connection-pool tuning parameters.
// ---------------------------------------------------------------------------

// PoolConfig holds operator-tunable connection-pool settings.
type PoolConfig struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
}

type Thumbnail struct {
	FileID         string
	Version        int
	Size           string
	Status         string
	Kind           string
	Digest         *string
	Lang           *string
	BlobID         *string
	Original       bool
	ContentType    *string
	OwnerID        string
	ServiceType    string
	ClaimedBy      *string
	ClaimedAt      *time.Time
	Attempts       int
	Codec          *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAccessedAt time.Time
}

// Store is the database access layer for carbonio-preview-ce.
// All methods are safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store, verifies connectivity (ping), and returns an error if
// the database is unreachable.  dsn must be a valid libpq-compatible DSN or
// postgres:// URL.  pc controls pool sizing and connection lifetime; zero
// values fall back to pgxpool defaults.
func New(ctx context.Context, dsn string, pc PoolConfig) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse DSN: %w", err)
	}

	if pc.MaxConns > 0 {
		cfg.MaxConns = pc.MaxConns
	}
	if pc.MinConns > 0 {
		cfg.MinConns = pc.MinConns
	}
	if pc.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = pc.MaxConnLifetime
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping failed: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Close shuts down the connection pool.  It is safe to call more than once.
func (s *Store) Close() {
	s.pool.Close()
}

// Migrate runs all pending SQL migrations from db/migration/*.sql against the
// target database.  It is idempotent: already-applied migrations are skipped.
// "no change" is not treated as an error.
func (s *Store) Migrate(ctx context.Context) error {
	m, closeFn, err := s.migrator(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: run migrations: %w", err)
	}
	return nil
}

func (s *Store) migrator(ctx context.Context) (*migrate.Migrate, func(), error) {
	srcDriver, err := iofs.New(MigrationFS, "migration")
	if err != nil {
		return nil, nil, fmt.Errorf("db: iofs source: %w", err)
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("db: acquire connection for migration: %w", err)
	}

	// Rebuilt from the pool's config so passwords with special characters are percent-encoded.
	connConfig := conn.Conn().Config()
	u := &url.URL{
		Scheme: "pgx5",
		User:   url.UserPassword(connConfig.User, connConfig.Password),
		Host:   fmt.Sprintf("%s:%d", connConfig.Host, connConfig.Port),
		Path:   "/" + connConfig.Database,
	}

	m, err := migrate.NewWithSourceInstance("iofs", srcDriver, u.String())
	if err != nil {
		conn.Release()
		return nil, nil, fmt.Errorf("db: build migrator: %w", err)
	}
	return m, func() {
		m.Close()
		conn.Release()
	}, nil
}

const selectCols = `file_id, version, size, status, kind, digest, lang, blob_id, original, content_type,
    owner_id, service_type, claimed_by, claimed_at, attempts, codec, created_at, updated_at, last_accessed_at`

func scanThumbnail(rows pgx.Rows) (Thumbnail, error) {
	var t Thumbnail
	err := rows.Scan(
		&t.FileID, &t.Version, &t.Size, &t.Status, &t.Kind, &t.Digest, &t.Lang, &t.BlobID,
		&t.Original, &t.ContentType, &t.OwnerID, &t.ServiceType, &t.ClaimedBy, &t.ClaimedAt,
		&t.Attempts, &t.Codec, &t.CreatedAt, &t.UpdatedAt, &t.LastAccessedAt,
	)
	return t, err
}

func scanAll(rows pgx.Rows) ([]Thumbnail, error) {
	defer rows.Close()
	var out []Thumbnail
	for rows.Next() {
		t, err := scanThumbnail(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) FindPair(ctx context.Context, fileID string, version int) (map[string]*Thumbnail, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+selectCols+` FROM thumbnail WHERE file_id=$1 AND version=$2`,
		fileID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("db.FindPair: %w", err)
	}
	all, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.FindPair: scan: %w", err)
	}
	out := make(map[string]*Thumbnail, len(all))
	for i := range all {
		out[all[i].Size] = &all[i]
	}
	return out, nil
}

const casWhere = `file_id=$1 AND version=$2 AND size=$3 AND status=$4
    AND blob_id IS NOT DISTINCT FROM $5 AND digest IS NOT DISTINCT FROM $6`

// PutReady and Enqueue are compare-and-set: expected == nil inserts and loses to any existing row,
// otherwise the row is replaced only while it still equals expected. They report whether a row was written.
// In the same transaction a won write consumes the reservation of its blob and tombstones the blob it replaced,
// a lost one tombstones the new blob.
func (s *Store) PutReady(ctx context.Context, row Thumbnail, expected *Thumbnail) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("db.PutReady: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if ok, err := consumeReservation(ctx, tx, &row); err != nil {
		return false, fmt.Errorf("db.PutReady: %w", err)
	} else if !ok {
		return false, tx.Commit(ctx)
	}
	var tag pgconn.CommandTag
	if expected == nil {
		tag, err = tx.Exec(ctx,
			`INSERT INTO thumbnail
                (file_id, version, size, status, kind, digest, lang, blob_id, original, content_type,
                 owner_id, service_type, attempts, created_at, updated_at, last_accessed_at)
             VALUES ($1, $2, $3, 'READY', $4, $5, $6, $7, $8, $9, $10, $11, 0, now(), now(), now())
             ON CONFLICT (file_id, version, size) DO NOTHING`,
			row.FileID, row.Version, row.Size, row.Kind, row.Digest, row.Lang, row.BlobID, row.Original,
			row.ContentType, row.OwnerID, row.ServiceType,
		)
	} else {
		tag, err = tx.Exec(ctx,
			`UPDATE thumbnail
             SET status='READY', kind=$7, digest=$8, lang=$9, blob_id=$10, original=$11, content_type=$12,
                 owner_id=$13, service_type=$14, attempts=0, codec=NULL, claimed_by=NULL, claimed_at=NULL,
                 updated_at=now(), last_accessed_at=now()
             WHERE `+casWhere,
			row.FileID, row.Version, row.Size, expected.Status, expected.BlobID, expected.Digest,
			row.Kind, row.Digest, row.Lang, row.BlobID, row.Original, row.ContentType, row.OwnerID, row.ServiceType,
		)
	}
	if err != nil {
		return false, fmt.Errorf("db.PutReady: %w", err)
	}
	won := tag.RowsAffected() == 1
	if err := settleBlobs(ctx, tx, won, row, expected); err != nil {
		return false, fmt.Errorf("db.PutReady: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("db.PutReady: commit: %w", err)
	}
	return won, nil
}

func (s *Store) Enqueue(ctx context.Context, row Thumbnail, expected *Thumbnail) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("db.Enqueue: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tag pgconn.CommandTag
	if expected == nil {
		tag, err = tx.Exec(ctx,
			`INSERT INTO thumbnail
                (file_id, version, size, status, kind, digest, lang, owner_id, service_type,
                 attempts, created_at, updated_at, last_accessed_at)
             VALUES ($1, $2, $3, 'PENDING', $4, $5, $6, $7, $8, 0, now(), now(), now())
             ON CONFLICT (file_id, version, size) DO NOTHING`,
			row.FileID, row.Version, row.Size, row.Kind, row.Digest, row.Lang, row.OwnerID, row.ServiceType,
		)
	} else {
		tag, err = tx.Exec(ctx,
			`UPDATE thumbnail
             SET status='PENDING', kind=$7, digest=$8, lang=$9, blob_id=NULL, original=false, content_type=NULL,
                 owner_id=$10, service_type=$11, attempts=0, codec=NULL, claimed_by=NULL, claimed_at=NULL,
                 created_at=now(), updated_at=now()
             WHERE `+casWhere,
			row.FileID, row.Version, row.Size, expected.Status, expected.BlobID, expected.Digest,
			row.Kind, row.Digest, row.Lang, row.OwnerID, row.ServiceType,
		)
	}
	if err != nil {
		return false, fmt.Errorf("db.Enqueue: %w", err)
	}
	won := tag.RowsAffected() == 1
	if won && expected != nil {
		if err := tombstoneRowBlob(ctx, tx, expected); err != nil {
			return false, fmt.Errorf("db.Enqueue: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("db.Enqueue: commit: %w", err)
	}
	return won, nil
}

// Claim atomically moves the PENDING row of (fileID, version, size) to GENERATING
// for instanceID and returns it; an empty result means the claim was lost.
func (s *Store) Claim(ctx context.Context, fileID string, version int, size, instanceID string) ([]Thumbnail, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE thumbnail
         SET status='GENERATING', claimed_by=$1, claimed_at=now(), updated_at=now()
         WHERE file_id=$2 AND version=$3 AND size=$4 AND status='PENDING'
         RETURNING `+selectCols,
		instanceID, fileID, version, size,
	)
	if err != nil {
		return nil, fmt.Errorf("db.Claim: %w", err)
	}
	out, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.Claim: scan: %w", err)
	}
	return out, nil
}

// ReclaimStale takes over GENERATING rows whose claimed_at is older than ttl.
// Each reclaimed row has its attempts incremented (the only path that does so).
func (s *Store) ReclaimStale(ctx context.Context, instanceID string, ttl time.Duration) ([]Thumbnail, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE thumbnail
         SET status='GENERATING', claimed_by=$1, claimed_at=now(),
             attempts=attempts+1, updated_at=now()
         WHERE status='GENERATING' AND claimed_at < now() - $2::interval
         RETURNING `+selectCols,
		instanceID,
		fmt.Sprintf("%d seconds", int(ttl.Seconds())),
	)
	if err != nil {
		return nil, fmt.Errorf("db.ReclaimStale: %w", err)
	}
	out, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.ReclaimStale: scan: %w", err)
	}
	return out, nil
}

// FindPendingNewest returns up to limit PENDING rows, newest first.
func (s *Store) FindPendingNewest(ctx context.Context, limit int) ([]Thumbnail, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+selectCols+`
         FROM thumbnail
         WHERE status='PENDING'
         ORDER BY created_at DESC
         LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("db.FindPendingNewest: %w", err)
	}
	out, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.FindPendingNewest: scan: %w", err)
	}
	return out, nil
}

// MarkReady moves the GENERATING rows owned by instanceID to READY in one
// transaction and returns the sizes it wrote; rows that are gone or no longer
// owned are skipped, and their new blob is tombstoned. A written row consumes the reservation of its blob.
func (s *Store) MarkReady(ctx context.Context, instanceID string, rows []Thumbnail) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db.MarkReady: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var written []string
	for _, r := range rows {
		if ok, err := consumeReservation(ctx, tx, &r); err != nil {
			return nil, fmt.Errorf("db.MarkReady: %w", err)
		} else if !ok {
			continue
		}
		tag, err := tx.Exec(ctx,
			`UPDATE thumbnail
             SET status='READY', blob_id=$1, original=$2, content_type=$3,
                 claimed_by=NULL, claimed_at=NULL, updated_at=now(), last_accessed_at=now()
             WHERE file_id=$4 AND version=$5 AND size=$6 AND status='GENERATING' AND claimed_by=$7`,
			r.BlobID, r.Original, r.ContentType, r.FileID, r.Version, r.Size, instanceID,
		)
		if err != nil {
			return nil, fmt.Errorf("db.MarkReady: %w", err)
		}
		won := tag.RowsAffected() == 1
		if won {
			written = append(written, r.Size)
		}
		if err := settleBlobs(ctx, tx, won, r, nil); err != nil {
			return nil, fmt.Errorf("db.MarkReady: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("db.MarkReady: commit: %w", err)
	}
	return written, nil
}

func (s *Store) execClaimed(ctx context.Context, op, set string, fileID string, version int, instanceID string, sizes []string, extra ...any) error {
	args := append([]any{fileID, version, instanceID, sizes}, extra...)
	_, err := s.pool.Exec(ctx,
		`UPDATE thumbnail SET `+set+`, updated_at=now()
         WHERE file_id=$1 AND version=$2 AND status='GENERATING' AND claimed_by=$3 AND size = ANY($4)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("db.%s: %w", op, err)
	}
	return nil
}

// MarkUnsupported moves the claimed rows to UNSUPPORTED (terminal until the
// codec list grows). The caller logs the reason; it is not persisted.
func (s *Store) MarkUnsupported(ctx context.Context, fileID string, version int, instanceID string, sizes []string) error {
	return s.execClaimed(ctx, "MarkUnsupported", `status='UNSUPPORTED', claimed_by=NULL, claimed_at=NULL`,
		fileID, version, instanceID, sizes)
}

// MarkFailed moves the claimed rows to FAILED (terminal: attempts exhausted or
// source gone). The caller logs the reason; it is not persisted.
func (s *Store) MarkFailed(ctx context.Context, fileID string, version int, instanceID string, sizes []string) error {
	return s.execClaimed(ctx, "MarkFailed", `status='FAILED', claimed_by=NULL, claimed_at=NULL`,
		fileID, version, instanceID, sizes)
}

// Release returns the claimed rows to PENDING without counting an attempt.
func (s *Store) Release(ctx context.Context, fileID string, version int, instanceID string, sizes []string) error {
	return s.execClaimed(ctx, "Release", `status='PENDING', claimed_by=NULL, claimed_at=NULL`,
		fileID, version, instanceID, sizes)
}

// ReleaseWithAttempt returns the claimed rows to PENDING and counts an attempt.
func (s *Store) ReleaseWithAttempt(ctx context.Context, fileID string, version int, instanceID string, sizes []string) error {
	return s.execClaimed(ctx, "ReleaseWithAttempt", `status='PENDING', claimed_by=NULL, claimed_at=NULL, attempts=attempts+1`,
		fileID, version, instanceID, sizes)
}

// SetCodec records the detected codec on the claimed rows.
func (s *Store) SetCodec(ctx context.Context, fileID string, version int, instanceID string, sizes []string, codec string) error {
	return s.execClaimed(ctx, "SetCodec", `codec=$5`, fileID, version, instanceID, sizes, codec)
}

// Heartbeat refreshes claimed_at of the claimed rows so ReclaimStale never
// takes over a job that is making progress.
func (s *Store) Heartbeat(ctx context.Context, fileID string, version int, instanceID string, sizes []string) error {
	return s.execClaimed(ctx, "Heartbeat", `claimed_at=now()`, fileID, version, instanceID, sizes)
}

// ReenqueueUnsupported moves the UNSUPPORTED row of (fileID, version, size) back to PENDING
// keeping the stored codec, so the worker skips the probe.
func (s *Store) ReenqueueUnsupported(ctx context.Context, fileID string, version int, size string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE thumbnail
         SET status='PENDING', claimed_by=NULL, claimed_at=NULL, updated_at=now()
         WHERE file_id=$1 AND version=$2 AND size=$3 AND status='UNSUPPORTED'`,
		fileID, version, size,
	)
	if err != nil {
		return fmt.Errorf("db.ReenqueueUnsupported: %w", err)
	}
	return nil
}

// DeleteClaimed deletes the claimed rows as if the thumbnail had never been
// requested; rows owned by another instance or in another status are kept.
func (s *Store) DeleteClaimed(ctx context.Context, fileID string, version int, instanceID string, sizes []string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM thumbnail
         WHERE file_id=$1 AND version=$2 AND status='GENERATING' AND claimed_by=$3 AND size = ANY($4)`,
		fileID, version, instanceID, sizes,
	)
	if err != nil {
		return fmt.Errorf("db.DeleteClaimed: %w", err)
	}
	return nil
}

// TouchReady records an access on a READY row whose last access is older than
// one day. It writes nothing otherwise.
func (s *Store) TouchReady(ctx context.Context, fileID string, version int, size string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE thumbnail SET last_accessed_at=now()
         WHERE file_id=$1 AND version=$2 AND size=$3 AND status='READY'
           AND last_accessed_at < now() - interval '1 day'`,
		fileID, version, size,
	)
	if err != nil {
		return fmt.Errorf("db.TouchReady: %w", err)
	}
	return nil
}

// DeleteFile deletes every row of the file and tombstones their blobs in one transaction. It returns the deleted rows,
// whose blobs the caller deletes after the commit.
func (s *Store) DeleteFile(ctx context.Context, fileID string) ([]Thumbnail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db.DeleteFile: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `DELETE FROM thumbnail WHERE file_id=$1 RETURNING `+selectCols, fileID)
	if err != nil {
		return nil, fmt.Errorf("db.DeleteFile: %w", err)
	}
	deleted, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.DeleteFile: scan: %w", err)
	}
	for i := range deleted {
		if err := tombstoneRowBlob(ctx, tx, &deleted[i]); err != nil {
			return nil, fmt.Errorf("db.DeleteFile: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("db.DeleteFile: commit: %w", err)
	}
	return deleted, nil
}

// CleanupConn is the connection holding the cleanup advisory lock; the job runs
// its queries on it so a run never needs a second pooled connection.
type CleanupConn struct {
	conn *pgxpool.Conn
}

// FindExpired returns up to limit READY or LEGACY rows not accessed for 30
// days, ordered by (last_accessed_at, file_id, version, size) and strictly
// after the cursor row when after is non-nil.
func (s *CleanupConn) FindExpired(ctx context.Context, after *Thumbnail, limit int) ([]Thumbnail, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if after == nil {
		rows, err = s.conn.Query(ctx,
			`SELECT `+selectCols+` FROM thumbnail
             WHERE status IN ('READY','LEGACY') AND last_accessed_at < now() - interval '30 days'
             ORDER BY last_accessed_at, file_id, version, size
             LIMIT $1`,
			limit,
		)
	} else {
		rows, err = s.conn.Query(ctx,
			`SELECT `+selectCols+` FROM thumbnail
             WHERE status IN ('READY','LEGACY') AND last_accessed_at < now() - interval '30 days'
               AND (last_accessed_at, file_id, version, size) > ($2, $3, $4, $5)
             ORDER BY last_accessed_at, file_id, version, size
             LIMIT $1`,
			limit, after.LastAccessedAt, after.FileID, after.Version, after.Size,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("db.FindExpired: %w", err)
	}
	out, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.FindExpired: scan: %w", err)
	}
	return out, nil
}

// DeleteExpired deletes the row only if it is still READY or LEGACY and still
// not accessed for 30 days, so an access after FindExpired saves it. Its blob is tombstoned in the same transaction.
// It returns the deleted row, or nil when it was kept.
func (s *CleanupConn) DeleteExpired(ctx context.Context, fileID string, version int, size string) (*Thumbnail, error) {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db.DeleteExpired: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`DELETE FROM thumbnail
         WHERE file_id=$1 AND version=$2 AND size=$3 AND status IN ('READY','LEGACY')
           AND last_accessed_at < now() - interval '30 days'
         RETURNING `+selectCols,
		fileID, version, size,
	)
	if err != nil {
		return nil, fmt.Errorf("db.DeleteExpired: %w", err)
	}
	deleted, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.DeleteExpired: scan: %w", err)
	}
	if len(deleted) == 0 {
		return nil, nil
	}
	if err := tombstoneRowBlob(ctx, tx, &deleted[0]); err != nil {
		return nil, fmt.Errorf("db.DeleteExpired: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("db.DeleteExpired: commit: %w", err)
	}
	return &deleted[0], nil
}

// cleanupLockKey is the fixed Postgres advisory-lock key serialising the
// cleanup job across instances.
const cleanupLockKey int64 = 0x7a65787472617663

// unlockTimeout bounds releasing the cleanup lock after the caller's context ended.
const unlockTimeout = 5 * time.Second

// WithCleanupLock runs fn while holding the cleanup advisory lock on a
// dedicated connection, which fn receives for all of its queries. It returns false without running fn when another
// session already holds the lock.
func (s *Store) WithCleanupLock(ctx context.Context, fn func(context.Context, *CleanupConn) error) (bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("db.WithCleanupLock: acquire: %w", err)
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, cleanupLockKey).Scan(&got); err != nil {
		return false, fmt.Errorf("db.WithCleanupLock: lock: %w", err)
	}
	if !got {
		return false, nil
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(uctx, `SELECT pg_advisory_unlock($1)`, cleanupLockKey).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Conn().Close(uctx)
		}
	}()
	return true, fn(ctx, &CleanupConn{conn: conn})
}
