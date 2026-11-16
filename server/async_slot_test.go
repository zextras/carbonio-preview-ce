// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type eofReader struct {
	io.Reader
	sig chan<- struct{}
}

func (r eofReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		select {
		case r.sig <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (r eofReader) Close() error { return nil }

type eofStore struct {
	eof chan struct{}
}

func newEOFStore() *eofStore { return &eofStore{eof: make(chan struct{}, 256)} }

func (s *eofStore) RetrieveData(context.Context, string, int, string, string) (storage.Blob, error) {
	return []byte("blob"), nil
}

func (s *eofStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, int64, error) {
	return eofReader{Reader: strings.NewReader("video"), sig: s.eof}, int64(len("video")), nil
}

func (s *eofStore) StoreData(_ context.Context, nodeID string, _ int, _ string, _ string, _ []byte) (string, error) {
	return nodeID, nil
}

func (s *eofStore) Delete(context.Context, string, int, string, string) error { return nil }

func (s *eofStore) waitEOF(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-s.eof:
		case <-time.After(slotWatchdog):
			t.Fatalf("only %d of %d source downloads finished", i, n)
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(slotWatchdog)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		runtime.Gosched()
	}
}

func slotWorker(st *db.Store, store storage.Client, v int) *AsyncWorker {
	cfg := testCfg()
	cfg.AsyncMaxAttempts = 3
	return NewAsyncWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		Cache:    cache.New(0),
		DB:       st,
		AsyncSem: make(chan struct{}, v),
	})
}

func runAttempt(ctx context.Context, w *AsyncWorker, rows []db.Thumbnail) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.attempt(ctx, rows)
	}()
	return done
}

func waitDone(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(slotWatchdog):
		t.Fatal("attempt did not return")
	}
}

func waitReady(t *testing.T, st *db.Store, id string) {
	t.Helper()
	eventually(t, "row "+id+" READY", func() bool {
		r := findRow(st, id, 1)
		return r != nil && r.Status == db.StatusReady
	})
}

