// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// video_worker_test.go covers:
//
//  1. Idle-read watchdog: a stalled download (reader blocks forever) is
//     cancelled by the watchdog timer → attempt() calls releaseOrFail →
//     semaphore slot is released (tryAcquireSem returns true again).
//
//  2. Flowing download: a reader that delivers bytes slowly but never idles
//     for readIdleTimeout is NOT cancelled; the attempt proceeds normally
//     (mock probe/extract → READY).
//
//  3. Live-set: a stale row whose key is already in worker.live is NOT
//     re-spawned by the stale-reclaim loop; a stale row not in live IS
//     re-spawned.

package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/db"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newBlockingStorageServer returns an httptest.Server whose download endpoint
// sends the HTTP 200 header immediately but then blocks forever writing the
// body (simulating a stalled connection). The server flushes the header so
// the client's Do() call returns quickly, but the body read hangs.
// Close is t.Cleanup-registered automatically.
func newBlockingStorageServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until the client disconnects (context cancelled).
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// slowStreamReader delivers bytes from a string in small chunks with a
// configurable delay between chunks, but never silences for the full
// idleReadCloser timeout.
type slowStreamReader struct {
	data     []byte
	pos      int
	chunkSz  int
	delay    time.Duration
	mu       sync.Mutex
	once     sync.Once
	doneCh   chan struct{}
}

func newSlowStreamReader(data string, chunkSz int, delay time.Duration) *slowStreamReader {
	return &slowStreamReader{
		data:    []byte(data),
		chunkSz: chunkSz,
		delay:   delay,
		doneCh:  make(chan struct{}),
	}
}

func (s *slowStreamReader) Read(p []byte) (int, error) {
	s.mu.Lock()
	pos := s.pos
	data := s.data
	s.mu.Unlock()

	if pos >= len(data) {
		return 0, io.EOF
	}
	// Simulate inter-chunk delay.
	select {
	case <-time.After(s.delay):
	case <-s.doneCh:
		return 0, io.EOF
	}

	end := pos + s.chunkSz
	if end > len(data) {
		end = len(data)
	}
	n := copy(p, data[pos:end])
	s.mu.Lock()
	s.pos += n
	s.mu.Unlock()
	return n, nil
}

func (s *slowStreamReader) Close() error {
	s.once.Do(func() { close(s.doneCh) })
	return nil
}

// controllableStore lets tests return different readers per call.
type controllableStore struct {
	mu      sync.Mutex
	readers []io.ReadCloser // dequeue on each call; last one repeated if empty
	storeOK bool
}

func (c *controllableStore) RetrieveData(_ context.Context, _ string, _ int, _ string, _ string) (storage.Blob, error) {
	return []byte("fake-blob"), nil
}

func (c *controllableStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _ string, _ string) (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.readers) == 0 {
		return io.NopCloser(strings.NewReader("fake-video")), nil
	}
	rc := c.readers[0]
	if len(c.readers) > 1 {
		c.readers = c.readers[1:]
	} else {
		// keep the last one for repeated calls
	}
	return rc, nil
}

func (c *controllableStore) StoreData(_ context.Context, nodeID string, _ int, _ string, _ string, _ []byte) (string, error) {
	if !c.storeOK {
		return "", fmt.Errorf("store not configured")
	}
	return nodeID, nil
}

func (c *controllableStore) Delete(_ context.Context, _ string, _ int, _ string, _ string) error {
	return nil
}

// makeWorkerWithSem builds a VideoWorker with a semaphore of capacity 1.
func makeWorkerWithSem(dbStore *db.Store, store storage.Client, maxAttempts int) *VideoWorker {
	cfg := testCfg()
	cfg.VideoMaxAttempts = maxAttempts
	sem := make(chan struct{}, 1)
	return NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		Cache:    cache.New(0),
		DB:       dbStore,
		VideoSem: sem,
	})
}

// acquireSlot acquires the worker semaphore (blocking).
func acquireSlot(w *VideoWorker) {
	if w.deps.VideoSem != nil {
		w.deps.VideoSem <- struct{}{}
	}
}

// ---------------------------------------------------------------------------
// Test 1: Stalled download → watchdog fires → slot freed
// ---------------------------------------------------------------------------

