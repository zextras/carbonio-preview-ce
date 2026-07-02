// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v2/cache"
	"github.com/zextras/carbonio-preview-ce/v2/config"
	"github.com/zextras/carbonio-preview-ce/v2/storage"
)

// slotTestStore returns canned bytes so the handler reaches the render step.
type slotTestStore struct{ body []byte }

func (s slotTestStore) RetrieveData(_ context.Context, _ string, _ int, _, _ string) (storage.Blob, error) {
	return s.body, nil
}

func (s slotTestStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _, _ string) (io.ReadCloser, error) {
	panic("not used (image path uses RetrieveData)")
}

func (s slotTestStore) StoreData(_ context.Context, nodeID string, _ int, _, _ string, _ []byte) (string, error) {
	return nodeID, nil
}

func (s slotTestStore) Delete(_ context.Context, _ string, _ int, _, _ string) error {
	return nil
}

// buildImageMuxWithSem builds an image-only huma mux backed by a render slot
// semaphore of the given capacity.
func buildImageMuxWithSem(cfg *config.Config, store storage.Client, sem chan struct{}) *http.ServeMux {
	mux := http.NewServeMux()
	api := newHumaAPI(mux)
	registerImageOps(api, Deps{Cfg: cfg, Store: store, Cache: cache.New(1 << 20), Sem: sem})
	return mux
}

// TestRenderSlot_ReleaseOnAllPaths_NoLeak is the regression test for the
// destructive render-slot leak/wedge found under burst load.
//
// It proves:
//  1. No double-acquire: with render_concurrency = N, exactly N requests run
//     concurrently (the pre-fix bug acquired the slot twice per request — once
//     in the middleware and once inside render.ImageThumbnail — which wedged the
//     limiter; with N=2 the bug would deadlock before reaching N in-flight).
//  2. Fast-fail on busy: an over-limit request whose context is already
//     cancelled returns 503 promptly WITHOUT acquiring a slot (it never blocks
//     until the client read-timeout, and it never consumes a slot).
//  3. No leak: once the in-flight requests complete, all slots are reclaimed and
//     a subsequent request succeeds. (Pre-fix, leaked slots accumulated until
//     every request 503/timed-out and only a restart recovered.)
func TestRenderSlot_ReleaseOnAllPaths_NoLeak(t *testing.T) {
	const n = 2
	cfg := testCfg()
	cfg.RenderConcurrency = n
	sem := make(chan struct{}, n)

	// Gate the render call so we can hold exactly N slots in flight.
	inRender := make(chan struct{}, n) // signalled when a render starts
	release := make(chan struct{})     // closed to let renders finish

	prev := imageThumbnailFunc
	// The stub mirrors render.ImageThumbnail's contract: if it is handed a
	// non-nil semaphore it acquires (and releases) a slot itself. The fixed
	// code passes nil here (the middleware is the single acquire point); if a
	// regression re-introduces the double-acquire (call site passing deps.Sem),
	// this stub will acquire a SECOND slot per request and the limiter wedges —
	// which the "only X/N reached render" assertion below catches.
	imageThumbnailFunc = func(sem chan struct{}, _ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		if sem != nil {
			sem <- struct{}{}
			defer func() { <-sem }()
		}
		inRender <- struct{}{}
		<-release
		return []byte("rendered"), nil
	}
	t.Cleanup(func() { imageThumbnailFunc = prev })

	mux := buildImageMuxWithSem(cfg, slotTestStore{body: []byte("img")}, sem)

	url := "/preview/image/" + validUUID + "/1/100x100/?service_type=files"

	// 1. Launch N concurrent requests; each must reach render and hold a slot.
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			codes[i] = rec.Code
		}(i)
	}

	// All N must enter render concurrently. If the limiter deadlocked (the
	// double-acquire bug), fewer than N would arrive and this times out.
	for i := 0; i < n; i++ {
		select {
		case <-inRender:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d/%d requests reached render — limiter wedged (double-acquire?)", i, n)
		}
	}

	// 2. Over-limit request with an already-cancelled context must fail fast
	//    with 503 and NOT acquire a slot. It must return promptly (well under
	//    the 2s busy-wait, since the cancelled ctx short-circuits immediately).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	busyRec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(busyRec, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("over-limit cancelled request did not fail fast — it blocked on acquire")
	}
	if busyRec.Code != http.StatusServiceUnavailable {
		t.Errorf("busy request: got %d, want 503", busyRec.Code)
	}

	// The busy request must NOT have consumed a slot: the channel should still
	// hold exactly N tokens (the N in-flight renders), so a non-blocking send
	// must fail.
	select {
	case sem <- struct{}{}:
		<-sem // give it back
		t.Fatal("a slot was free while N renders are in flight — busy request leaked/over-released a slot")
	default:
		// expected: full
	}

	// 3. Release the in-flight renders; all must complete 200 and free slots.
	close(release)
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("in-flight request %d: got %d, want 200", i, c)
		}
	}

	// After completion every slot must be reclaimed (channel fully drained).
	if len(sem) != 0 {
		t.Fatalf("after completion %d slots still held — leak", len(sem))
	}

	// A fresh request must succeed (proves the service is not wedged).
	freshPrev := imageThumbnailFunc
	imageThumbnailFunc = func(_ chan struct{}, _ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return []byte("rendered"), nil
	}
	t.Cleanup(func() { imageThumbnailFunc = freshPrev })

	freshRec := httptest.NewRecorder()
	// distinct URL to avoid the cache hit from the earlier successful renders
	freshURL := "/preview/image/" + validUUID + "/2/100x100/?service_type=files"
	mux.ServeHTTP(freshRec, httptest.NewRequest(http.MethodGet, freshURL, nil))
	if freshRec.Code != http.StatusOK {
		t.Errorf("post-recovery request: got %d, want 200 (service wedged?)", freshRec.Code)
	}
}
