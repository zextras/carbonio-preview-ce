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

func (s *eofStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, error) {
	return eofReader{Reader: strings.NewReader("video"), sig: s.eof}, nil
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

func slotWorker(st *db.Store, store storage.Client, v int) *VideoWorker {
	cfg := testCfg()
	cfg.VideoMaxAttempts = 3
	return NewVideoWorker(Deps{
		Cfg:      cfg,
		Store:    store,
		Cache:    cache.New(0),
		DB:       st,
		VideoSem: make(chan struct{}, v),
	})
}

func runAttempt(ctx context.Context, w *VideoWorker, row db.VideoPreview) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.attempt(ctx, row)
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
		r, _ := st.Find(context.Background(), id, 1)
		return r != nil && r.Status == db.StatusReady
	})
}

func TestVideoSlot(t *testing.T) {
	st, dsn := startVideoPostgresWithDSN(t)
	ctx := context.Background()

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
		idA, idB := videoUID(t, "A"), videoUID(t, "B")
		rowA, rowB := claimedRow(t, st, w, idA, 0), claimedRow(t, st, w, idB, 0)

		doneA := runAttempt(ctx, w, rowA)
		select {
		case <-inFFmpeg:
		case <-time.After(slotWatchdog):
			t.Fatal("first job never reached ffmpeg")
		}
		doneB := runAttempt(ctx, w, rowB)
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
			id := videoUID(t, "j"+strconv.Itoa(i))
			ids = append(ids, id)
			done = append(done, runAttempt(ctx, w, claimedRow(t, st, w, id, 0)))
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
		if len(w.deps.VideoSem) != 0 {
			t.Errorf("slots still held=%d", len(w.deps.VideoSem))
		}
	})

	t.Run("waiting_for_the_slot_keeps_heartbeating", func(t *testing.T) {
		swap(t, &heartbeatInterval, 5*time.Millisecond)
		stubSupportedCodec(t)
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		id := videoUID(t, "hb")
		row := claimedRow(t, st, w, id, 0)
		w.deps.VideoSem <- struct{}{}

		done := runAttempt(ctx, w, row)
		store.waitEOF(t, 1)
		seen := map[time.Time]struct{}{}
		eventually(t, "claimed_at refreshed three times while waiting for the slot", func() bool {
			r, _ := st.Find(ctx, id, 1)
			if r != nil && r.ClaimedAt != nil {
				seen[*r.ClaimedAt] = struct{}{}
			}
			return len(seen) >= 4
		})
		if r, _ := st.Find(ctx, id, 1); r.Status != db.StatusGenerating || r.Attempts != 0 {
			t.Fatalf("waiting row: status=%s attempts=%d, want GENERATING/0", r.Status, r.Attempts)
		}

		<-w.deps.VideoSem
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
		id := videoUID(t, "cancel")
		row := claimedRow(t, st, w, id, 1)
		w.deps.VideoSem <- struct{}{}

		runCtx, cancel := context.WithCancel(ctx)
		done := runAttempt(runCtx, w, row)
		store.waitEOF(t, 1)
		cancel()
		waitDone(t, done)

		after, _ := st.Find(ctx, id, 1)
		if after.Status != db.StatusPending || after.Attempts != row.Attempts || after.ClaimedBy != nil {
			t.Fatalf("row after cancel: status=%s attempts=%d claimed_by=%v, want PENDING/%d/nil",
				after.Status, after.Attempts, after.ClaimedBy, row.Attempts)
		}
		if probes.Load() != 0 {
			t.Error("ffmpeg ran without a slot")
		}
		if len(w.deps.VideoSem) != 1 {
			t.Errorf("slots held=%d, want 1 (only the test's)", len(w.deps.VideoSem))
		}
	})

	t.Run("live_jobs_across_ticks_never_exceed_maxInFlight", func(t *testing.T) {
		stubSupportedCodec(t)
		sqlExec(t, dsn, `DELETE FROM video_preview WHERE status='PENDING'`)
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		w.deps.VideoSem <- struct{}{}
		const rows = maxInFlight + 6
		for i := 0; i < rows; i++ {
			if err := st.EnqueueIfAbsent(ctx, videoUID(t, "m"+strconv.Itoa(i)), 1, "owner1", "files"); err != nil {
				t.Fatal(err)
			}
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
			if r, _ := st.Find(ctx, videoUID(t, "m"+strconv.Itoa(i)), 1); r.Status == db.StatusGenerating {
				generating++
			}
		}
		if generating != maxInFlight {
			t.Fatalf("GENERATING rows=%d, want %d (the rest stay PENDING)", generating, maxInFlight)
		}

		cancel()
		eventually(t, "all jobs returned their rows", func() bool { return w.liveCount() == 0 })
		pending, _ := st.FindPendingNewest(ctx, rows)
		if len(pending) != rows {
			t.Errorf("PENDING rows after cancel=%d, want %d", len(pending), rows)
		}
	})

	t.Run("stale_rows_beyond_maxInFlight_go_back_to_pending", func(t *testing.T) {
		stubSupportedCodec(t)
		sqlExec(t, dsn, `DELETE FROM video_preview WHERE status='PENDING'`)
		store := newEOFStore()
		w := slotWorker(st, store, 1)
		w.deps.VideoSem <- struct{}{}
		w.staleTTL = time.Minute
		const stale = 3
		for i := 0; i < stale; i++ {
			id := videoUID(t, "s"+strconv.Itoa(i))
			if err := st.EnqueueIfAbsent(ctx, id, 1, "owner1", "files"); err != nil {
				t.Fatal(err)
			}
			if ok, err := st.Claim(ctx, id, 1, "dead-instance"); err != nil || !ok {
				t.Fatalf("Claim: %v %v", ok, err)
			}
			sqlExec(t, dsn, `UPDATE video_preview SET claimed_at = now() - interval '1 hour' WHERE file_id=$1`, id)
		}
		for i := 0; i < maxInFlight-1; i++ {
			w.live[liveKey("busy", i)] = struct{}{}
		}
		tickCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		w.tick(tickCtx)

		generating, pending := 0, 0
		for i := 0; i < stale; i++ {
			r, _ := st.Find(ctx, videoUID(t, "s"+strconv.Itoa(i)), 1)
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
		<-w.deps.VideoSem
		w.Wait()
	})
}