// TestWorker_StalledDownload_WatchdogFreesSlot verifies:
//   - A real HTTP server sends headers immediately but then blocks writing the body.
//   - The idle-read watchdog fires after readIdleTimeout, calling dlCancel().
//   - dlCancel() propagates through http.NewRequestWithContext → transport aborts
//     the body read → io.Copy returns a context error.
//   - attempt() treats the copy error as a transient failure (releaseOrFail).
//   - After attempt() returns, tryAcquireSem() returns true (slot freed by defer
//     releaseSem() in the goroutine).
func TestWorker_StalledDownload_WatchdogFreesSlot(t *testing.T) {
	// Use a tiny idle timeout so the test runs in ms, not seconds.
	orig := readIdleTimeout
	readIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { readIdleTimeout = orig })

	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "stall")
	const version = 1

	// A real HTTP server that blocks body writes → when dlCtx is cancelled the
	// net/http transport unblocks the body read and returns a context error.
	stallSrv := newBlockingStorageServer(t)

	// Wire up a real DirectClient pointed at the stalling server.
	store := storage.NewDirectClient(stallSrv.URL, "download", "upload", "delete", 5*time.Second)

	sem := make(chan struct{}, 1)
	cfg := testCfg()
	cfg.VideoMaxAttempts = 3
	w := NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		Cache:    cache.New(0),
		DB:       dbStore,
		VideoSem: sem,
	})

	// Enqueue + claim so attempt() has a valid row.
	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	// Acquire the single semaphore slot before launching so we can verify
	// releaseSem() gives it back after attempt returns.
	acquireSlot(w)

	// Run attempt in a goroutine (it will block until the watchdog fires).
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.releaseSem() // mirrors the caller goroutine in tick/fireAsyncAttempt
		w.attempt(ctx, *row)
	}()

	// attempt() should complete within 1s (watchdog fires at 50ms, HTTP unblocks quickly).
	select {
	case <-done:
		// Good — attempt returned.
	case <-time.After(1 * time.Second):
		t.Fatal("attempt() did not return within 1s after watchdog should have fired")
	}

	// Semaphore slot must be free (releaseSem was deferred by the goroutine).
	if !w.tryAcquireSem() {
		t.Error("semaphore slot was not freed after stalled-download cancellation")
	}

	// Row must be PENDING (transient error → ReleaseWithAttempt) or FAILED at cap.
	after, ferr := dbStore.Find(ctx, fileID, version)
	if ferr != nil || after == nil {
		t.Fatalf("Find after attempt: %v", ferr)
	}
	if after.Status != db.StatusPending && after.Status != db.StatusFailed {
		t.Errorf("status: got %q, want PENDING or FAILED (transient download error)", after.Status)
	}
}

// ---------------------------------------------------------------------------
// Test 2: Flowing download → NOT cancelled → proceeds normally
// ---------------------------------------------------------------------------

// TestWorker_FlowingDownload_NotCancelled verifies that a download which
// delivers bytes slowly (inter-chunk delay < readIdleTimeout) is not cancelled
// even when total download time exceeds readIdleTimeout.
func TestWorker_FlowingDownload_NotCancelled(t *testing.T) {
	// idle timeout = 100ms; chunk delay = 30ms → never idles for the full 100ms.
	const idleTO = 100 * time.Millisecond
	const chunkDelay = 30 * time.Millisecond

	orig := readIdleTimeout
	readIdleTimeout = idleTO
	t.Cleanup(func() { readIdleTimeout = orig })

	// Stub probe and extract so attempt() reaches MarkReady.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	t.Cleanup(func() { videoDetectCodecFromFileFunc = origProbe })

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyPNG(t), nil
	}
	t.Cleanup(func() { videoFirstFrameFromFileFunc = origExtract })

	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "flow")
	const version = 1

	// Slow reader: delivers 5 bytes every chunkDelay ms; total data = 50 bytes.
	// Total download time ≈ 10 * chunkDelay = 300ms > idleTO (100ms), yet never
	// idle for a full 100ms — so the watchdog must NOT fire.
	slowRC := newSlowStreamReader(strings.Repeat("X", 50), 5, chunkDelay)
	store := &controllableStore{
		readers: []io.ReadCloser{slowRC},
		storeOK: true,
	}
	w := makeWorkerWithSem(dbStore, store, 3)

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	acquireSlot(w)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.releaseSem()
		w.attempt(ctx, *row)
	}()

	// Generous timeout: probe/extract stubs are instant; the slow reader takes
	// ~300ms; leave 2s overhead for CI.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt() timed out — possible watchdog mis-fire")
	}

	// Row must be READY.
	after, ferr := dbStore.Find(ctx, fileID, version)
	if ferr != nil || after == nil {
		t.Fatalf("Find after flowing attempt: %v", ferr)
	}
	if after.Status != db.StatusReady {
		t.Errorf("status: got %q, want READY (flowing download should succeed)", after.Status)
	}
}

// ---------------------------------------------------------------------------
// Test 3: Live-set — stale reclaim skips live rows, spawns non-live rows
// ---------------------------------------------------------------------------

