// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"testing"
)

// TestSetCodec_PersistsCodec verifies that SetCodec writes the codec field to a
// GENERATING row owned by the correct instance, and that Find returns it.
func TestSetCodec_PersistsCodec(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "setcodec")
	const version = 1
	const instID = "inst-setcodec"
	const codec = "h264"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	if err := store.SetCodec(ctx, fileID, version, instID, codec); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: err=%v row=%v", err, row)
	}
	if row.Codec == nil || *row.Codec != codec {
		t.Errorf("Codec: got %v, want %q", row.Codec, codec)
	}
	if row.Status != StatusGenerating {
		t.Errorf("Status: got %q, want GENERATING (SetCodec must not change status)", row.Status)
	}
}

// TestSetCodec_WrongInstanceNoop verifies that SetCodec from a non-owning instance
// is a no-op: the codec column stays NULL.
func TestSetCodec_WrongInstanceNoop(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "setcodec_noop")
	const version = 1
	const ownerInst = "inst-owner"
	const wrongInst = "inst-intruder"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, ownerInst)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	// Wrong instance should not set codec.
	if err := store.SetCodec(ctx, fileID, version, wrongInst, "vp9"); err != nil {
		t.Fatalf("SetCodec (wrong inst): unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Codec != nil {
		t.Errorf("Codec: got %v, want nil (wrong instance must be a no-op)", row.Codec)
	}
}

// TestReenqueueUnsupported_MovesToPendingAndPreservesCodec verifies that
// ReenqueueUnsupported transitions an UNSUPPORTED row to PENDING and preserves
// the codec column.
func TestReenqueueUnsupported_MovesToPendingAndPreservesCodec(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_unsup")
	const version = 1
	const instID = "inst-u"
	const codec = "vp9"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	// Set codec and mark unsupported.
	if err := store.SetCodec(ctx, fileID, version, instID, codec); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}
	if err := store.MarkUnsupported(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	// Verify UNSUPPORTED with codec preserved.
	before, err := store.Find(ctx, fileID, version)
	if err != nil || before == nil {
		t.Fatalf("Find before: %v", err)
	}
	if before.Status != StatusUnsupported {
		t.Fatalf("status before: got %q, want UNSUPPORTED", before.Status)
	}
	if before.Codec == nil || *before.Codec != codec {
		t.Fatalf("codec before: got %v, want %q", before.Codec, codec)
	}

	if err := store.ReenqueueUnsupported(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueUnsupported: %v", err)
	}

	after, err := store.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after: %v", err)
	}
	if after.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING", after.Status)
	}
	// Codec MUST be preserved.
	if after.Codec == nil || *after.Codec != codec {
		t.Errorf("Codec: got %v, want %q (codec must be preserved)", after.Codec, codec)
	}
	if after.ClaimedBy != nil {
		t.Errorf("ClaimedBy: got %v, want nil", after.ClaimedBy)
	}
	if after.ClaimedAt != nil {
		t.Errorf("ClaimedAt: got %v, want nil", after.ClaimedAt)
	}
}

// TestReenqueueUnsupported_NoopOnPending verifies that ReenqueueUnsupported
// on a PENDING row is a no-op (row stays PENDING).
func TestReenqueueUnsupported_NoopOnPending(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_unsup_pend")
	const version = 1

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	if err := store.ReenqueueUnsupported(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueUnsupported: unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusPending {
		t.Errorf("status: got %q, want PENDING (no-op)", row.Status)
	}
}

// TestReenqueueUnsupported_NoopOnFailed verifies that ReenqueueUnsupported
// on a FAILED row is a no-op (FAILED is terminal).
func TestReenqueueUnsupported_NoopOnFailed(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_unsup_fail")
	const version = 1
	const instID = "inst-fail"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}
	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}
	if err := store.MarkFailed(ctx, fileID, version, instID); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := store.ReenqueueUnsupported(ctx, fileID, version); err != nil {
		t.Fatalf("ReenqueueUnsupported: unexpected error: %v", err)
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil || row == nil {
		t.Fatalf("Find: %v", err)
	}
	if row.Status != StatusFailed {
		t.Errorf("status: got %q, want FAILED (no-op)", row.Status)
	}
}

// TestCodecRoundTrip verifies that codec round-trips through EnqueueIfAbsent,
// Claim, SetCodec, and Find.
func TestCodecRoundTrip(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "codec_roundtrip")
	const version = 1
	const instID = "inst-rt"

	if err := store.EnqueueIfAbsent(ctx, fileID, version, "owner1", "files"); err != nil {
		t.Fatalf("EnqueueIfAbsent: %v", err)
	}

	// codec is NULL initially.
	before, err := store.Find(ctx, fileID, version)
	if err != nil || before == nil {
		t.Fatalf("Find before: %v", err)
	}
	if before.Codec != nil {
		t.Errorf("Codec before SetCodec: got %v, want nil", before.Codec)
	}

	ok, err := store.Claim(ctx, fileID, version, instID)
	if err != nil || !ok {
		t.Fatalf("Claim: err=%v ok=%v", err, ok)
	}

	const want = "hevc"
	if err := store.SetCodec(ctx, fileID, version, instID, want); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}

	after, err := store.Find(ctx, fileID, version)
	if err != nil || after == nil {
		t.Fatalf("Find after: %v", err)
	}
	if after.Codec == nil || *after.Codec != want {
		t.Errorf("Codec after SetCodec: got %v, want %q", after.Codec, want)
	}
}
