// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"strings"

	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type fakeVideoStore struct {
	retrieveErr error
	storeErr    error
	blob        []byte
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

func (f *fakeVideoStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _ string, _ string) (io.ReadCloser, int64, error) {
	if f.retrieveErr != nil {
		return nil, 0, f.retrieveErr
	}
	b := f.blob
	if b == nil {
		b = []byte("fake-video-stream")
	}
	return io.NopCloser(strings.NewReader(string(b))), int64(len(b)), nil
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