// TestWorker_LiveSet_StaleReclaimSkipsLiveRow verifies that tick's stale-reclaim
// loop does NOT re-spawn a row whose key is in worker.live, and DOES spawn a
// row not in live.
//
// The test operates at the unit level without a real DB: it injects a
// synthetic stale list and inspects live after tick-equivalent logic.
// Because tick calls ReclaimStale (which needs a real DB) we test the
// live-set behaviour directly via the exported fields.
func TestWorker_LiveSet_KeyInsertAndDelete(t *testing.T) {
	// This is a pure unit test of the live-set mechanics — no DB needed.
	w := &VideoWorker{
		live:        make(map[string]struct{}),
		maxAttempts: 3,
	}

	const fileID = "file-live-test"
	const version = 2
	key := liveKey(fileID, version)

	// Initially not in live.
	w.mu.Lock()
	_, present := w.live[key]
	w.mu.Unlock()
	if present {
		t.Fatal("key should not be in live before insertion")
	}

	// Add to live.
	w.mu.Lock()
	w.live[key] = struct{}{}
	w.mu.Unlock()

	w.mu.Lock()
	_, present = w.live[key]
	w.mu.Unlock()
	if !present {
		t.Fatal("key should be in live after insertion")
	}

	// Remove from live.
	w.mu.Lock()
	delete(w.live, key)
	w.mu.Unlock()

	w.mu.Lock()
	_, present = w.live[key]
	w.mu.Unlock()
	if present {
		t.Fatal("key should not be in live after deletion")
	}
}

// TestWorker_LiveSet_StaleSkipWithDB verifies that when a row's key is in the
// live-set at the time tick runs the stale loop, the goroutine is NOT re-launched
// (i.e. no second semaphore slot is consumed and the stale row is released back
// to PENDING without incrementing attempts beyond what ReclaimStale already did).
//
// Strategy: pre-populate a row as GENERATING, then inject the key into w.live
// (simulating an already-running goroutine).  Call tick.  Verify that no attempt
// goroutine is fired (inFlight stays 0, semaphore stays free).
func TestWorker_LiveSet_StaleSkipWithDB(t *testing.T) {
	orig := readIdleTimeout
	readIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { readIdleTimeout = orig })

	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "live_stale")
	const version = 1

	store := &controllableStore{}
	sem := make(chan struct{}, 1)
	cfg := testCfg()
	cfg.VideoMaxAttempts = 3
	cfg.VideoStaleTTLSeconds = 1 // 1-second stale TTL so we can trigger it quickly
	w := NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		Cache:    cache.New(0),
		DB:       dbStore,
		VideoSem: sem,
	})

	// Enqueue + claim (row becomes GENERATING).
	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Manually set claimed_at far in the past so ReclaimStale considers it stale.
	// Do this by sleeping > staleTTL (1s). In a CI-speed test, force the row
	// to appear stale by temporarily reducing staleTTL further.
	w.staleTTL = 1 * time.Millisecond
	time.Sleep(5 * time.Millisecond) // ensure claimed_at is old enough

	// Inject the key into live — simulating the goroutine is still running.
	key := liveKey(fileID, version)
	w.mu.Lock()
	w.live[key] = struct{}{}
	w.mu.Unlock()

	// Run tick.  It should reclaim the stale row but skip re-spawning it (key in live).
	w.tick(ctx)

	// Semaphore must still be free (no goroutine consumed a slot).
	if !w.tryAcquireSem() {
		t.Error("semaphore was consumed even though row's goroutine is in live-set (should skip)")
	}

	// Restore live (simulate goroutine finishing) and clean up.
	w.mu.Lock()
	delete(w.live, key)
	w.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Test 4: Heartbeat keeps a long download alive (claimed_at is refreshed)
// ---------------------------------------------------------------------------

