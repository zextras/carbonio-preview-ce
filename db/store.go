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
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Status constants — mirror WSC VideoPreviewStatus enum exactly.
// ---------------------------------------------------------------------------

const (
	StatusPending     = "PENDING"
	StatusGenerating  = "GENERATING"
	StatusReady       = "READY"
	StatusUnsupported = "UNSUPPORTED"
	StatusFailed      = "FAILED"
)

// ---------------------------------------------------------------------------
// PoolConfig holds connection-pool tuning parameters.
// ---------------------------------------------------------------------------

// PoolConfig holds operator-tunable connection-pool settings.
type PoolConfig struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
}

// ---------------------------------------------------------------------------
// VideoPreview is the Go representation of a video_preview row.
// ---------------------------------------------------------------------------

// VideoPreview maps one row of the video_preview table.
type VideoPreview struct {
	FileID      string
	Version     int
	Status      string
	PreviewID   *string
	OwnerID     string
	ServiceType string
	ClaimedBy   *string
	ClaimedAt   *time.Time
	LastError   *string
	Attempts    int
	Codec       *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ---------------------------------------------------------------------------
// Store wraps a pgxpool.Pool and exposes all video_preview operations.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Migrate applies all embedded SQL migrations idempotently.
// ---------------------------------------------------------------------------

// Migrate runs all pending SQL migrations from db/migration/*.sql against the
// target database.  It is idempotent: already-applied migrations are skipped.
// "no change" is not treated as an error.
func (s *Store) Migrate(ctx context.Context) error {
	// Build the source driver from the embedded FS.
	srcDriver, err := iofs.New(MigrationFS, "migration")
	if err != nil {
		return fmt.Errorf("db: iofs source: %w", err)
	}

	// Acquire a single connection from the pool for migration.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: acquire connection for migration: %w", err)
	}
	defer conn.Release()

	// Build the pgx5:// URL expected by the golang-migrate pgx/v5 driver.
	// We reconstruct it from the pool's parsed config so passwords with special
	// characters are handled correctly via url.UserPassword (percent-encoding).
	connConfig := conn.Conn().Config()
	u := &url.URL{
		Scheme: "pgx5",
		User:   url.UserPassword(connConfig.User, connConfig.Password),
		Host:   fmt.Sprintf("%s:%d", connConfig.Host, connConfig.Port),
		Path:   "/" + connConfig.Database,
	}
	pgURL := u.String()

	// NewWithSourceInstance is required when using an in-process source driver
	// (like iofs) instead of a URL string. The source name "iofs" is arbitrary
	// (only used in error messages). The pgx5:// URL selects the registered driver.
	m, err := migrate.NewWithSourceInstance("iofs", srcDriver, pgURL)
	if err != nil {
		return fmt.Errorf("db: build migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: run migrations: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helper: scan a VideoPreview row from a pgx.Rows.
// ---------------------------------------------------------------------------

func scanVideoPreview(rows pgx.Rows) (VideoPreview, error) {
	var vp VideoPreview
	err := rows.Scan(
		&vp.FileID,
		&vp.Version,
		&vp.Status,
		&vp.PreviewID,
		&vp.OwnerID,
		&vp.ServiceType,
		&vp.ClaimedBy,
		&vp.ClaimedAt,
		&vp.LastError,
		&vp.Attempts,
		&vp.Codec,
		&vp.CreatedAt,
		&vp.UpdatedAt,
	)
	return vp, err
}

// scanOne collects exactly one VideoPreview from rows, returning nil if the
// result set is empty, or an error on scan failure.
func scanOne(rows pgx.Rows) (*VideoPreview, error) {
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	vp, err := scanVideoPreview(rows)
	if err != nil {
		return nil, err
	}
	return &vp, nil
}

// scanAll collects all VideoPreview rows.
func scanAll(rows pgx.Rows) ([]VideoPreview, error) {
	defer rows.Close()
	var out []VideoPreview
	for rows.Next() {
		vp, err := scanVideoPreview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, vp)
	}
	return out, rows.Err()
}

// selectCols is the canonical column list for SELECT * equivalents.
const selectCols = `file_id, version, status, preview_id, owner_id, service_type,
    claimed_by, claimed_at, last_error, attempts, codec, created_at, updated_at`

// ---------------------------------------------------------------------------
// Query methods
// ---------------------------------------------------------------------------

// Find returns the video_preview row for (fileID, version), or nil if absent.
func (s *Store) Find(ctx context.Context, fileID string, version int) (*VideoPreview, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+selectCols+` FROM video_preview WHERE file_id=$1 AND version=$2`,
		fileID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("db.Find: %w", err)
	}
	vp, err := scanOne(rows)
	if err != nil {
		return nil, fmt.Errorf("db.Find: scan: %w", err)
	}
	return vp, nil
}

// ---------------------------------------------------------------------------
// Write methods
// ---------------------------------------------------------------------------

// EnqueueIfAbsent inserts a PENDING row for (fileID, version).
// If a row already exists (any status) the INSERT is silently skipped.
func (s *Store) EnqueueIfAbsent(ctx context.Context, fileID string, version int, ownerID, serviceType string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO video_preview
            (file_id, version, status, owner_id, service_type, attempts, created_at, updated_at)
         VALUES ($1, $2, 'PENDING', $3, $4, 0, now(), now())
         ON CONFLICT (file_id, version) DO NOTHING`,
		fileID, version, ownerID, serviceType,
	)
	if err != nil {
		return fmt.Errorf("db.EnqueueIfAbsent: %w", err)
	}
	return nil
}

// Claim atomically transitions a PENDING row to GENERATING, recording the
// instanceID as the owner.  Returns true iff exactly one row was updated
// (i.e. this instance won the claim race).
func (s *Store) Claim(ctx context.Context, fileID string, version int, instanceID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='GENERATING', claimed_by=$1, claimed_at=now(), updated_at=now()
         WHERE file_id=$2 AND version=$3 AND status='PENDING'`,
		instanceID, fileID, version,
	)
	if err != nil {
		return false, fmt.Errorf("db.Claim: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReclaimStale takes over GENERATING rows whose claimed_at is older than ttl.
// Each reclaimed row has its attempts incremented (the only path that does so).
// Returns all reclaimed rows (for the worker to restart generation).
func (s *Store) ReclaimStale(ctx context.Context, instanceID string, ttl time.Duration) ([]VideoPreview, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE video_preview
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
	vps, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.ReclaimStale: scan: %w", err)
	}
	return vps, nil
}

// FindPendingNewest returns up to limit PENDING rows ordered by created_at DESC
// (newest first, so freshest requests are processed first).
func (s *Store) FindPendingNewest(ctx context.Context, limit int) ([]VideoPreview, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+selectCols+`
         FROM video_preview
         WHERE status='PENDING'
         ORDER BY created_at DESC
         LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("db.FindPendingNewest: %w", err)
	}
	vps, err := scanAll(rows)
	if err != nil {
		return nil, fmt.Errorf("db.FindPendingNewest: scan: %w", err)
	}
	return vps, nil
}

// MarkReady transitions a GENERATING row owned by instanceID to READY,
// recording previewID and clearing claimed_by/claimed_at/last_error.
func (s *Store) MarkReady(ctx context.Context, fileID string, version int, instanceID, previewID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='READY', preview_id=$1,
             claimed_by=NULL, claimed_at=NULL, last_error=NULL, updated_at=now()
         WHERE file_id=$2 AND version=$3 AND status='GENERATING' AND claimed_by=$4`,
		previewID, fileID, version, instanceID,
	)
	if err != nil {
		return fmt.Errorf("db.MarkReady: %w", err)
	}
	return nil
}

// MarkUnsupported transitions a GENERATING row owned by instanceID to UNSUPPORTED
// (terminal — ffmpeg cannot decode this format).
func (s *Store) MarkUnsupported(ctx context.Context, fileID string, version int, instanceID, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='UNSUPPORTED', last_error=$1,
             claimed_by=NULL, claimed_at=NULL, updated_at=now()
         WHERE file_id=$2 AND version=$3 AND status='GENERATING' AND claimed_by=$4`,
		errMsg, fileID, version, instanceID,
	)
	if err != nil {
		return fmt.Errorf("db.MarkUnsupported: %w", err)
	}
	return nil
}

// MarkFailed transitions a GENERATING row owned by instanceID to FAILED
// (terminal — max attempts exhausted).  Guards on claimed_by=$inst to match
// WSC's EbeanVideoPreviewRepository.markFailed semantics.
func (s *Store) MarkFailed(ctx context.Context, fileID string, version int, instanceID, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='FAILED', last_error=$1,
             claimed_by=NULL, claimed_at=NULL, updated_at=now()
         WHERE file_id=$2 AND version=$3 AND status='GENERATING' AND claimed_by=$4`,
		errMsg, fileID, version, instanceID,
	)
	if err != nil {
		return fmt.Errorf("db.MarkFailed: %w", err)
	}
	return nil
}

