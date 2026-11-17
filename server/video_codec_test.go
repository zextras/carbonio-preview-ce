// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// video_codec_test.go tests the codec-detection enhancement:
//   - GET /thumbnail UNSUPPORTED + codec ∈ list  → 202 + rows PENDING
//   - GET /thumbnail UNSUPPORTED + codec ∉ list  → 415 + rows stay UNSUPPORTED
//   - attempt() codec ∉ list                → UNSUPPORTED (codec stored, attempts unchanged)
//   - attempt() probe fails                 → ReleaseWithAttempt / cap → FAILED
//   - attempt() codec ∈ list + generate OK  → READY
//   - attempt() codec ∈ list + ErrExtractFailed → ReleaseWithAttempt / cap → FAILED

package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/video"
)

// ---------------------------------------------------------------------------
// GET /thumbnail UNSUPPORTED branch
// ---------------------------------------------------------------------------

func unsupportedPair(t *testing.T, h *harness, codec string) string {
	t.Helper()
	ctx := context.Background()
	const instID = "inst-codec"
	id := newID()
	seedPending(t, h.st, id, testVersion)
	claimOne(t, h.st, instID, id, testVersion)
	if codec != "" {
		if err := h.st.SetCodec(ctx, id, testVersion, instID, db.AllSizes, codec); err != nil {
			t.Fatalf("SetCodec: %v", err)
		}
	}
	if err := h.st.MarkUnsupported(ctx, id, testVersion, instID, db.AllSizes); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}
	return id
}

// TestResolve_Unsupported_CodecInList_Returns202 verifies that an UNSUPPORTED
// row whose stored codec is now in the supported list is re-enqueued
// (rows → PENDING) and the GET returns 202.
func TestResolve_Unsupported_CodecInList_Returns202(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	const codec = "h264" // in supported list
	id := unsupportedPair(t, h, codec)

	rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime})
	if rec.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want 202 (codec in list → re-enqueue)", rec.Code)
	}

	rows := h.pair(id, testVersion)
	r := rows[db.SizeM]
	if len(rows) != 1 || r == nil || r.Status != db.StatusPending {
		t.Fatalf("rows = %+v, want only the requested m row PENDING (re-enqueued)", rows)
	}
	if r.Codec == nil || *r.Codec != codec {
		t.Errorf("Codec: got %v, want %q (must be preserved after re-enqueue)", r.Codec, codec)
	}
}

// TestResolve_Unsupported_CodecNotInList_Returns415 verifies that an UNSUPPORTED
// row whose codec is NOT in the supported list still returns 415.
func TestResolve_Unsupported_CodecNotInList_Returns415(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := unsupportedPair(t, h, "av1_unsup_test_codec")

	rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status: got %d, want 415 (codec not in list → terminal)", rec.Code)
	}

	for size, r := range h.pair(id, testVersion) {
		if r.Status != db.StatusUnsupported {
			t.Errorf("size %s status: got %q, want UNSUPPORTED (unchanged)", size, r.Status)
		}
	}
}

