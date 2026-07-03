// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// video_api_test.go tests resolve() and the attempt() outcome mapping in
// VideoWorker, driven through a real DB (Postgres 16 container) + a fake
// storage.Client.
//
// Structure:
//   - fakeVideoStore — implements storage.Client for the video path.
//   - startVideoStore — spins up the Postgres testcontainer and returns a *db.Store.
//   - Tests for resolve() covering all 6 states.
//   - Tests for attempt() outcome mapping (success, ErrExtractFailed,
//     storage.ErrNotFound, transient reaching cap, transient below cap).
//
// Tests run in the `server` package (same package as the production code) so
// they can call resolve() and other unexported helpers directly without
// requiring HTTP wiring.

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v3/cache"
	"github.com/zextras/carbonio-preview-ce/v3/db"
	"github.com/zextras/carbonio-preview-ce/v3/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v3/storage"
	"github.com/zextras/carbonio-preview-ce/v3/video"
)

// ---------------------------------------------------------------------------
// fakeVideoStore — minimal storage.Client for video layer tests.
// ---------------------------------------------------------------------------

// fakeVideoStore drives storage responses for resolve() tests.
// retrieveErr controls the result of RetrieveData (the "verify blob" call in resolve).
// storeErr controls StoreData (used by attempt via generateFirstFrameJPEG).
type fakeVideoStore struct {
	retrieveErr error
	storeErr    error
	// blob returned when retrieveErr==nil
	blob []byte
}

func (f *fakeVideoStore) RetrieveData(_ context.Context, _ string, _ int, _ string, _ string) (storage.Blob, error) {
	if f.retrieveErr != nil {
		return nil, f.retrieveErr
	}
	if f.blob == nil {
		return []byte("fake-frame-bytes"), nil
	}
	return f.blob, nil
}

func (f *fakeVideoStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _ string, _ string) (io.ReadCloser, error) {
	if f.retrieveErr != nil {
		return nil, f.retrieveErr
	}
	b := f.blob
	if b == nil {
		b = []byte("fake-video-stream")
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (f *fakeVideoStore) StoreData(_ context.Context, nodeID string, _ int, _ string, _ string, _ []byte) (string, error) {
	if f.storeErr != nil {
		return "", f.storeErr
	}
	return nodeID, nil
}

func (f *fakeVideoStore) Delete(_ context.Context, _ string, _ int, _ string, _ string) error {
	return nil
}

// ---------------------------------------------------------------------------
// Postgres helper (mirrors db/store_test.go approach without importing it)
// ---------------------------------------------------------------------------

func startVideoPostgres(t *testing.T) *db.Store {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH — skipping video DB integration tests")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable (%v): %s", err, out)
	}

	const (
		pgUser  = "preview"
		pgPass  = "preview"
		pgDB    = "preview_video_test"
		pgPort  = "5432"
		imgName = "postgres:16-alpine"
	)

	out, err := exec.Command("docker", "run", "--rm", "-d",
		"-e", "POSTGRES_USER="+pgUser,
		"-e", "POSTGRES_PASSWORD="+pgPass,
		"-e", "POSTGRES_DB="+pgDB,
		"-P",
		imgName,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v — %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
	})

	portOut, err := exec.Command(
		"docker", "inspect",
		"--format", fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%s/tcp") 0).HostPort}}`, pgPort),
		containerID,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect port: %v — %s", err, portOut)
	}
	hostPort := strings.TrimSpace(string(portOut))

	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%s/%s?sslmode=disable",
		pgUser, pgPass, hostPort, pgDB)

	ctx := context.Background()
	var store *db.Store
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, serr := db.New(ctx, dsn, db.PoolConfig{MaxConns: 5})
		if serr == nil {
			store = st
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if store == nil {
		t.Fatalf("postgres not ready after 30s (DSN: %s)", dsn)
	}
	t.Cleanup(store.Close)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// videoUID generates a unique file_id for a sub-test.
func videoUID(t *testing.T, suffix string) string {
	t.Helper()
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, t.Name())
	s := safe + "_" + suffix
	if len(s) > 60 {
		s = s[len(s)-60:]
	}
	return s
}