// Release returns a GENERATING row owned by instanceID back to PENDING without
// incrementing attempts (used for transient/storage/deadline errors).
// last_error is recorded for observability.
func (s *Store) Release(ctx context.Context, fileID string, version int, instanceID, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='PENDING', claimed_by=NULL, claimed_at=NULL,
             last_error=$1, updated_at=now()
         WHERE file_id=$2 AND version=$3 AND status='GENERATING' AND claimed_by=$4`,
		errMsg, fileID, version, instanceID,
	)
	if err != nil {
		return fmt.Errorf("db.Release: %w", err)
	}
	return nil
}

// ReenqueueReady moves a READY row back to PENDING for regeneration
// (e.g. when its preview blob is missing from storage). Clears preview_id
// so a stale blob id is never reused. Guarded on status='READY'.
func (s *Store) ReenqueueReady(ctx context.Context, fileID string, version int, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='PENDING', preview_id=NULL, claimed_by=NULL, claimed_at=NULL,
             last_error=$3, updated_at=now()
         WHERE file_id=$1 AND version=$2 AND status='READY'`,
		fileID, version, reason,
	)
	if err != nil {
		return fmt.Errorf("db.ReenqueueReady: %w", err)
	}
	return nil
}

// SetCodec persists the detected codec string for a GENERATING row owned by
// instanceID. Guarded on status='GENERATING' AND claimed_by=$inst so that a
// stolen/reclaimed row is never accidentally updated. No-op on wrong instance.
func (s *Store) SetCodec(ctx context.Context, fileID string, version int, instanceID, codec string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET codec=$4, updated_at=now()
         WHERE file_id=$1 AND version=$2 AND status='GENERATING' AND claimed_by=$3`,
		fileID, version, instanceID, codec,
	)
	if err != nil {
		return fmt.Errorf("db.SetCodec: %w", err)
	}
	return nil
}

// ReenqueueUnsupported moves an UNSUPPORTED row back to PENDING for re-generation
// (used when the codec list has been expanded to now include the row's codec).
// The codec column is intentionally preserved so the worker can skip re-probe.
// Guarded on status='UNSUPPORTED' — no-op on any other status.
func (s *Store) ReenqueueUnsupported(ctx context.Context, fileID string, version int, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='PENDING', claimed_by=NULL, claimed_at=NULL,
             last_error=$3, updated_at=now()
         WHERE file_id=$1 AND version=$2 AND status='UNSUPPORTED'`,
		fileID, version, reason,
	)
	if err != nil {
		return fmt.Errorf("db.ReenqueueUnsupported: %w", err)
	}
	return nil
}