// TestResolve_Unsupported_NoCodec_Returns415 verifies that an UNSUPPORTED row
// without a stored codec (codec IS NULL) returns 415 (no re-enqueue possible).
func TestResolve_Unsupported_NoCodec_Returns415(t *testing.T) {
	h := newHarness(t, harnessOpts{noWorker: true})
	id := unsupportedPair(t, h, "")

	rec := h.get(thumbReq{id: id, version: testVersion, mime: videoMime})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status: got %d, want 415 (nil codec → terminal)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// attempt() codec-detection tests
// ---------------------------------------------------------------------------

// TestAttempt_CodecNotSupported_MarksUnsupported verifies that when the probe
// returns a codec not in the supported list, the row is marked UNSUPPORTED
// and attempts are NOT incremented.
func TestAttempt_CodecNotSupported_MarksUnsupported(t *testing.T) {
	dbStore := startDBStore(t)
	ctx := context.Background()

	fileID := testUID(t, "att_codec_unsup")
	const version = 1

	// Stub probe to return an unsupported codec.
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "av1_not_in_list", nil
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	// Stub extract (should not be called for unsupported codec).
	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		t.Error("videoFirstFrameFromFileFunc must NOT be called when codec is unsupported")
		return nil, errors.New("unexpected call")
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	seedPending(t, dbStore, fileID, version)
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	row := claimOne(t, dbStore, w.instanceID, fileID, version)
	attempsBefore := row.Attempts

	w.attempt(ctx, row)

	after := findRow(dbStore, fileID, version)
	if after == nil {
		t.Fatal("Find after attempt: row missing")
	}
	if after.Status != db.StatusUnsupported {
		t.Errorf("status: got %q, want UNSUPPORTED", after.Status)
	}
	// Codec must have been stored.
	if after.Codec == nil || *after.Codec != "av1_not_in_list" {
		t.Errorf("Codec: got %v, want %q (must be stored even for unsupported)", after.Codec, "av1_not_in_list")
	}
	// Attempts must NOT be incremented (UNSUPPORTED is not a transient error).
	if after.Attempts != attempsBefore {
		t.Errorf("Attempts: got %d, want %d (UNSUPPORTED must not increment attempts)", after.Attempts, attempsBefore)
	}
}

// TestAttempt_ProbeFails_ReleasesWithAttempt verifies that a probe failure
// (cannot detect codec) is treated as a transient error: ReleaseWithAttempt.
func TestAttempt_ProbeFails_ReleasesWithAttempt(t *testing.T) {
	dbStore := startDBStore(t)
	ctx := context.Background()

	fileID := testUID(t, "att_probe_fail")
	const version = 1

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "", errors.New("probe error: no video stream found")
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	seedPending(t, dbStore, fileID, version)
	// maxAttempts=3 → with attempts=0, attempts+1=1 < 3 → ReleaseWithAttempt
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	row := claimOne(t, dbStore, w.instanceID, fileID, version)

	w.attempt(ctx, row)

	after := findRow(dbStore, fileID, version)
	if after == nil {
		t.Fatal("Find after attempt: row missing")
	}
	if after.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING (probe failure → ReleaseWithAttempt)", after.Status)
	}
	if after.Attempts != row.Attempts+1 {
		t.Errorf("Attempts: got %d, want %d (incremented)", after.Attempts, row.Attempts+1)
	}
}

// TestAttempt_ProbeFails_AtCap_MarksFailed verifies that probe failures at
// maxAttempts cap result in FAILED (not UNSUPPORTED).
func TestAttempt_ProbeFails_AtCap_MarksFailed(t *testing.T) {
	dbStore := startDBStore(t)
	ctx := context.Background()

	fileID := testUID(t, "att_probe_cap")
	const version = 1

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "", errors.New("probe unreadable")
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	seedPending(t, dbStore, fileID, version)
	const maxAttempts = 2
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, maxAttempts)
	row := claimOne(t, dbStore, w.instanceID, fileID, version)
	// Bump attempts to maxAttempts-1 so the next attempt hits the cap.
	if rerr := dbStore.ReleaseWithAttempt(ctx, fileID, version, w.instanceID, db.AllSizes); rerr != nil {
		t.Fatalf("ReleaseWithAttempt (setup): %v", rerr)
	}
	row = claimOne(t, dbStore, w.instanceID, fileID, version)

	if row.Attempts != 1 {
		t.Fatalf("setup: expected attempts=1, got %d", row.Attempts)
	}

	w.attempt(ctx, row)

	after := findRow(dbStore, fileID, version)
	if after == nil {
		t.Fatal("Find after attempt: row missing")
	}
	if after.Status != db.StatusFailed {
		t.Errorf("status: got %q, want FAILED (probe cap reached)", after.Status)
	}
}

// TestAttempt_CodecInList_GenerateOK_MarksReady verifies the happy path with
// codec detection: probe returns a supported codec, extract succeeds → READY.
func TestAttempt_CodecInList_GenerateOK_MarksReady(t *testing.T) {
	dbStore := startDBStore(t)
	ctx := context.Background()

	fileID := testUID(t, "att_codec_ok")
	const version = 1

	stubVideoPipeline(t)

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil // in supported list
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyJPEG(t), nil
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	seedPending(t, dbStore, fileID, version)
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	row := claimOne(t, dbStore, w.instanceID, fileID, version)

	w.attempt(ctx, row)

	after := findRow(dbStore, fileID, version)
	if after == nil {
		t.Fatal("Find after attempt: row missing")
	}
	if after.Status != db.StatusReady {
		t.Errorf("status: got %q, want READY", after.Status)
	}
	for size, r := range pairOf(t, dbStore, fileID, version) {
		if r.Status != db.StatusReady || r.BlobID == nil || *r.BlobID == "" {
			t.Errorf("size %s: got %+v, want READY with a non-empty BlobID", size, r)
		}
	}
	// Codec must be stored.
	if after.Codec == nil || *after.Codec != "h264" {
		t.Errorf("Codec: got %v, want %q", after.Codec, "h264")
	}
}