// ---------------------------------------------------------------------------
// resolve() state machine tests
// ---------------------------------------------------------------------------

// buildResolveDeps creates a Deps wired for resolve() tests.
func buildResolveDeps(dbStore *db.Store, storeClient storage.Client) Deps {
	return Deps{
		Cfg:      testCfg(),
		Store:    storeClient,
		Cache:    cache.New(0),
		DB:       dbStore,
		VideoSem: nil,
	}
}

// TestResolve_NotFound_Returns202AndEnqueues verifies that a missing row causes
// EnqueueIfAbsent to create a PENDING row and resolve returns 202.
func TestResolve_NotFound_Returns202AndEnqueues(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "nf")
	const version = 1

	fakeStore := &fakeVideoStore{}
	deps := buildResolveDeps(dbStore, fakeStore)

	// No worker needed for the state check (fire-and-forget goroutine races with Find below).
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusAccepted {
		t.Errorf("httpStatus: got %d, want 202", res.httpStatus)
	}

	// Row must now exist as PENDING (or GENERATING if the background goroutine ran,
	// but nil worker means fireAsyncAttempt is a no-op).
	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if row == nil {
		t.Fatal("expected a row to be enqueued, got nil")
	}
	if row.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING", row.Status)
	}
}

// TestResolve_Pending_Returns202 verifies that a PENDING row returns 202.
func TestResolve_Pending_Returns202(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "pend")
	const version = 1

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusAccepted {
		t.Errorf("httpStatus: got %d, want 202", res.httpStatus)
	}
}

// TestResolve_Generating_Returns202 verifies that a GENERATING row returns 202.
func TestResolve_Generating_Returns202(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "gen")
	const version = 1

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	if _, err := dbStore.Claim(ctx, fileID, version, "inst-worker"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusAccepted {
		t.Errorf("httpStatus: got %d, want 202", res.httpStatus)
	}
}

// TestResolve_Ready_BlobPresent_Returns200 verifies that a READY row returns 200
// with the previewID set. resolve() is now DB-only — it does not touch storage.
func TestResolve_Ready_BlobPresent_Returns200(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "ready_ok")
	const version = 1
	const previewID = "preview-ok-1"

	if err := dbStore.InsertReady(ctx, fileID, version, "owner1", "files", previewID); err != nil {
		t.Fatalf("InsertReady: %v", err)
	}

	// resolve() no longer calls Store for READY rows; storage is irrelevant here.
	deps := buildResolveDeps(dbStore, &fakeVideoStore{})

	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusOK {
		t.Errorf("httpStatus: got %d, want 200", res.httpStatus)
	}
	if res.previewID != previewID {
		t.Errorf("previewID: got %q, want %q", res.previewID, previewID)
	}
}

// TestResolve_Ready_Returns200_DBOnly verifies that a READY row returns 200 with
// the previewID set. After the fix, resolve() is DB-only for READY rows — it does
// NOT check storage. Blob existence is verified lazily by the handler.
func TestResolve_Ready_Returns200_DBOnly(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "ready_dbonly")
	const version = 1
	const previewID = "preview-dbonly"

	if err := dbStore.InsertReady(ctx, fileID, version, "owner1", "files", previewID); err != nil {
		t.Fatalf("InsertReady: %v", err)
	}

	// Even with a store that would return ErrNotFound, resolve() must return 200
	// because it no longer touches storage for READY rows.
	fakeStore := &fakeVideoStore{retrieveErr: storage.ErrNotFound}
	deps := buildResolveDeps(dbStore, fakeStore)

	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusOK {
		t.Errorf("httpStatus: got %d, want 200 (DB-only, no storage call in resolve)", res.httpStatus)
	}
	if res.previewID != previewID {
		t.Errorf("previewID: got %q, want %q", res.previewID, previewID)
	}
}