func TestAsyncSlot(t *testing.T) {
	st, dsn := startDBStoreWithDSN(t)
	ctx := context.Background()
	stubVideoPipeline(t)

	t.Run("download_is_not_gated_by_a_busy_extraction", func(t *testing.T) {
		var probes, inside atomic.Int32
		var overlap atomic.Bool
		inFFmpeg := make(chan struct{})
		releaseA := make(chan struct{})
		swap(t, &videoDetectCodecFromFileFunc, func(context.Context, string) (string, error) {
			if inside.Add(1) > 1 {
				overlap.Store(true)
			}
			if probes.Add(1) == 1 {
				close(inFFmpeg)
				<-releaseA
			}
			return "h264", nil
		})
		swap(t, &videoFirstFrameFromFileFunc, func(context.Context, string) ([]byte, error) {
			inside.Add(-1)
			return tinyJPEG(t), nil
		})
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		idA, idB := testUID(t, "A"), testUID(t, "B")
		rowsA, rowsB := claimedRows(t, st, w, idA, 0), claimedRows(t, st, w, idB, 0)

		doneA := runAttempt(ctx, w, rowsA)
		select {
		case <-inFFmpeg:
		case <-time.After(slotWatchdog):
			t.Fatal("first job never reached ffmpeg")
		}
		doneB := runAttempt(ctx, w, rowsB)
		store.waitEOF(t, 2)
		eventually(t, "second job waiting for the slot or probing", func() bool {
			return w.slotWaiters.Load() == 1 || probes.Load() > 1
		})
		if probes.Load() != 1 {
			t.Fatalf("probes=%d, want 1 (second job must wait for the slot after its download)", probes.Load())
		}

		close(releaseA)
		waitDone(t, doneA)
		waitDone(t, doneB)
		waitReady(t, st, idA)
		waitReady(t, st, idB)
		if overlap.Load() {
			t.Error("the second job's probe started while the first was still inside ffmpeg (V=1)")
		}
	})

	t.Run("ffmpeg_work_never_exceeds_V", func(t *testing.T) {
		const (
			v    = 2
			jobs = 5
		)
		var cur, peak atomic.Int32
		entered := make(chan struct{}, jobs)
		release := make(chan struct{})
		swap(t, &videoDetectCodecFromFileFunc, func(context.Context, string) (string, error) {
			c := cur.Add(1)
			for {
				p := peak.Load()
				if c <= p || peak.CompareAndSwap(p, c) {
					break
				}
			}
			entered <- struct{}{}
			<-release
			return "h264", nil
		})
		swap(t, &videoFirstFrameFromFileFunc, func(context.Context, string) ([]byte, error) {
			cur.Add(-1)
			return tinyJPEG(t), nil
		})
		store := newEOFStore()
		w := slotWorker(st, store, v)
		var done []<-chan struct{}
		var ids []string
		for i := 0; i < jobs; i++ {
			id := testUID(t, "j"+strconv.Itoa(i))
			ids = append(ids, id)
			done = append(done, runAttempt(ctx, w, claimedRows(t, st, w, id, 0)))
		}
		store.waitEOF(t, jobs)
		for i := 0; i < v; i++ {
			select {
			case <-entered:
			case <-time.After(slotWatchdog):
				t.Fatal("ffmpeg work did not start")
			}
		}
		if c := cur.Load(); c != v {
			t.Fatalf("%d ffmpeg jobs running, want %d", c, v)
		}

		close(release)
		for _, ch := range done {
			waitDone(t, ch)
		}
		for _, id := range ids {
			waitReady(t, st, id)
		}
		if p := peak.Load(); p != v {
			t.Errorf("peak concurrent ffmpeg jobs=%d, want %d", p, v)
		}
		if len(w.deps.AsyncSem) != 0 {
			t.Errorf("slots still held=%d", len(w.deps.AsyncSem))
		}
	})

	t.Run("office_conversion_holds_the_async_slot", func(t *testing.T) {
		inCollabora := make(chan struct{}, 2)
		release := make(chan struct{})
		var w *AsyncWorker
		calls := stubOffice(t, func() int { return len(w.deps.AsyncSem) }, func(collaboraCall) ([]byte, error) {
			inCollabora <- struct{}{}
			<-release
			return []byte("png"), nil
		})
		w = slotWorker(st, newEOFStore(), 1)
		idA, idB := testUID(t, "oA"), testUID(t, "oB")
		for _, id := range []string{idA, idB} {
			enqueueKind(t, st, id, 1, "office")
		}
		doneA := runAttempt(ctx, w, claimPair(t, st, w.instanceID, idA, 1))
		select {
		case <-inCollabora:
		case <-time.After(slotWatchdog):
			t.Fatal("first conversion never started")
		}
		doneB := runAttempt(ctx, w, claimPair(t, st, w.instanceID, idB, 1))
		eventually(t, "second job waiting for the slot", func() bool { return w.slotWaiters.Load() == 1 })
		if len(inCollabora) != 0 {
			t.Fatal("second conversion started while the first holds the only slot (V=1)")
		}
		if n := len(w.deps.AsyncSem); n != 1 {
			t.Fatalf("slots held during conversion=%d, want 1", n)
		}

		close(release)
		waitDone(t, doneA)
		waitDone(t, doneB)
		waitReady(t, st, idA)
		waitReady(t, st, idB)
		if len(*calls) != 2 || (*calls)[0].slots != 1 || (*calls)[1].slots != 1 {
			t.Errorf("each conversion must hold exactly one slot: %+v", *calls)
		}
		if len(w.deps.AsyncSem) != 0 {
			t.Errorf("slots still held=%d", len(w.deps.AsyncSem))
		}
	})

	t.Run("waiting_for_the_slot_keeps_heartbeating", func(t *testing.T) {
		swap(t, &heartbeatInterval, 5*time.Millisecond)
		stubSupportedCodec(t)
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		id := testUID(t, "hb")
		rows := claimedRows(t, st, w, id, 0)
		w.deps.AsyncSem <- struct{}{}

		done := runAttempt(ctx, w, rows)
		store.waitEOF(t, 1)
		seen := map[time.Time]struct{}{}
		eventually(t, "claimed_at refreshed three times while waiting for the slot", func() bool {
			r := findRow(st, id, 1)
			if r != nil && r.ClaimedAt != nil {
				seen[*r.ClaimedAt] = struct{}{}
			}
			return len(seen) >= 4
		})
		if r := findRow(st, id, 1); r.Status != db.StatusGenerating || r.Attempts != 0 {
			t.Fatalf("waiting row: status=%s attempts=%d, want GENERATING/0", r.Status, r.Attempts)
		}

		<-w.deps.AsyncSem
		waitDone(t, done)
		waitReady(t, st, id)
	})

	t.Run("ctx_cancelled_while_waiting_returns_row_to_pending", func(t *testing.T) {
		var probes atomic.Int32
		swap(t, &videoDetectCodecFromFileFunc, func(context.Context, string) (string, error) {
			probes.Add(1)
			return "h264", nil
		})
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		id := testUID(t, "cancel")
		rows := claimedRows(t, st, w, id, 1)
		w.deps.AsyncSem <- struct{}{}

		runCtx, cancel := context.WithCancel(ctx)
		done := runAttempt(runCtx, w, rows)
		store.waitEOF(t, 1)
		cancel()
		waitDone(t, done)

		after := findRow(st, id, 1)
		if after.Status != db.StatusPending || after.Attempts != rows[0].Attempts || after.ClaimedBy != nil {
			t.Fatalf("row after cancel: status=%s attempts=%d claimed_by=%v, want PENDING/%d/nil",
				after.Status, after.Attempts, after.ClaimedBy, rows[0].Attempts)
		}
		if probes.Load() != 0 {
			t.Error("ffmpeg ran without a slot")
		}
		if len(w.deps.AsyncSem) != 1 {
			t.Errorf("slots held=%d, want 1 (only the test's)", len(w.deps.AsyncSem))
		}
	})

	t.Run("live_jobs_across_ticks_never_exceed_maxInFlight", func(t *testing.T) {
		stubSupportedCodec(t)
		sqlExec(t, dsn, `DELETE FROM thumbnail WHERE status='PENDING'`)
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		w.deps.AsyncSem <- struct{}{}
		const rows = maxInFlight + 6
		for i := 0; i < rows; i++ {
			enqueuePair(t, st, testUID(t, "m"+strconv.Itoa(i)), 1)
		}
		tickCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		w.tick(tickCtx)
		w.tick(tickCtx)
		w.tick(tickCtx)
		if n := w.liveCount(); n != maxInFlight {
			t.Fatalf("live jobs=%d after three ticks, want %d", n, maxInFlight)
		}
		generating := 0
		for i := 0; i < rows; i++ {
			if r := findRow(st, testUID(t, "m"+strconv.Itoa(i)), 1); r.Status == db.StatusGenerating {
				generating++
			}
		}
		if generating != maxInFlight {
			t.Fatalf("GENERATING rows=%d, want %d (the rest stay PENDING)", generating, maxInFlight)
		}

		cancel()
		eventually(t, "all jobs returned their rows", func() bool { return w.liveCount() == 0 })
		pending, _ := st.FindPendingNewest(ctx, 2*rows)
		if len(pending) != 2*rows {
			t.Errorf("PENDING rows after cancel=%d, want %d", len(pending), 2*rows)
		}
	})

	t.Run("stale_rows_beyond_maxInFlight_go_back_to_pending", func(t *testing.T) {
		stubSupportedCodec(t)
		sqlExec(t, dsn, `DELETE FROM thumbnail WHERE status='PENDING'`)
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		w.deps.AsyncSem <- struct{}{}
		w.staleTTL = time.Minute
		const stale = 3
		for i := 0; i < stale; i++ {
			id := testUID(t, "s"+strconv.Itoa(i))
			enqueuePair(t, st, id, 1)
			claimPair(t, st, "dead-instance", id, 1)
			sqlExec(t, dsn, `UPDATE thumbnail SET claimed_at = now() - interval '1 hour' WHERE file_id=$1`, id)
		}
		for i := 0; i < maxInFlight-1; i++ {
			w.live[liveKey("busy", i)] = struct{}{}
		}
		tickCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		w.tick(tickCtx)

		generating, pending := 0, 0
		for i := 0; i < stale; i++ {
			r := findRow(st, testUID(t, "s"+strconv.Itoa(i)), 1)
			switch {
			case r.Status == db.StatusGenerating && r.ClaimedBy != nil && *r.ClaimedBy == w.instanceID:
				generating++
			case r.Status == db.StatusPending && r.ClaimedBy == nil:
				pending++
			}
		}
		if generating != 1 || pending != stale-1 {
			t.Fatalf("after tick: %d running, %d PENDING, want 1 and %d", generating, pending, stale-1)
		}
		if n := w.liveCount(); n != maxInFlight {
			t.Fatalf("live jobs=%d, want %d", n, maxInFlight)
		}
		cancel()
		<-w.deps.AsyncSem
		w.Wait()
	})
}

