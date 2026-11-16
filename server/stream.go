// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

// idleReadCloser runs its timer only while a source Read is pending, so time spent in the client's Write never counts; when it fires the source context is cancelled.
type idleReadCloser struct {
	rc      io.ReadCloser
	timer   *time.Timer
	timeout time.Duration
	cancel  context.CancelFunc
}

func (r *idleReadCloser) Read(p []byte) (int, error) {
	r.timer.Reset(r.timeout)
	n, err := r.rc.Read(p)
	r.timer.Stop()
	return n, err
}

func (r *idleReadCloser) Close() error {
	r.timer.Stop()
	err := r.rc.Close()
	r.cancel()
	return err
}

// openSource streams a stored blob and abandons it when no byte moves for idle, the wait for the response headers included.
func openSource(ctx context.Context, store storage.Client, fileID string, version int, serviceType, ownerID string, idle time.Duration) (io.ReadCloser, int64, error) {
	srcCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(idle, cancel)
	rc, size, err := store.RetrieveDataStreaming(srcCtx, fileID, version, serviceType, ownerID)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, 0, err
	}
	timer.Stop()
	return &idleReadCloser{rc: rc, timer: timer, timeout: idle, cancel: cancel}, size, nil
}
