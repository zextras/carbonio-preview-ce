// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"testing"
)

// TestSetCodec_PersistsCodec verifies that SetCodec writes the codec field to
// the GENERATING rows owned by the correct instance, and that FindPair returns it.
func TestSetCodec_PersistsCodec(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "setcodec")
	const version = 1
	const instID = "inst-setcodec"
	const codec = "h264"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)

	if err := store.SetCodec(ctx, fileID, version, instID, AllSizes, codec); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Codec == nil || *row.Codec != codec {
			t.Errorf("%s Codec: got %v, want %q", size, row.Codec, codec)
		}
		if row.Status != StatusGenerating {
			t.Errorf("%s Status: got %q, want GENERATING (SetCodec must not change status)", size, row.Status)
		}
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

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, ownerInst)

	if err := store.SetCodec(ctx, fileID, version, wrongInst, AllSizes, "vp9"); err != nil {
		t.Fatalf("SetCodec (wrong inst): unexpected error: %v", err)
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Codec != nil {
			t.Errorf("%s Codec: got %v, want nil (wrong instance must be a no-op)", size, row.Codec)
		}
	}
}

// TestReenqueueUnsupported_MovesToPendingAndPreservesCodec verifies that
// ReenqueueUnsupported transitions UNSUPPORTED rows to PENDING and preserves
// the codec column.
func TestReenqueueUnsupported_MovesToPendingAndPreservesCodec(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_unsup")
	const version = 1
	const instID = "inst-u"
	const codec = "vp9"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)
	if err := store.SetCodec(ctx, fileID, version, instID, AllSizes, codec); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}
	if err := store.MarkUnsupported(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("MarkUnsupported: %v", err)
	}

	for size, before := range pairOf(t, store, fileID, version) {
		if before.Status != StatusUnsupported {
			t.Fatalf("%s status before: got %q, want UNSUPPORTED", size, before.Status)
		}
		if before.Codec == nil || *before.Codec != codec {
			t.Fatalf("%s codec before: got %v, want %q", size, before.Codec, codec)
		}
	}

	for _, size := range AllSizes {
		if err := store.ReenqueueUnsupported(ctx, fileID, version, size); err != nil {
			t.Fatalf("ReenqueueUnsupported: %v", err)
		}
	}

	for size, after := range pairOf(t, store, fileID, version) {
		if after.Status != StatusPending {
			t.Errorf("%s status: got %q, want PENDING", size, after.Status)
		}
		if after.Codec == nil || *after.Codec != codec {
			t.Errorf("%s Codec: got %v, want %q (codec must be preserved)", size, after.Codec, codec)
		}
		if after.ClaimedBy != nil {
			t.Errorf("%s ClaimedBy: got %v, want nil", size, after.ClaimedBy)
		}
		if after.ClaimedAt != nil {
			t.Errorf("%s ClaimedAt: got %v, want nil", size, after.ClaimedAt)
		}
	}
}

// TestReenqueueUnsupported_NoopOnPending verifies that ReenqueueUnsupported
// on PENDING rows is a no-op (rows stay PENDING).
func TestReenqueueUnsupported_NoopOnPending(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_unsup_pend")
	const version = 1

	enqueuePair(t, store, fileID, version)

	for _, size := range AllSizes {
		if err := store.ReenqueueUnsupported(ctx, fileID, version, size); err != nil {
			t.Fatalf("ReenqueueUnsupported: unexpected error: %v", err)
		}
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusPending {
			t.Errorf("%s status: got %q, want PENDING (no-op)", size, row.Status)
		}
	}
}

// TestReenqueueUnsupported_NoopOnFailed verifies that ReenqueueUnsupported
// on FAILED rows is a no-op (FAILED is terminal).
func TestReenqueueUnsupported_NoopOnFailed(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "reenq_unsup_fail")
	const version = 1
	const instID = "inst-fail"

	enqueuePair(t, store, fileID, version)
	claimPair(t, store, fileID, version, instID)
	if err := store.MarkFailed(ctx, fileID, version, instID, AllSizes); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	for _, size := range AllSizes {
		if err := store.ReenqueueUnsupported(ctx, fileID, version, size); err != nil {
			t.Fatalf("ReenqueueUnsupported: unexpected error: %v", err)
		}
	}

	for size, row := range pairOf(t, store, fileID, version) {
		if row.Status != StatusFailed {
			t.Errorf("%s status: got %q, want FAILED (no-op)", size, row.Status)
		}
	}
}

// TestCodecRoundTrip verifies that codec round-trips through Enqueue,
// Claim, SetCodec, and FindPair.
func TestCodecRoundTrip(t *testing.T) {
	store := startPostgres(t)
	ctx := context.Background()

	fileID := uid(t, "codec_roundtrip")
	const version = 1
	const instID = "inst-rt"

	enqueuePair(t, store, fileID, version)

	for size, before := range pairOf(t, store, fileID, version) {
		if before.Codec != nil {
			t.Errorf("%s Codec before SetCodec: got %v, want nil", size, before.Codec)
		}
	}

	claimPair(t, store, fileID, version, instID)

	const want = "hevc"
	if err := store.SetCodec(ctx, fileID, version, instID, AllSizes, want); err != nil {
		t.Fatalf("SetCodec: %v", err)
	}

	for size, after := range pairOf(t, store, fileID, version) {
		if after.Codec == nil || *after.Codec != want {
			t.Errorf("%s Codec after SetCodec: got %v, want %q", size, after.Codec, want)
		}
	}
}