func TestThumbnailGet_AsyncAttemptFiresOnlyForPendingRows(t *testing.T) {
	stubSupportedCodec(t)
	store := newEOFStore()
	h := newHarness(t, harnessOpts{cfg: func(_ *testing.T, d Deps) Deps {
		d.Store = store
		d.AsyncSem = make(chan struct{}, 1)
		return d
	}})
	st, w := h.st, h.worker
	h.deps.AsyncSem <- struct{}{}
	idOther, idPending := newID(), newID()

	enqueuePair(t, st, idOther, testVersion)
	claimPair(t, st, "other-instance", idOther, testVersion)
	before := findRow(st, idOther, testVersion)
	if rec := h.get(thumbReq{id: idOther, version: testVersion, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("GENERATING row: status %d, want 202", rec.Code)
	}

	enqueuePair(t, st, idPending, testVersion)
	if rec := h.get(thumbReq{id: idPending, version: testVersion, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("PENDING row: status %d, want 202", rec.Code)
	}
	eventually(t, "PENDING row claimed by the immediate attempt", func() bool {
		r := findRow(st, idPending, testVersion)
		return r != nil && r.Status == db.StatusGenerating && r.ClaimedBy != nil && *r.ClaimedBy == w.instanceID
	})
	store.waitEOF(t, 1)

	if n := w.liveCount(); n != 1 {
		t.Errorf("live jobs=%d, want 1 (only the PENDING row's)", n)
	}
	after := findRow(st, idOther, testVersion)
	if after.Status != db.StatusGenerating || after.ClaimedBy == nil || *after.ClaimedBy != "other-instance" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("GENERATING row of another instance was touched: %+v", after)
	}

	h.cancel()
	w.Wait()
	<-h.deps.AsyncSem
}