// TestGetVideoPreview_BlobMissing_ReenqueuesAndReturns202 verifies the blob-gone
// path at the handler level: when resolve() returns 200 (READY) but the handler's
// own RetrieveData call returns ErrNotFound, the handler re-enqueues the row via
// ReenqueueReady and returns 202 (Accepted).
//
// This test covers the robustness path previously tested in resolve(): the
// behavior is identical — a READY row with a missing blob is re-enqueued — but the
// check now happens in the handler (single fetch point), not resolve().
func TestGetVideoPreview_BlobMissing_ReenqueuesAndReturns202(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	// Use a real UUID so validateUUID passes.
	const fileID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	const version = 1
	const previewID = "preview-blob-gone"

	if err := dbStore.InsertReady(ctx, fileID, version, "owner1", "files", previewID); err != nil {
		t.Fatalf("InsertReady: %v", err)
	}

	// Storage returns ErrNotFound — simulates orphaned READY row (blob manually deleted
	// or PowerStore glitch).
	fakeStore := &fakeVideoStore{retrieveErr: storage.ErrNotFound}
	deps := buildResolveDeps(dbStore, fakeStore)

	handler := buildGetVideoPreview(deps, nil)
	_, herr := handler(ctx, &apispec.VideoGetPreviewInput{
		ID:          fileID,
		Version:     version,
		Area:        "100x100",
		ServiceType: "files",
		Quality:     "medium",
		OutputFormat: "jpeg",
	})

	// Handler must return a 202 Accepted error.
	if herr == nil {
		t.Fatal("expected a huma error (202), got nil")
	}
	type statusGetter interface{ GetStatus() int }
	se, ok := herr.(statusGetter)
	if !ok {
		t.Fatalf("error %T does not implement GetStatus()", herr)
	}
	if se.GetStatus() != http.StatusAccepted {
		t.Errorf("HTTP status: got %d, want 202", se.GetStatus())
	}

	// Row must have been moved back to PENDING with preview_id cleared.
	row, ferr := dbStore.Find(ctx, fileID, version)
	if ferr != nil || row == nil {
		t.Fatalf("Find: err=%v row=%v", ferr, row)
	}
	if row.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING (re-enqueued by handler)", row.Status)
	}
	if row.PreviewID != nil {
		t.Errorf("preview_id: got %v, want nil (cleared by ReenqueueReady)", row.PreviewID)
	}
}

// TestResolve_Unsupported_Returns415 verifies that an UNSUPPORTED row returns 415.
func TestResolve_Unsupported_Returns415(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "unsup")
	const version = 1

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	if _, err := dbStore.Claim(ctx, fileID, version, "inst-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := dbStore.MarkUnsupported(ctx, fileID, version, "inst-1"); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusUnsupportedMediaType {
		t.Errorf("httpStatus: got %d, want 415", res.httpStatus)
	}
}

// TestResolve_Failed_Returns422 verifies that a FAILED row returns 422.
func TestResolve_Failed_Returns422(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "fail")
	const version = 1

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	if _, err := dbStore.Claim(ctx, fileID, version, "inst-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := dbStore.MarkFailed(ctx, fileID, version, "inst-1"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")
	if res.httpStatus != http.StatusUnprocessableEntity {
		t.Errorf("httpStatus: got %d, want 422", res.httpStatus)
	}
}

// ---------------------------------------------------------------------------
// attempt() outcome mapping tests
// ---------------------------------------------------------------------------

// makeWorkerWithDB builds a VideoWorker with a real DB store and the given
// storage fake. maxAttempts can be overridden via the config field.
func makeWorkerWithDB(dbStore *db.Store, storeClient storage.Client, maxAttempts int) *VideoWorker {
	cfg := testCfg()
	cfg.VideoMaxAttempts = maxAttempts
	return NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    storeClient,
		Cache:    cache.New(0),
		DB:       dbStore,
		VideoSem: nil,
	})
}

// TestAttempt_Success_MarksReady verifies that a successful generate
// (probe returns supported codec, extract returns valid PNG, StoreData succeeds) →
// MarkReady → row becomes READY with a non-empty preview_id.
func TestAttempt_Success_MarksReady(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_ok")
	const version = 1

	// Stub probe to return a supported codec.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	// Stub extract to return a minimal PNG.
	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyPNG(t), nil
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	// Enqueue + Claim so we have a GENERATING row.
	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
	}
	if after.Status != db.StatusReady {
		t.Errorf("status: got %q, want READY", after.Status)
	}
	if after.PreviewID == nil || *after.PreviewID == "" {
		t.Errorf("preview_id: got %v, want non-empty", after.PreviewID)
	}
}