// TestAttempt_CodecInList_ErrExtractFailed_ReleasesWithAttempt verifies that
// ErrExtractFailed from the extract step (supported codec, corrupt file) is
// treated as a transient error → ReleaseWithAttempt (NOT UNSUPPORTED).
func TestAttempt_CodecInList_ErrExtractFailed_ReleasesWithAttempt(t *testing.T) {
	dbStore := startDBStore(t)
	ctx := context.Background()

	fileID := testUID(t, "att_corrupt")
	const version = 1

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return nil, video.ErrExtractFailed // corrupt despite supported codec
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	seedPending(t, dbStore, fileID, version)
	// maxAttempts=3 → 0+1=1 < 3 → ReleaseWithAttempt
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	row := claimOne(t, dbStore, w.instanceID, fileID, version)

	w.attempt(ctx, row)

	after := findRow(dbStore, fileID, version)
	if after == nil {
		t.Fatal("Find after attempt: row missing")
	}
	// Must be PENDING (released with attempt), NOT UNSUPPORTED.
	if after.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING (ErrExtractFailed on supported codec → ReleaseWithAttempt)", after.Status)
	}
	if after.Attempts != row.Attempts+1 {
		t.Errorf("Attempts: got %d, want %d", after.Attempts, row.Attempts+1)
	}
}

// TestAttempt_StoredCodecSkipsProbe verifies that a row already carrying a
// codec (re-enqueued UNSUPPORTED) does not re-probe. The probe stub panics if
// called so that any accidental call is caught as a test failure.
func TestAttempt_StoredCodecSkipsProbe(t *testing.T) {
	dbStore := startDBStore(t)
	ctx := context.Background()

	fileID := testUID(t, "att_skip_probe")
	const version = 1
	const instID = "inst-sp"
	const codec = "h264"

	seedPending(t, dbStore, fileID, version)

	// Simulate: row was UNSUPPORTED with codec stored, then re-enqueued to PENDING.
	claimOne(t, dbStore, instID, fileID, version)
	if err := dbStore.SetCodec(ctx, fileID, version, instID, db.AllSizes, codec); err != nil {
		t.Fatalf("SetCodec (setup): %v", err)
	}
	if err := dbStore.MarkUnsupported(ctx, fileID, version, instID, db.AllSizes); err != nil {
		t.Fatalf("MarkUnsupported (setup): %v", err)
	}
	if err := dbStore.ReenqueueUnsupported(ctx, fileID, version, db.SizeM); err != nil {
		t.Fatalf("ReenqueueUnsupported (setup): %v", err)
	}

	// Now make a new worker and claim it.
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	row := claimOne(t, dbStore, w.instanceID, fileID, version)

	stubVideoPipeline(t)

	probeCallCount := 0
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		probeCallCount++
		return "h264", nil // shouldn't be reached
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyJPEG(t), nil
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	// Verify setup: codec must be stored on the row.
	if row.Codec == nil || *row.Codec != codec {
		t.Fatalf("setup: expected Codec=%q, got %v", codec, row.Codec)
	}

	w.attempt(ctx, row)

	if probeCallCount != 0 {
		t.Errorf("probe called %d times, want 0 (stored codec must skip re-probe)", probeCallCount)
	}

	after := findRow(dbStore, fileID, version)
	if after == nil {
		t.Fatal("Find after: row missing")
	}
	if after.Status != db.StatusReady {
		t.Errorf("status: got %q, want READY", after.Status)
	}
}

// ---------------------------------------------------------------------------
// isSupportedVideoCodec unit tests (no DB needed)
// ---------------------------------------------------------------------------

func TestIsSupportedVideoCodec(t *testing.T) {
	cases := []struct {
		codec string
		want  bool
	}{
		{"h264", true},
		{"H264", true}, // case-insensitive
		{"HEVC", true},
		{"hevc", true},
		{"h265", true},
		{"vp8", true},
		{"vp9", true},
		{"mpeg4", true},
		{"mpeg2video", true},
		{"theora", true},
		{"av1", false}, // not in current list
		{"wmv3", false},
		{"", false},
		{"not_a_codec_xyz", false},
	}
	for _, tc := range cases {
		got := isSupportedVideoCodec(tc.codec)
		if got != tc.want {
			t.Errorf("isSupportedVideoCodec(%q) = %v, want %v", tc.codec, got, tc.want)
		}
	}
}