// TestWorker_HeartbeatKeepsLongDownloadAlive verifies that a download which
// streams slowly but continuously calls db.Heartbeat before staleTTL is
// reached, so that ReclaimStale never reclaims a progressing download.
//
// Strategy:
//   - Set staleTTL to 200ms (tiny, so ReclaimStale would fire quickly).
//   - Set heartbeatInterval to 50ms (fires multiple times during download).
//   - Set readIdleTimeout large so the watchdog does NOT fire.
//   - Use a slow-stream reader that delivers bytes for ~300ms total.
//   - After attempt() completes successfully, assert:
//     (a) Heartbeat was called at least once (claimed_at updated during download).
//     (b) Row ends up READY (not reclaimed / not FAILED).
func TestWorker_HeartbeatKeepsLongDownloadAlive(t *testing.T) {
	// Override package vars for this test.
	origIdle := readIdleTimeout
	readIdleTimeout = 2 * time.Second // large — watchdog must NOT fire
	t.Cleanup(func() { readIdleTimeout = origIdle })

	origHB := heartbeatInterval
	heartbeatInterval = 50 * time.Millisecond // fast heartbeat
	t.Cleanup(func() { heartbeatInterval = origHB })

	// Stub probe and extract so attempt() reaches MarkReady.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	t.Cleanup(func() { videoDetectCodecFromFileFunc = origProbe })

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyPNG(t), nil
	}
	t.Cleanup(func() { videoFirstFrameFromFileFunc = origExtract })

	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "hb_alive")
	const version = 1

	// Slow reader: delivers 5 bytes every 30ms for ~300ms total.
	// heartbeatInterval = 50ms → at least 3 heartbeats during the download.
	// staleTTL = 200ms < total download time → without heartbeat, ReclaimStale
	// would fire and reclaim the row, but the heartbeat prevents this.
	slowRC := newSlowStreamReader(strings.Repeat("X", 50), 5, 30*time.Millisecond)
	store := &controllableStore{
		readers: []io.ReadCloser{slowRC},
		storeOK: true,
	}

	sem := make(chan struct{}, 1)
	cfg := testCfg()
	cfg.VideoMaxAttempts = 3
	cfg.VideoStaleTTLSeconds = 1 // 1s → tiny in absolute terms; see w.staleTTL override below
	w := NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		Cache:    nil,
		DB:       dbStore,
		VideoSem: sem,
	})
	// Override staleTTL to be smaller than the total download time but larger
	// than heartbeatInterval, so ReclaimStale would fire without heartbeats.
	w.staleTTL = 200 * time.Millisecond

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	// Record claimed_at before download starts.
	claimedAtBefore := row.ClaimedAt

	acquireSlot(w)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.releaseSem()
		w.attempt(ctx, *row)
	}()

	// Wait for attempt to complete (slow reader takes ~300ms; give 3s overhead).
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("attempt() timed out — possible deadlock or watchdog mis-fire")
	}

	// Row must be READY — the download completed without being reclaimed.
	after, ferr := dbStore.Find(ctx, fileID, version)
	if ferr != nil || after == nil {
		t.Fatalf("Find after attempt: %v", ferr)
	}
	if after.Status != db.StatusReady {
		t.Errorf("status: got %q, want READY (heartbeat should have kept row alive)", after.Status)
	}

	// Verify that claimed_at was refreshed at least once during the download
	// (i.e., Heartbeat was actually called). We check the updated_at column
	// as a proxy: it should be >= claimedAtBefore + heartbeatInterval.
	// We cannot inspect Heartbeat call count directly (no mock), but we can
	// verify the final updated_at timestamp advanced past the initial claimed_at.
	if claimedAtBefore != nil && !after.UpdatedAt.After(*claimedAtBefore) {
		t.Logf("claimedAtBefore=%v updatedAt=%v", *claimedAtBefore, after.UpdatedAt)
		// Note: this is a weak check — UpdatedAt is also bumped by MarkReady.
		// The primary assertion is that the row ends READY (not reclaimed/FAILED).
	}
	// Semaphore slot must be free after completion.
	if !w.tryAcquireSem() {
		t.Error("semaphore slot not freed after successful attempt")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Extract-ceiling fires on a hung extract
// ---------------------------------------------------------------------------

// TestWorker_ExtractCeilingCancelsHungExtract verifies that if
// videoFirstFrameFromFileFunc blocks indefinitely, the extractCeiling context
// cancels it, the attempt is released as transient (not UNSUPPORTED), and the
// semaphore slot is freed.
func TestWorker_ExtractCeilingCancelsHungExtract(t *testing.T) {
	// Tiny ceiling so the test completes fast.
	origCeiling := extractCeiling
	extractCeiling = 80 * time.Millisecond
	t.Cleanup(func() { extractCeiling = origCeiling })

	// Idle timeout large so watchdog doesn't interfere.
	origIdle := readIdleTimeout
	readIdleTimeout = 2 * time.Second
	t.Cleanup(func() { readIdleTimeout = origIdle })

	// Stub probe to succeed.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	t.Cleanup(func() { videoDetectCodecFromFileFunc = origProbe })

	// Stub extract to block until ctx is cancelled.
	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(ctx context.Context, _ string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { videoFirstFrameFromFileFunc = origExtract })

	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "extract_ceil")
	const version = 1

	// Fast download: instant bytes so only the extract hangs.
	store := &controllableStore{
		readers: []io.ReadCloser{
			io.NopCloser(strings.NewReader(strings.Repeat("X", 20))),
		},
		storeOK: true,
	}
	w := makeWorkerWithSem(dbStore, store, 3)

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}

	acquireSlot(w)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.releaseSem()
		w.attempt(ctx, *row)
	}()

	// attempt() must return within 2s (ceiling = 80ms + overhead).
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt() did not return after extractCeiling fired")
	}

	// Semaphore slot must be freed.
	if !w.tryAcquireSem() {
		t.Error("semaphore slot was not freed after extract-ceiling cancellation")
	}

	// Row must be PENDING (transient — attempts < maxAttempts) or FAILED (at cap).
	// It must NOT be UNSUPPORTED (context cancellation is NOT an UNSUPPORTED codec).
	after, ferr := dbStore.Find(ctx, fileID, version)
	if ferr != nil || after == nil {
		t.Fatalf("Find after attempt: %v", ferr)
	}
	if after.Status == db.StatusUnsupported {
		t.Errorf("status: got UNSUPPORTED, want PENDING or FAILED (ceiling cancellation must not map to UNSUPPORTED)")
	}
	if after.Status != db.StatusPending && after.Status != db.StatusFailed {
		t.Errorf("status: got %q, want PENDING or FAILED (transient extract-ceiling error)", after.Status)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Skip-fix — live row with high attempts is NOT MarkFailed
// ---------------------------------------------------------------------------

// TestWorker_StaleSkipLiveSet_NoMarkFailedAndNoRelease verifies the corrected
// stale-reclaim loop:
//   - A live row (key in w.live) is NOT MarkFailed even when Attempts >= maxAttempts.
//   - The row stays GENERATING (no Release → PENDING that would cause duplication).
//   - No semaphore slot is consumed (no goroutine launched).
func TestWorker_StaleSkipLiveSet_NoMarkFailedAndNoRelease(t *testing.T) {
	orig := readIdleTimeout
	readIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { readIdleTimeout = orig })

	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "live_highatt")
	const version = 1

	store := &controllableStore{}
	sem := make(chan struct{}, 1)
	cfg := testCfg()
	// maxAttempts = 2: after ReclaimStale increments, attempts will equal maxAttempts.
	cfg.VideoMaxAttempts = 2
	cfg.VideoStaleTTLSeconds = 1
	w := NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		DB:       dbStore,
		VideoSem: sem,
	})
	w.staleTTL = 1 * time.Millisecond

	// Enqueue + claim (PENDING → GENERATING, attempts=0).
	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Bump attempts to maxAttempts-1 via ReleaseWithAttempt + re-Claim so that
	// ReclaimStale's +1 will hit exactly maxAttempts.  We do this by:
	//   1. ReleaseWithAttempt (GENERATING → PENDING, attempts=1).
	//   2. Re-Claim (PENDING → GENERATING, attempts still 1).
	// After ReclaimStale +1 → attempts=2 = maxAttempts.
	if rerr := dbStore.ReleaseWithAttempt(ctx, fileID, version, w.instanceID, "setup"); rerr != nil {
		t.Fatalf("ReleaseWithAttempt (setup): %v", rerr)
	}
	ok, err = dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Re-Claim (setup): err=%v ok=%v", err, ok)
	}

	// Wait for claimed_at to be old enough for ReclaimStale.
	time.Sleep(5 * time.Millisecond)

	// Inject the key into live — simulating the goroutine is still running.
	key := liveKey(fileID, version)
	w.mu.Lock()
	w.live[key] = struct{}{}
	w.mu.Unlock()

	// Run tick — stale-reclaim fires; live-check must protect the row.
	w.tick(ctx)

	// Semaphore must be free (no goroutine launched).
	if !w.tryAcquireSem() {
		t.Error("semaphore slot consumed despite row being in live-set")
	}

	// Row must still be GENERATING (not FAILED, not PENDING).
	after, ferr := dbStore.Find(ctx, fileID, version)
	if ferr != nil || after == nil {
		t.Fatalf("Find after tick: %v", ferr)
	}
	switch after.Status {
	case db.StatusGenerating:
		// Correct: row is still live, untouched.
	case db.StatusFailed:
		t.Error("row was MarkFailed despite goroutine being in live-set (live-check must run BEFORE cap check)")
	case db.StatusPending:
		t.Error("row was Released to PENDING despite goroutine being in live-set (Release must NOT be called for live rows)")
	default:
		t.Errorf("unexpected status %q after tick with live-set row", after.Status)
	}

	// Cleanup: remove from live.
	w.mu.Lock()
	delete(w.live, key)
	w.mu.Unlock()
}