// TestAttempt_ErrExtractFailed_NoCodecInRow_ReleasesWithAttempt verifies that
// when the probe step itself fails (not the extract step), the row is treated as
// a transient failure → ReleaseWithAttempt. This replaces the old
// ErrExtractFailed→UNSUPPORTED mapping: ErrExtractFailed now means corruption
// of a supported codec (→ retry), not an unsupported codec.
func TestAttempt_ErrExtractFailed_NoCodecInRow_ReleasesWithAttempt(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_probe_transient")
	const version = 1

	// Probe fails → transient error path.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "", video.ErrExtractFailed
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
	}
	// Probe failure is transient: row must be PENDING (released with attempt increment).
	if after.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING (probe failure → ReleaseWithAttempt)", after.Status)
	}
	if after.Attempts != row.Attempts+1 {
		t.Errorf("Attempts: got %d, want %d", after.Attempts, row.Attempts+1)
	}
}

// TestAttempt_StorageNotFound_MarksFailed verifies that storage.ErrNotFound from
// the source blob transitions the row to FAILED (terminal, no retry).
func TestAttempt_StorageNotFound_MarksFailed(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_notfound")
	const version = 1

	// RetrieveDataStreaming returns ErrNotFound → no extract attempted.
	fakeStore := &fakeVideoStore{retrieveErr: storage.ErrNotFound}

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	w := makeWorkerWithDB(dbStore, fakeStore, 3)
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
	}
	if after.Status != db.StatusFailed {
		t.Errorf("status: got %q, want FAILED (source blob gone → terminal)", after.Status)
	}
}

// TestAttempt_Transient_BelowCap_ReleasesWithAttempt verifies that a transient
// error during extract (not ErrNotFound) below maxAttempts calls
// ReleaseWithAttempt → PENDING + attempts incremented.
func TestAttempt_Transient_BelowCap_ReleasesWithAttempt(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_transient")
	const version = 1

	// Probe succeeds (supported codec), extract fails transiently.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("transient network hiccup")
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	// maxAttempts=3; row starts with attempts=0, so 0+1=1 < 3 → ReleaseWithAttempt.
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
	}
	if after.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING (released with attempt)", after.Status)
	}
	if after.Attempts != row.Attempts+1 {
		t.Errorf("attempts: got %d, want %d (incremented by ReleaseWithAttempt)", after.Attempts, row.Attempts+1)
	}
}

// TestAttempt_Transient_AtCap_MarksFailed verifies that a transient error at
// maxAttempts causes MarkFailed instead of ReleaseWithAttempt.
func TestAttempt_Transient_AtCap_MarksFailed(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_cap")
	const version = 1

	// Probe succeeds, extract fails transiently (hits cap → FAILED).
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("transient error")
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	// maxAttempts=2; manually bump attempts to 1 so that attempt+1=2>=2 triggers FAILED.
	const maxAttempts = 2
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, maxAttempts)
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Simulate attempts=1 already accumulated (e.g. from a prior ReclaimStale).
	// The worker reads row.Attempts from the DB at attempt() call time; bump it
	// via the public API: ReleaseWithAttempt to increment attempts to 1, then re-Claim.
	if rerr := dbStore.ReleaseWithAttempt(ctx, fileID, version, w.instanceID); rerr != nil {
		t.Fatalf("ReleaseWithAttempt (setup): %v", rerr)
	}
	ok2, err2 := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err2 != nil || !ok2 {
		t.Fatalf("re-Claim after setup: err=%v ok=%v", err2, ok2)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	// row.Attempts should now be 1 (= maxAttempts-1 for maxAttempts=2).
	if row.Attempts != 1 {
		t.Fatalf("setup: expected attempts=1, got %d", row.Attempts)
	}

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
	}
	if after.Status != db.StatusFailed {
		t.Errorf("status: got %q, want FAILED (cap %d reached)", after.Status, maxAttempts)
	}
}
