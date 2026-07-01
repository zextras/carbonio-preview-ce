// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// video_codec_test.go tests the codec-detection enhancement:
//   - resolve() UNSUPPORTED + codec ∈ list  → 202 + row PENDING
//   - resolve() UNSUPPORTED + codec ∉ list  → 415 + row stays UNSUPPORTED
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

	"github.com/zextras/carbonio-preview-ce/db"
	"github.com/zextras/carbonio-preview-ce/video"
)

// ---------------------------------------------------------------------------
// resolve() UNSUPPORTED branch
// ---------------------------------------------------------------------------

// TestResolve_Unsupported_CodecInList_Returns202 verifies that an UNSUPPORTED
// row whose stored codec is now in the supported list causes ReenqueueUnsupported
// (row → PENDING) and returns 202.
func TestResolve_Unsupported_CodecInList_Returns202(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "unsup_in_list")
	const version = 1
	const instID = "inst-codec"
	const codec = "h264" // in supported list

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := dbStore.SetCodec(ctx, fileID, version, instID, codec); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}
	if err := dbStore.MarkUnsupported(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")

	if res.httpStatus != http.StatusAccepted {
		t.Errorf("httpStatus: got %d, want 202 (codec in list → re-enqueue)", res.httpStatus)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != db.StatusPending {
		t.Errorf("status: got %q, want PENDING (re-enqueued)", row.Status)
	}
	// Codec must be preserved.
	if row.Codec == nil || *row.Codec != codec {
		t.Errorf("Codec: got %v, want %q (must be preserved after re-enqueue)", row.Codec, codec)
	}
}

// TestResolve_Unsupported_CodecNotInList_Returns415 verifies that an UNSUPPORTED
// row whose codec is NOT in the supported list still returns 415.
func TestResolve_Unsupported_CodecNotInList_Returns415(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "unsup_not_list")
	const version = 1
	const instID = "inst-av1"
	const codec = "av1_unsup_test_codec" // deliberately not in supported list

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := dbStore.SetCodec(ctx, fileID, version, instID, codec); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}
	if err := dbStore.MarkUnsupported(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")

	if res.httpStatus != http.StatusUnsupportedMediaType {
		t.Errorf("httpStatus: got %d, want 415 (codec not in list → terminal)", res.httpStatus)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != db.StatusUnsupported {
		t.Errorf("status: got %q, want UNSUPPORTED (unchanged)", row.Status)
	}
}

// TestResolve_Unsupported_NoCodec_Returns415 verifies that an UNSUPPORTED row
// without a stored codec (codec IS NULL) returns 415 (no re-enqueue possible).
func TestResolve_Unsupported_NoCodec_Returns415(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "unsup_no_codec")
	const version = 1
	const instID = "inst-nc"

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := dbStore.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	// Mark UNSUPPORTED WITHOUT setting a codec first.
	if err := dbStore.MarkUnsupported(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	deps := buildResolveDeps(dbStore, &fakeVideoStore{})
	res := resolve(ctx, deps, nil, fileID, version, "files", "owner1")

	if res.httpStatus != http.StatusUnsupportedMediaType {
		t.Errorf("httpStatus: got %d, want 415 (nil codec → terminal)", res.httpStatus)
	}
}

// ---------------------------------------------------------------------------
// attempt() codec-detection tests
// ---------------------------------------------------------------------------

// TestAttempt_CodecNotSupported_MarksUnsupported verifies that when the probe
// returns a codec not in the supported list, the row is marked UNSUPPORTED
// and attempts are NOT incremented.
func TestAttempt_CodecNotSupported_MarksUnsupported(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_codec_unsup")
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
	attempsBefore := row.Attempts

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
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
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_probe_fail")
	const version = 1

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "", errors.New("probe error: no video stream found")
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	// maxAttempts=3 → with attempts=0, attempts+1=1 < 3 → ReleaseWithAttempt
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
		t.Errorf("status: got %q, want PENDING (probe failure → ReleaseWithAttempt)", after.Status)
	}
	if after.Attempts != row.Attempts+1 {
		t.Errorf("Attempts: got %d, want %d (incremented)", after.Attempts, row.Attempts+1)
	}
}