// ReleaseWithAttempt releases a GENERATING row back to PENDING AND increments
// attempts. Guarded on status='GENERATING' AND claimed_by=$inst. Used for
// soft/transient failures so that repeated fast failures eventually hit
// maxAttempts (unlike plain Release which never increments).
func (s *Store) ReleaseWithAttempt(ctx context.Context, fileID string, version int, instanceID, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE video_preview
         SET status='PENDING', claimed_by=NULL, claimed_at=NULL,
             attempts=attempts+1, last_error=$1, updated_at=now()
         WHERE file_id=$2 AND version=$3 AND status='GENERATING' AND claimed_by=$4`,
		errMsg, fileID, version, instanceID,
	)
	if err != nil {
		return fmt.Errorf("db.ReleaseWithAttempt: %w", err)
	}
	return nil
}

// InsertReady inserts a READY row directly (used by the copy endpoint).
// If a row already exists (any status) the INSERT is silently skipped.
func (s *Store) InsertReady(ctx context.Context, fileID string, version int, ownerID, serviceType, previewID string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO video_preview
            (file_id, version, status, owner_id, service_type, preview_id,
             attempts, created_at, updated_at)
         VALUES ($1, $2, 'READY', $3, $4, $5, 0, now(), now())
         ON CONFLICT (file_id, version) DO NOTHING`,
		fileID, version, ownerID, serviceType, previewID,
	)
	if err != nil {
		return fmt.Errorf("db.InsertReady: %w", err)
	}
	return nil
}

// DeleteByFileId deletes the row for (fileID, version).
func (s *Store) DeleteByFileId(ctx context.Context, fileID string, version int) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM video_preview WHERE file_id=$1 AND version=$2`,
		fileID, version,
	)
	if err != nil {
		return fmt.Errorf("db.DeleteByFileId: %w", err)
	}
	return nil
}
