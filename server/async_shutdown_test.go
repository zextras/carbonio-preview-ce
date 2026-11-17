// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type ctxReader struct{ ctx context.Context }

func (r ctxReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r ctxReader) Close() error { return nil }

type stageStore struct {
	*eofStore
	dlID          string
	hdrID         string
	dlStarted     chan struct{}
	hdrStarted    chan struct{}
	uploadStarted chan struct{}
	deleteCtxs    chan bool
}

func (s *stageStore) Delete(ctx context.Context, _ string, _ int, _, _ string) error {
	_, bounded := ctx.Deadline()
	s.deleteCtxs <- bounded && ctx.Err() == nil
	return nil
}

func (s *stageStore) RetrieveDataStreaming(ctx context.Context, id string, version int, st, owner string) (io.ReadCloser, int64, error) {
	if id == s.hdrID {
		close(s.hdrStarted)
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}
	if id == s.dlID {
		close(s.dlStarted)
		return ctxReader{ctx}, -1, nil
	}
	return s.eofStore.RetrieveDataStreaming(ctx, id, version, st, owner)
}

func (s *stageStore) StoreData(ctx context.Context, _ string, _ int, _, _ string, _ []byte) (string, error) {
	close(s.uploadStarted)
	<-ctx.Done()
	return "", ctx.Err()
}

func startJob(w *AsyncWorker, ctx context.Context, row db.Thumbnail) {
	key := liveKey(row.FileID, row.Version, row.Size)
	if w.reserve(key) != reserveOK {
		panic("reserve")
	}
	go func() {
		defer w.unreserve(key)
		w.attempt(ctx, row)
	}()
}

func waitWorker(t *testing.T, w *AsyncWorker) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		w.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(slotWatchdog):
		t.Fatal("worker jobs did not end after the context was cancelled")
	}
}

func TestAsyncShutdown_EveryStageReturnsRowToPending(t *testing.T) {
	st := startDBStore(t)
	bg := context.Background()
	var probes atomic.Int32
	inFFmpeg := make(chan struct{})
	stubVideoPipeline(t)
	swap(t, &videoDetectCodecFromFileFunc, func(ctx context.Context, _ string) (string, error) {
		if probes.Add(1) == 1 {
			return "h264", nil
		}
		close(inFFmpeg)
		<-ctx.Done()
		return "", ctx.Err()
	})
	swap(t, &videoFirstFrameFromFileFunc, func(context.Context, string) ([]byte, error) { return tinyJPEG(t), nil })

	idUpload, idFFmpeg, idSlot, idDownload, idHeaders := testUID(t, "up"), testUID(t, "ff"), testUID(t, "slot"), testUID(t, "dl"), testUID(t, "hdr")
	store := &stageStore{
		eofStore: newEOFStore(), dlID: idDownload, hdrID: idHeaders,
		dlStarted: make(chan struct{}), hdrStarted: make(chan struct{}), uploadStarted: make(chan struct{}),
		deleteCtxs: make(chan bool, 4),
	}
	w := slotWorker(st, store, 1)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	w.bind(ctx)

	startJob(w, ctx, claimedRow(t, st, w, idUpload, 0))
	select {
	case <-store.uploadStarted:
	case <-time.After(slotWatchdog):
		t.Fatal("upload stage never reached")
	}
	startJob(w, ctx, claimedRow(t, st, w, idFFmpeg, 0))
	select {
	case <-inFFmpeg:
	case <-time.After(slotWatchdog):
		t.Fatal("ffmpeg stage never reached")
	}
	startJob(w, ctx, claimedRow(t, st, w, idSlot, 0))
	store.waitEOF(t, 3)
	seedPending(t, st, idDownload, 1)
	w.runOne(idDownload, 1, db.SizeM)
	select {
	case <-store.dlStarted:
	case <-time.After(slotWatchdog):
		t.Fatal("download stage never reached")
	}

	startJob(w, ctx, claimedRow(t, st, w, idHeaders, 0))
	select {
	case <-store.hdrStarted:
	case <-time.After(slotWatchdog):
		t.Fatal("header-wait stage never reached")
	}

	cancel()
	waitWorker(t, w)

	select {
	case bounded := <-store.deleteCtxs:
		if !bounded {
			t.Error("orphan frame Delete must complete before Wait returns, on a bounded live context")
		}
	default:
		t.Error("orphan frame blob was not deleted before Wait returned")
	}

	for stage, id := range map[string]string{"upload": idUpload, "ffmpeg": idFFmpeg, "slot wait": idSlot, "download": idDownload, "headers": idHeaders} {
		r := findRow(st, id, 1)
		if r == nil {
			t.Errorf("%s: row missing", stage)
			continue
		}
		if r.Status != db.StatusPending || r.Attempts != 0 || r.ClaimedBy != nil {
			t.Errorf("%s: status=%s attempts=%d claimed_by=%v, want PENDING/0/nil", stage, r.Status, r.Attempts, r.ClaimedBy)
		}
	}
	if len(w.deps.AsyncSem) != 0 || w.liveCount() != 0 {
		t.Errorf("slots held=%d live=%d, want 0/0", len(w.deps.AsyncSem), w.liveCount())
	}
}