// TestAttempt_ProbeFails_AtCap_MarksFailed verifies that probe failures at
// maxAttempts cap result in FAILED (not UNSUPPORTED).
func TestAttempt_ProbeFails_AtCap_MarksFailed(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_probe_cap")
	const version = 1

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "", errors.New("probe unreadable")
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	const maxAttempts = 2
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, maxAttempts)
	ok, err := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	// Bump attempts to maxAttempts-1 so the next attempt hits the cap.
	if rerr := dbStore.ReleaseWithAttempt(ctx, fileID, version, w.instanceID); rerr != nil {
		t.Fatalf("ReleaseWithAttempt (setup): %v", rerr)
	}
	ok2, err2 := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err2 != nil || !ok2 {
		t.Fatalf("re-Claim: err=%v ok=%v", err2, ok2)
	}

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Attempts != 1 {
		t.Fatalf("setup: expected attempts=1, got %d", row.Attempts)
	}

	w.attempt(ctx, *row)

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after attempt: %v", err)
	}
	if after.Status != db.StatusFailed {
		t.Errorf("status: got %q, want FAILED (probe cap reached)", after.Status)
	}
}

// TestAttempt_CodecInList_GenerateOK_MarksReady verifies the happy path with
// codec detection: probe returns a supported codec, extract succeeds → READY.
func TestAttempt_CodecInList_GenerateOK_MarksReady(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_codec_ok")
	const version = 1

	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		return "h264", nil // in supported list
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyPNG(t), nil
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

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
		t.Errorf("PreviewID: got %v, want non-empty", after.PreviewID)
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
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_corrupt")
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

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	// maxAttempts=3 → 0+1=1 < 3 → ReleaseWithAttempt
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
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	fileID := videoUID(t, "att_skip_probe")
	const version = 1
	const instID = "inst-sp"
	const codec = "h264"

	if err := dbStore.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	// Simulate: row was UNSUPPORTED with codec stored, then re-enqueued to PENDING.
	ok, err := dbStore.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim (setup): err=%v ok=%v", err, ok)
	}
	if err := dbStore.SetCodec(ctx, fileID, version, instID, codec); err != nil {
		t.Fatalf("SetCodec (setup): %v", err)
	}
	if err := dbStore.MarkUnsupported(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkUnsupported (setup): %v", err)
	}
	if err := dbStore.ReenqueueUnsupported(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueUnsupported (setup): %v", err)
	}

	// Now make a new worker and claim it.
	w := makeWorkerWithDB(dbStore, &fakeVideoStore{}, 3)
	ok2, err2 := dbStore.Claim(ctx, fileID, version, w.instanceID)
	if err2 != nil || !ok2 {
		t.Fatalf("Claim (worker): err=%v ok=%v", err2, ok2)
	}

	probeCallCount := 0
	origProbe := videoDetectCodecFromFileFunc
	videoDetectCodecFromFileFunc = func(_ context.Context, _ string) (string, error) {
		probeCallCount++
		return "h264", nil // shouldn't be reached
	}
	defer func() { videoDetectCodecFromFileFunc = origProbe }()

	origExtract := videoFirstFrameFromFileFunc
	videoFirstFrameFromFileFunc = func(_ context.Context, _ string) ([]byte, error) {
		return tinyPNG(t), nil
	}
	defer func() { videoFirstFrameFromFileFunc = origExtract }()

	row, err := dbStore.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	// Verify setup: codec must be stored on the row.
	if row.Codec == nil || *row.Codec != codec {
		t.Fatalf("setup: expected Codec=%q, got %v", codec, row.Codec)
	}

	w.attempt(ctx, *row)

	if probeCallCount != 0 {
		t.Errorf("probe called %d times, want 0 (stored codec must skip re-probe)", probeCallCount)
	}

	after, err := dbStore.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after: %v", err)
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
		{"av1", false},  // not in current list
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

