// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v2/storage"
	"github.com/zextras/carbonio-preview-ce/v2/video"
)

// genFakeStore records StoreData calls and serves a canned video stream. A
// non-nil retrieveErr makes RetrieveDataStreaming fail.
type genFakeStore struct {
	stored      map[string][]byte
	video       []byte
	storedID    string
	retrieveErr error
}

func (f *genFakeStore) RetrieveData(context.Context, string, int, string, string) (storage.Blob, error) {
	return f.video, f.retrieveErr
}
func (f *genFakeStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, error) {
	if f.retrieveErr != nil {
		return nil, f.retrieveErr
	}
	return io.NopCloser(bytes.NewReader(f.video)), nil
}
func (f *genFakeStore) StoreData(_ context.Context, nodeID string, _ int, _ string, _ string, data []byte) (string, error) {
	if f.stored == nil {
		f.stored = map[string][]byte{}
	}
	f.stored[nodeID] = data
	f.storedID = nodeID
	return nodeID, nil
}
func (f *genFakeStore) Delete(_ context.Context, _ string, _ int, _ string, _ string) error {
	return nil
}

const (
	genSourceUUID = "11111111-1111-1111-1111-111111111111"
	genTargetUUID = "22222222-2222-2222-2222-222222222222"
)

// tinyPNG returns a minimal valid PNG so the JPEG re-encode step has real input.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestGenerate_StoresJPEGFrameUnderCallerUUID verifies the internal helper extracts
// a frame, JPEG-encodes it, and stores it under the caller-supplied UUID.
// The public POST /preview/video/generate/ endpoint has been removed (spec Q5);
// generation now runs only via the internal worker / resolve() fast-path.
func TestGenerate_StoresJPEGFrameUnderCallerUUID(t *testing.T) {
	pngBytes := tinyPNG(t)
	orig := videoFirstFrameFunc
	videoFirstFrameFunc = func(context.Context, io.Reader) ([]byte, error) { return pngBytes, nil }
	defer func() { videoFirstFrameFunc = orig }()

	fs := &genFakeStore{video: []byte("RIFFfakevideo")}

	id, err := generateFirstFrameJPEG(context.Background(), fs, "src-node", 0, "chats", "owner-1", genTargetUUID)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if id != genTargetUUID {
		t.Fatalf("expected echoed caller UUID %q, got %q", genTargetUUID, id)
	}
	stored, ok := fs.stored[genTargetUUID]
	if !ok {
		t.Fatalf("frame was not stored under %q", genTargetUUID)
	}
	// stored bytes must be JPEG (SOI marker 0xFFD8), not PNG.
	if len(stored) < 2 || stored[0] != 0xFF || stored[1] != 0xD8 {
		n := 2
		if len(stored) < n {
			n = len(stored)
		}
		t.Fatalf("expected JPEG bytes, got prefix %x", stored[:n])
	}
}

// TestGenerate_StorageNotFound verifies generateFirstFrameJPEG propagates
// storage.ErrNotFound from RetrieveDataStreaming.
func TestGenerate_StorageNotFound(t *testing.T) {
	fs := &genFakeStore{retrieveErr: storage.ErrNotFound}
	_, err := generateFirstFrameJPEG(context.Background(), fs, genSourceUUID, 0, "chats", "owner-1", genTargetUUID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestGenerate_ExtractFailed verifies generateFirstFrameJPEG propagates
// video.ErrExtractFailed (AV1/corrupt).
func TestGenerate_ExtractFailed(t *testing.T) {
	orig := videoFirstFrameFunc
	videoFirstFrameFunc = func(context.Context, io.Reader) ([]byte, error) { return nil, video.ErrExtractFailed }
	defer func() { videoFirstFrameFunc = orig }()

	fs := &genFakeStore{video: []byte("RIFFfakevideo")}
	_, err := generateFirstFrameJPEG(context.Background(), fs, genSourceUUID, 0, "chats", "owner-1", genTargetUUID)
	if err == nil {
		t.Fatal("expected ErrExtractFailed, got nil")
	}
}