func TestAsyncShutdown_PoolClosedOnlyAfterJobsEnd(t *testing.T) {
	st, dsn := startDBStoreWithDSN(t)
	chk, err := db.New(context.Background(), dsn, db.PoolConfig{MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chk.Close)
	bg := context.Background()

	inFFmpeg := make(chan struct{})
	proceed := make(chan struct{})
	swap(t, &videoDetectCodecFromFileFunc, func(ctx context.Context, _ string) (string, error) {
		close(inFFmpeg)
		<-ctx.Done()
		<-proceed
		return "", ctx.Err()
	})
	cfg := testCfg()
	cfg.AsyncConcurrency = 1
	cfg.AsyncMaxAttempts = 3
	s := New(cfg, newEOFStore(), cache.New(0))
	_ = s.buildMux()
	s.dbGate.Set(st)
	ctx, cancel := context.WithCancel(bg)
	s.dbMu.Lock()
	s.dbCancel = cancel
	s.dbPool = st
	s.dbMu.Unlock()
	s.StartAsyncWorker(ctx)

	id := testUID(t, "job")
	startJob(s.worker, ctx, claimedRow(t, st, s.worker, id, 0))
	select {
	case <-inFFmpeg:
	case <-time.After(slotWatchdog):
		t.Fatal("job never reached ffmpeg")
	}

	stopped := make(chan struct{})
	go func() {
		s.stopAsync()
		close(stopped)
	}()
	eventually(t, "stopAsync waiting for the worker", func() bool {
		s.worker.mu.Lock()
		defer s.worker.mu.Unlock()
		return s.worker.closed
	})
	select {
	case <-stopped:
		t.Fatal("stopAsync returned while a job was still running")
	default:
	}
	close(proceed)
	select {
	case <-stopped:
	case <-time.After(slotWatchdog):
		t.Fatal("stopAsync never returned")
	}

	pair, err := chk.FindPair(bg, id, 1)
	if r := pair[db.SizeM]; err != nil || r == nil || r.Status != db.StatusPending || r.Attempts != 0 {
		t.Fatalf("rows after shutdown: %+v err=%v, want PENDING with 0 attempts (the pool was closed before the job released it)", pair, err)
	}
	if _, err := st.FindPair(bg, id, 1); err == nil {
		t.Error("the video DB pool was not closed")
	}
}

func TestAsyncShutdown_ClaimedAfterContextEndedIsReleased(t *testing.T) {
	st, dsn := startDBStoreWithDSN(t)
	bg := context.Background()
	stubSupportedCodec(t)

	t.Run("attempt_with_ended_context", func(t *testing.T) {
		for _, name := range []string{"storage_honours_ctx", "storage_ignores_ctx"} {
			id := testUID(t, name)
			var store storage.Client = newEOFStore()
			if name == "storage_honours_ctx" {
				store = &stageStore{eofStore: newEOFStore(), hdrID: id, hdrStarted: make(chan struct{}), deleteCtxs: make(chan bool, 1)}
			}
			w := slotWorker(st, store, 1)
			row := claimedRow(t, st, w, id, 0)
			ctx, cancel := context.WithCancel(bg)
			cancel()
			w.attempt(ctx, row)
			r := findRow(st, id, 1)
			if r == nil || r.Status != db.StatusPending || r.Attempts != 0 || r.ClaimedBy != nil {
				t.Errorf("%s: row after attempt on an ended context: %+v, want PENDING/0/nil", name, r)
			}
		}
	})

	t.Run("stale_row_reclaimed_after_context_ended", func(t *testing.T) {
		sqlExec(t, dsn, `DELETE FROM thumbnail WHERE status='PENDING'`)
		w := slotWorker(st, newEOFStore(), 1)
		w.staleTTL = time.Minute
		id := testUID(t, "stale")
		seedPending(t, st, id, 1)
		claimOne(t, st, "dead-instance", id, 1)
		sqlExec(t, dsn, `UPDATE thumbnail SET claimed_at = now() - interval '1 hour' WHERE file_id=$1`, id)
		ctx, cancel := context.WithCancel(bg)
		cancel()
		w.tick(ctx)
		w.Wait()
		r := findRow(st, id, 1)
		if r == nil || r.Status != db.StatusPending || r.ClaimedBy != nil {
			t.Fatalf("stale row after a tick on an ended context: %+v, want PENDING and unowned", r)
		}
	})
}