func TestResolve_AsyncAttemptFiresOnlyForPendingRows(t *testing.T) {
	st := startVideoPostgres(t)
	bg := context.Background()
	stubSupportedCodec(t)
	store := newEOFStore()
	w := slotWorker(st, store, 1)
	w.deps.VideoSem <- struct{}{}
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	w.bind(ctx)
	deps := w.deps
	idOther, idPending := videoUID(t, "other"), videoUID(t, "pending")

	if err := st.EnqueueIfAbsent(bg, idOther, 1, "owner1", "files"); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.Claim(bg, idOther, 1, "other-instance"); err != nil || !ok {
		t.Fatalf("Claim: %v %v", ok, err)
	}
	before, _ := st.Find(bg, idOther, 1)
	if res := resolve(bg, deps, w, idOther, 1, "files", "owner1"); res.httpStatus != http.StatusAccepted {
		t.Fatalf("GENERATING row: status %d, want 202", res.httpStatus)
	}

	if err := st.EnqueueIfAbsent(bg, idPending, 1, "owner1", "files"); err != nil {
		t.Fatal(err)
	}
	if res := resolve(bg, deps, w, idPending, 1, "files", "owner1"); res.httpStatus != http.StatusAccepted {
		t.Fatalf("PENDING row: status %d, want 202", res.httpStatus)
	}
	eventually(t, "PENDING row claimed by the immediate attempt", func() bool {
		r, _ := st.Find(bg, idPending, 1)
		return r != nil && r.Status == db.StatusGenerating && r.ClaimedBy != nil && *r.ClaimedBy == w.instanceID
	})
	store.waitEOF(t, 1)

	if n := w.liveCount(); n != 1 {
		t.Errorf("live jobs=%d, want 1 (only the PENDING row's)", n)
	}
	after, _ := st.Find(bg, idOther, 1)
	if after.Status != db.StatusGenerating || after.ClaimedBy == nil || *after.ClaimedBy != "other-instance" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("GENERATING row of another instance was touched: %+v", after)
	}

	cancel()
	<-w.deps.VideoSem
	w.Wait()
}
