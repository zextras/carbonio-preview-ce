// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

const slotWatchdog = 5 * time.Second

func waitWaiters(t *testing.T, l *limits, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(slotWatchdog)
	for {
		l.mu.Lock()
		got := 0
		if f := l.inflight[key]; f != nil {
			got = f.waiters
		}
		l.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("flight has %d waiters, want %d", got, n)
		}
		runtime.Gosched()
	}
}

func assertNoFlights(t *testing.T, l *limits) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.inflight) != 0 {
		t.Errorf("%d flights still published", len(l.inflight))
	}
}

func (l *limits) flightFor(t *testing.T, key string) *flight {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.inflight[key]
	if f == nil {
		t.Fatalf("no flight for %s", key)
	}
	return f
}

func waitFlightDone(t *testing.T, f *flight) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(slotWatchdog):
		t.Fatal("shared work never ended")
	}
}

const webpMime = "image/webp"

func thumbnailTestKey(version int) string {
	return thumbKey(cacheTagThumbnail, "files", "", validUUID, version, "m", "")
}

func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}

type gatedStore struct {
	mu      sync.Mutex
	calls   map[int]int
	gates   map[int]chan struct{}
	fetched chan int
	ctxErrs chan error
	err     error
}

func newGatedStore() *gatedStore {
	return &gatedStore{
		calls:   map[int]int{},
		gates:   map[int]chan struct{}{},
		fetched: make(chan int, 64),
		ctxErrs: make(chan error, 64),
	}
}

func (s *gatedStore) gate(version int) chan struct{} {
	ch := make(chan struct{})
	s.gates[version] = ch
	return ch
}

func (s *gatedStore) callsFor(version int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[version]
}

func (s *gatedStore) RetrieveData(ctx context.Context, _ string, version int, _, _ string) (storage.Blob, error) {
	s.mu.Lock()
	s.calls[version]++
	gate := s.gates[version]
	s.mu.Unlock()
	s.fetched <- version
	if gate != nil {
		<-gate
		s.ctxErrs <- ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return []byte("src"), nil
}

func (s *gatedStore) RetrieveDataStreaming(context.Context, string, int, string, string) (io.ReadCloser, int64, error) {
	panic("not used")
}

func (s *gatedStore) StoreData(_ context.Context, nodeID string, _ int, _, _ string, _ []byte) (string, error) {
	return nodeID, nil
}

func (s *gatedStore) Delete(context.Context, string, int, string, string) error { return nil }

func waitFetch(t *testing.T, s *gatedStore, version int) {
	t.Helper()
	select {
	case got := <-s.fetched:
		if got != version {
			t.Fatalf("fetched version %d, want %d", got, version)
		}
	case <-time.After(slotWatchdog):
		t.Fatalf("fetch of version %d never started", version)
	}
}

func slotMux(lim *limits, store storage.Client, c *cache.Cache) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterOperations(newHumaAPI(mux), Deps{Cfg: testCfg(), Store: store, Cache: c, Limits: lim})
	return mux
}

func imageURL(version int) string {
	return "/thumbnail/" + validUUID + "/" + strconv.Itoa(version) + "/m?service_type=files&mime=" + webpMime
}

func pdfURL(version int) string {
	return "/thumbnail/" + validUUID + "/" + strconv.Itoa(version) + "/m?service_type=files&mime=application/pdf"
}

const (
	postImageURL = "/thumbnail/m?mime=" + webpMime
	postDocURL   = "/thumbnail/m?mime=" + docxMime
	postPDFURL   = "/thumbnail/m?mime=application/pdf"
	fakePDFBytes = "%PDF-fake"
)

func thumbnailHandler(deps Deps) func(context.Context, *apispec.ThumbnailGetInput) (*apispec.ThumbnailOutput, error) {
	return buildGetThumbnail(deps, nil)
}

func thumbnailInput(version int) *apispec.ThumbnailGetInput {
	return &apispec.ThumbnailGetInput{
		ID: validUUID, Version: version, Size: "m", ServiceType: "files", Mime: webpMime, Lang: "en-US",
	}
}

func serveAsync(h http.Handler, req *http.Request) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		ch <- rec
	}()
	return ch
}

func waitRec(t *testing.T, ch <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-ch:
		return rec
	case <-time.After(slotWatchdog):
		t.Fatal("request did not complete")
		return nil
	}
}

func stubRender(t *testing.T, f func(data []byte) ([]byte, error)) {
	t.Helper()
	swap(t, &imagePairFunc, func(data []byte) (render.Pair, error) {
		out, err := f(data)
		if err != nil {
			return render.Pair{}, err
		}
		r := render.Rendition{Body: out, ContentType: "image/jpeg"}
		return render.Pair{S: r, M: r}, nil
	})
}

func TestAcquire_CancelledWhileQueuedNeverHoldsSlot(t *testing.T) {
	for _, evenIfDone := range []bool{false, true} {
		l := newLimits(1)
		l.render <- struct{}{}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- l.acquire(ctx, l.render, evenIfDone) }()
		waitParked(t, l)
		cancel()

		if err := <-errCh; !errors.Is(err, context.Canceled) {
			t.Fatalf("evenIfDone=%v: acquire err=%v, want context.Canceled", evenIfDone, err)
		}
		if len(l.render) != 1 || l.parked.Load() != 0 {
			t.Fatalf("evenIfDone=%v: held=%d parked=%d, want 1/0", evenIfDone, len(l.render), l.parked.Load())
		}
	}
}

func TestAcquire_DoneCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caller := newLimits(1)
	if err := caller.acquire(ctx, caller.render, false); !errors.Is(err, context.Canceled) || len(caller.render) != 0 {
		t.Fatalf("caller with a done ctx: err=%v held=%d, want context.Canceled and no slot", err, len(caller.render))
	}

	flight := newLimits(1)
	if err := flight.acquire(ctx, flight.render, true); err != nil {
		t.Fatalf("flight step refused a free slot with a done ctx: %v", err)
	}
	if err := flight.acquire(ctx, flight.render, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("flight step with a busy slot and a done ctx: err=%v, want context.Canceled", err)
	}
	if len(flight.render) != 1 {
		t.Fatalf("slots held=%d, want 1", len(flight.render))
	}
}

func waitParked(t *testing.T, l *limits) {
	t.Helper()
	deadline := time.Now().Add(slotWatchdog)
	for l.parked.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nothing ever parked waiting for a slot")
		}
		runtime.Gosched()
	}
}

func TestNewLimits_RenderPoolIsTheOnlyPool(t *testing.T) {
	if got := cap(newLimits(3).render); got != 3 {
		t.Fatalf("render slots=%d, want 3", got)
	}
	u := newLimits(0)
	if u.render != nil {
		t.Error("newLimits(0).render must be unlimited (nil)")
	}
	if err := u.acquire(context.Background(), u.render, false); err != nil {
		t.Fatalf("unlimited acquire: %v", err)
	}
	u.render.release()
}

// Regression: a cache hit got 503 while the render slot was held across another request's slow fetch.
func TestRenderSlot_CachedHitNeedsNoSlot(t *testing.T) {
	lim := newLimits(1)
	store := newGatedStore()
	slow := store.gate(2)
	stubRender(t, func([]byte) ([]byte, error) { return []byte("rendered"), nil })
	mux := slotMux(lim, store, cache.New(1<<20))

	if rec := waitRec(t, serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(1), nil))); rec.Code != http.StatusOK {
		t.Fatalf("warm-up: got %d, want 200", rec.Code)
	}
	waitFetch(t, store, 1)

	slowDone := serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(2), nil))
	waitFetch(t, store, 2)
	lim.render <- struct{}{}

	rec := waitRec(t, serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(1), nil)))
	if rec.Code != http.StatusOK || rec.Body.String() != "rendered" {
		t.Fatalf("cached request: got %d %q, want 200 \"rendered\"", rec.Code, rec.Body.String())
	}
	if n := store.callsFor(1); n != 1 {
		t.Errorf("version 1 fetched %d times, want 1 (second request is a cache hit)", n)
	}

	<-lim.render
	close(slow)
	if rec := waitRec(t, slowDone); rec.Code != http.StatusOK {
		t.Errorf("slow request: got %d, want 200", rec.Code)
	}
}

func TestRenderSlot_QueuedRequestServedWhenSlotFrees(t *testing.T) {
	lim := newLimits(1)
	store := newGatedStore()
	var renders atomic.Int32
	stubRender(t, func([]byte) ([]byte, error) {
		renders.Add(1)
		return []byte("rendered"), nil
	})
	mux := slotMux(lim, store, nil)

	lim.render <- struct{}{}
	done := serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(1), nil))
	waitFetch(t, store, 1)
	waitParked(t, lim)

	select {
	case rec := <-done:
		t.Fatalf("request completed (%d) while the only render slot was held", rec.Code)
	default:
	}
	if renders.Load() != 0 {
		t.Fatal("render ran without a render slot")
	}

	<-lim.render
	if rec := waitRec(t, done); rec.Code != http.StatusOK {
		t.Fatalf("queued request: got %d, want 200", rec.Code)
	}
	if renders.Load() != 1 {
		t.Errorf("renders=%d, want 1", renders.Load())
	}
	if len(lim.render) != 0 {
		t.Errorf("render slots still held=%d", len(lim.render))
	}
}

func TestRenderSlot_PostCancelledWhileQueuedNeverHoldsSlot(t *testing.T) {
	lim := newLimits(1)
	converted := make(chan struct{})
	swap(t, &collaboraConvertFunc, func(context.Context, []byte, string, string, time.Duration) ([]byte, error) {
		close(converted)
		return []byte(fakePDFBytes), nil
	})
	var rasterized atomic.Int32
	stubRender(t, func([]byte) ([]byte, error) {
		rasterized.Add(1)
		return []byte("jpeg"), nil
	})
	mux := slotMux(lim, newGatedStore(), nil)

	lim.render <- struct{}{}
	body, ct := buildMultipart(t, "file", "doc.docx", []byte("doc"))
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, postDocURL, body).WithContext(ctx)
	req.Header.Set("Content-Type", ct)
	done := serveAsync(mux, req)

	select {
	case <-converted:
	case <-time.After(slotWatchdog):
		t.Fatal("conversion never ran")
	}
	waitParked(t, lim)
	cancel()

	rec := waitRec(t, done)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "server busy, retry") {
		t.Fatalf("got %d %q, want 503 server busy", rec.Code, rec.Body.String())
	}
	if rasterized.Load() != 0 {
		t.Error("render ran for a request whose caller left while queued")
	}
	if len(lim.render) != 1 {
		t.Errorf("render slots held=%d, want 1 (only the test's)", len(lim.render))
	}
	<-lim.render
}

func TestRenderSlot_PostWithDoneCtxTakesNoFreeSlot(t *testing.T) {
	lim := newLimits(1)
	var renders atomic.Int32
	stubRender(t, func([]byte) ([]byte, error) {
		renders.Add(1)
		return []byte("rendered"), nil
	})
	mux := slotMux(lim, newGatedStore(), nil)

	body, ct := buildMultipart(t, "file", "img.jpg", []byte("img"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, postImageURL, body).WithContext(ctx)
	req.Header.Set("Content-Type", ct)

	rec := waitRec(t, serveAsync(mux, req))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "server busy, retry") {
		t.Fatalf("got %d %q, want 503 server busy", rec.Code, rec.Body.String())
	}
	if renders.Load() != 0 || len(lim.render) != 0 {
		t.Errorf("done POST rendered=%d held=%d, want 0/0", renders.Load(), len(lim.render))
	}
}

func TestRenderSlot_PdfiumWaitEndedByCtxIsServerBusy(t *testing.T) {
	swap(t, &pdfPairFunc, func(context.Context, []byte) (render.Pair, error) {
		return render.Pair{}, fmt.Errorf("pdfium worker wait: %w", context.Canceled)
	})
	mux := slotMux(newLimits(1), newGatedStore(), nil)

	body, ct := buildMultipart(t, "file", "doc.pdf", []byte(fakePDFBytes))
	req := httptest.NewRequest(http.MethodPost, postPDFURL, body)
	req.Header.Set("Content-Type", ct)
	rec := waitRec(t, serveAsync(mux, req))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "server busy, retry") {
		t.Fatalf("got %d %q, want 503 server busy", rec.Code, rec.Body.String())
	}
}

func TestRenderSlot_HeldOnlyAroundRenderCall(t *testing.T) {
	lim := newLimits(2)
	var convertSaw, rasterSaw int
	swap(t, &collaboraConvertFunc, func(context.Context, []byte, string, string, time.Duration) ([]byte, error) {
		convertSaw = len(lim.render)
		return []byte(fakePDFBytes), nil
	})
	stubRender(t, func([]byte) ([]byte, error) {
		rasterSaw = len(lim.render)
		return []byte("jpeg"), nil
	})
	mux := slotMux(lim, newGatedStore(), nil)

	if rec := waitRec(t, serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(1), nil))); rec.Code != http.StatusOK {
		t.Fatalf("GET: got %d %s", rec.Code, rec.Body.String())
	}
	if rasterSaw != 1 {
		t.Errorf("GET render slots held during the render: %d, want 1", rasterSaw)
	}

	body, ct := buildMultipart(t, "file", "doc.docx", []byte("doc"))
	req := httptest.NewRequest(http.MethodPost, postDocURL, body)
	req.Header.Set("Content-Type", ct)
	if rec := waitRec(t, serveAsync(mux, req)); rec.Code != http.StatusOK {
		t.Fatalf("POST: got %d %s", rec.Code, rec.Body.String())
	}
	if convertSaw != 0 || rasterSaw != 1 {
		t.Errorf("POST render slots held: convert=%d raster=%d, want 0/1", convertSaw, rasterSaw)
	}
	if len(lim.render) != 0 {
		t.Errorf("render slots still held after both requests: %d", len(lim.render))
	}
}

func TestFlight_ConcurrentIdenticalRequestsFetchOnce(t *testing.T) {
	const n = 5
	store := newGatedStore()
	gate := store.gate(1)
	var renders atomic.Int32
	stubRender(t, func([]byte) ([]byte, error) {
		renders.Add(1)
		return []byte("rendered"), nil
	})
	lim := newLimits(1)
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Limits: lim})

	type result struct {
		out *apispec.ThumbnailOutput
		err error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			out, err := handler(context.Background(), thumbnailInput(1))
			results <- result{out, err}
		}()
		waitWaiters(t, lim, thumbnailTestKey(1), i+1)
	}
	close(gate)

	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil || thumbBody(r.out) != "rendered" {
			t.Fatalf("waiter %d: out=%v err=%v", i, r.out, r.err)
		}
	}
	if c := store.callsFor(1); c != 1 {
		t.Errorf("RetrieveData calls=%d, want 1", c)
	}
	if r := renders.Load(); r != 1 {
		t.Errorf("renders=%d, want 1", r)
	}
	assertNoFlights(t, lim)
}

func TestFlight_ErrorSharedByAllWaiters(t *testing.T) {
	store := newGatedStore()
	store.err = storage.ErrNotFound
	gate := store.gate(1)
	lim := newLimits(0)
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Limits: lim})

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := handler(context.Background(), thumbnailInput(1))
			errs <- err
		}()
		waitWaiters(t, lim, thumbnailTestKey(1), i+1)
	}
	close(gate)

	for i := 0; i < 2; i++ {
		if got := statusOf(t, <-errs); got != http.StatusNotFound {
			t.Errorf("waiter %d: status %d, want 404", i, got)
		}
	}
	if c := store.callsFor(1); c != 1 {
		t.Errorf("RetrieveData calls=%d, want 1", c)
	}
	assertNoFlights(t, lim)
}

func TestFlight_AllWaitersLeaveDuringFetch_WorkCompletesAndFillsCache(t *testing.T) {
	store := newGatedStore()
	gate := store.gate(1)
	stubRender(t, func([]byte) ([]byte, error) { return []byte("rendered"), nil })
	c := cache.New(1 << 20)
	lim := newLimits(1)
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Cache: c, Limits: lim})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := handler(ctx, thumbnailInput(1))
		errCh <- err
	}()
	waitFetch(t, store, 1)
	f := lim.flightFor(t, thumbnailTestKey(1))
	cancel()
	if got := statusOf(t, <-errCh); got != http.StatusServiceUnavailable {
		t.Fatalf("disconnected waiter: status %d, want 503", got)
	}

	close(gate)
	if err := <-store.ctxErrs; err != nil {
		t.Fatalf("fetch ctx was cancelled with the waiter: %v", err)
	}
	waitFlightDone(t, f)
	if e, ok := c.Get(thumbnailTestKey(1)); !ok || string(e.Body) != "rendered" {
		t.Fatalf("cache not filled by the abandoned in-progress work: %v %q", ok, e.Body)
	}
	if len(lim.render) != 0 {
		t.Errorf("render slots still held=%d", len(lim.render))
	}
	assertNoFlights(t, lim)

	out, err := handler(context.Background(), thumbnailInput(1))
	if err != nil || thumbBody(out) != "rendered" {
		t.Fatalf("follow-up: out=%v err=%v", out, err)
	}
	if n := store.callsFor(1); n != 1 {
		t.Errorf("RetrieveData calls=%d, want 1", n)
	}
}

func TestFlight_AllWaitersLeaveDuringRender_WorkCompletesAndFillsCache(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	stubRender(t, func([]byte) ([]byte, error) {
		close(entered)
		<-release
		return []byte("rendered"), nil
	})
	c := cache.New(1 << 20)
	lim := newLimits(1)
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: newGatedStore(), Cache: c, Limits: lim})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := handler(ctx, thumbnailInput(1))
		errCh <- err
	}()
	<-entered
	f := lim.flightFor(t, thumbnailTestKey(1))
	cancel()
	if got := statusOf(t, <-errCh); got != http.StatusServiceUnavailable {
		t.Fatalf("disconnected waiter: status %d, want 503", got)
	}

	close(release)
	waitFlightDone(t, f)
	if _, ok := c.Get(thumbnailTestKey(1)); !ok {
		t.Fatal("cache not filled by the abandoned in-progress render")
	}
	if len(lim.render) != 0 {
		t.Errorf("render slots still held=%d", len(lim.render))
	}
	assertNoFlights(t, lim)
}

func TestFlight_AllWaitersLeaveWhileQueuedForRender_NeverRenders(t *testing.T) {
	lim := newLimits(1)
	store := newGatedStore()
	var renders atomic.Int32
	stubRender(t, func([]byte) ([]byte, error) {
		renders.Add(1)
		return []byte("rendered"), nil
	})
	c := cache.New(1 << 20)
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Cache: c, Limits: lim})

	lim.render <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := handler(ctx, thumbnailInput(1))
		errCh <- err
	}()
	waitFetch(t, store, 1)
	f := lim.flightFor(t, thumbnailTestKey(1))
	cancel()
	if got := statusOf(t, <-errCh); got != http.StatusServiceUnavailable {
		t.Fatalf("disconnected waiter: status %d, want 503", got)
	}
	waitFlightDone(t, f)

	if renders.Load() != 0 {
		t.Error("render ran for abandoned work")
	}
	if len(lim.render) != 1 {
		t.Errorf("render slots held=%d, want 1 (only the test's)", len(lim.render))
	}
	<-lim.render
	if _, ok := c.Get(thumbnailTestKey(1)); ok {
		t.Error("abandoned work filled the cache")
	}
	assertNoFlights(t, lim)
}

func TestFlight_JoinerKeepsFlightAlive(t *testing.T) {
	lim := newLimits(1)
	store := newGatedStore()
	stubRender(t, func([]byte) ([]byte, error) { return []byte("rendered"), nil })
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Limits: lim})

	lim.render <- struct{}{}
	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := handler(ctxA, thumbnailInput(1))
		errA <- err
	}()
	waitWaiters(t, lim, thumbnailTestKey(1), 1)

	type result struct {
		out *apispec.ThumbnailOutput
		err error
	}
	resB := make(chan result, 1)
	go func() {
		out, err := handler(context.Background(), thumbnailInput(1))
		resB <- result{out, err}
	}()
	waitWaiters(t, lim, thumbnailTestKey(1), 2)

	cancelA()
	if got := statusOf(t, <-errA); got != http.StatusServiceUnavailable {
		t.Fatalf("leaving waiter: status %d, want 503", got)
	}
	<-lim.render

	select {
	case r := <-resB:
		if r.err != nil || thumbBody(r.out) != "rendered" {
			t.Fatalf("remaining waiter: out=%v err=%v", r.out, r.err)
		}
	case <-time.After(slotWatchdog):
		t.Fatal("remaining waiter never served: the flight was dropped while it still had a waiter")
	}
	if n := store.callsFor(1); n != 1 {
		t.Errorf("RetrieveData calls=%d, want 1", n)
	}
	assertNoFlights(t, lim)
}

type readSignal struct {
	r    io.Reader
	once sync.Once
	read chan struct{}
}

func (s *readSignal) Read(p []byte) (int, error) {
	s.once.Do(func() { close(s.read) })
	return s.r.Read(p)
}

func TestFlight_LateCallerAfterAbandonmentIsServed(t *testing.T) {
	lim := newLimits(1)
	store := newGatedStore()
	gate := store.gate(1)
	stubRender(t, func([]byte) ([]byte, error) { return []byte("rendered"), nil })
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Limits: lim})

	lim.render <- struct{}{}
	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := handler(ctxA, thumbnailInput(1))
		errA <- err
	}()
	waitFetch(t, store, 1)
	abandoned := lim.flightFor(t, thumbnailTestKey(1))
	cancelA()
	if got := statusOf(t, <-errA); got != http.StatusServiceUnavailable {
		t.Fatalf("leaving caller: status %d, want 503", got)
	}

	type result struct {
		out *apispec.ThumbnailOutput
		err error
	}
	resB := make(chan result, 1)
	go func() {
		out, err := handler(context.Background(), thumbnailInput(1))
		resB <- result{out, err}
	}()
	waitWaiters(t, lim, thumbnailTestKey(1), 1)

	close(gate)
	waitFlightDone(t, abandoned)
	<-lim.render

	select {
	case r := <-resB:
		if r.err != nil || thumbBody(r.out) != "rendered" {
			t.Fatalf("late caller: out=%v err=%v, want 200 (it must not join the abandoned flight)", r.out, r.err)
		}
	case <-time.After(slotWatchdog):
		t.Fatal("late caller never served")
	}
	assertNoFlights(t, lim)
}

func TestServe_DoneCallerStartsNoFlight(t *testing.T) {
	lim := newLimits(1)
	store := newGatedStore()
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: store, Limits: lim})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := handler(ctx, thumbnailInput(1))
	if got := statusOf(t, err); got != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", got)
	}
	if n := store.callsFor(1); n != 0 {
		t.Errorf("RetrieveData calls=%d, want 0", n)
	}
	assertNoFlights(t, lim)
}

func TestJoin_RechecksCacheUnderLock(t *testing.T) {
	lim := newLimits(1)
	c := cache.New(1 << 20)
	want := cache.Entry{Body: []byte("cached"), ContentType: "image/jpeg"}
	c.Put("k", want)
	f, got, cached := lim.join(context.Background(), c, "getThumbnail", "k", func(runner) (cache.Entry, error) {
		t.Error("work started for a cached key")
		return cache.Entry{}, nil
	})
	if !cached || f != nil || string(got.Body) != "cached" {
		t.Fatalf("join: cached=%v flight=%v body=%q, want the cached entry and no flight", cached, f, got.Body)
	}
	assertNoFlights(t, lim)
}

func TestFlight_PanicBecomes500AndReleasesSlots(t *testing.T) {
	stubRender(t, func([]byte) ([]byte, error) { panic("boom") })
	lim := newLimits(1)
	handler := thumbnailHandler(Deps{Cfg: testCfg(), Store: newGatedStore(), Limits: lim})
	_, err := handler(context.Background(), thumbnailInput(1))
	if got := statusOf(t, err); got != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", got)
	}
	if len(lim.render) != 0 {
		t.Errorf("render slots still held after panic=%d", len(lim.render))
	}
}

func waitParkedN(t *testing.T, l *limits, n int32) {
	t.Helper()
	deadline := time.Now().Add(slotWatchdog)
	for l.parked.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("parked=%d, want %d", l.parked.Load(), n)
		}
		runtime.Gosched()
	}
}

func waitSignals(t *testing.T, ch <-chan struct{}, n int, what string) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(slotWatchdog):
			t.Fatalf("only %d of %d %s", i, n, what)
		}
	}
}

func TestRenderSlot_StorageFetchesAreNotGated(t *testing.T) {
	const k = 6
	lim := newLimits(1)
	store := newGatedStore()
	gates := make([]chan struct{}, k)
	for i := range gates {
		gates[i] = store.gate(i + 1)
	}
	stubRender(t, func([]byte) ([]byte, error) { return []byte("rendered"), nil })
	mux := slotMux(lim, store, nil)

	lim.render <- struct{}{}
	done := make([]<-chan *httptest.ResponseRecorder, k)
	for i := range done {
		done[i] = serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(i+1), nil))
	}
	for i := 0; i < k; i++ {
		select {
		case <-store.fetched:
		case <-time.After(slotWatchdog):
			t.Fatalf("only %d of %d fetches started while the render slot was held", i, k)
		}
	}
	for _, g := range gates {
		close(g)
	}
	waitParkedN(t, lim, k)

	<-lim.render
	for i, ch := range done {
		if rec := waitRec(t, ch); rec.Code != http.StatusOK {
			t.Errorf("request %d: got %d, want 200", i, rec.Code)
		}
	}
}

func TestRenderSlot_RenderCallsNeverExceedN(t *testing.T) {
	const (
		n     = 2
		perOp = 4
	)
	lim := newLimits(n)
	var cur, peak atomic.Int32
	entered := make(chan struct{}, 2*perOp)
	release := make(chan struct{})
	enter := func() {
		c := cur.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		cur.Add(-1)
	}
	stubRender(t, func([]byte) ([]byte, error) { enter(); return []byte("rendered"), nil })
	swap(t, &pdfPairFunc, func(context.Context, []byte) (render.Pair, error) {
		enter()
		r := render.Rendition{Body: []byte("jpeg"), ContentType: "image/jpeg"}
		return render.Pair{S: r, M: r}, nil
	})
	mux := slotMux(lim, newGatedStore(), nil)

	var done []<-chan *httptest.ResponseRecorder
	for i := 1; i <= perOp; i++ {
		done = append(done, serveAsync(mux, httptest.NewRequest(http.MethodGet, imageURL(i), nil)))
		done = append(done, serveAsync(mux, httptest.NewRequest(http.MethodGet, pdfURL(perOp+i), nil)))
	}
	waitSignals(t, entered, n, "render calls entered")
	waitParkedN(t, lim, 2*perOp-n)
	if c := cur.Load(); c != n {
		t.Fatalf("%d render calls running, want %d", c, n)
	}

	close(release)
	for i, ch := range done {
		if rec := waitRec(t, ch); rec.Code != http.StatusOK {
			t.Errorf("request %d: got %d, want 200", i, rec.Code)
		}
	}
	if p := peak.Load(); p != n {
		t.Errorf("peak concurrent render calls=%d, want %d", p, n)
	}
}

func TestRenderSlot_UploadBodiesAreReadWithoutSlot(t *testing.T) {
	const k = 5
	lim := newLimits(1)
	stubRender(t, func([]byte) ([]byte, error) { return []byte("rendered"), nil })
	mux := slotMux(lim, newGatedStore(), nil)

	lim.render <- struct{}{}
	read := make(chan struct{}, k)
	done := make([]<-chan *httptest.ResponseRecorder, k)
	for i := range done {
		body, ct := buildMultipart(t, "file", "img.jpg", []byte("img"))
		sig := &readSignal{r: body, read: make(chan struct{})}
		go func() {
			<-sig.read
			read <- struct{}{}
		}()
		req := httptest.NewRequest(http.MethodPost, postImageURL, sig)
		req.Header.Set("Content-Type", ct)
		done[i] = serveAsync(mux, req)
	}
	waitSignals(t, read, k, "upload bodies read while the render slot was held")
	waitParkedN(t, lim, k)

	<-lim.render
	for i, ch := range done {
		if rec := waitRec(t, ch); rec.Code != http.StatusOK {
			t.Errorf("upload %d: got %d, want 200", i, rec.Code)
		}
	}
}

func TestRenderSlot_CollaboraConversionsAreNotGated(t *testing.T) {
	const k = 5
	lim := newLimits(1)
	inConvert := make(chan struct{}, k)
	release := make(chan struct{})
	swap(t, &collaboraConvertFunc, func(context.Context, []byte, string, string, time.Duration) ([]byte, error) {
		inConvert <- struct{}{}
		<-release
		return []byte(fakePDFBytes), nil
	})
	stubRender(t, func([]byte) ([]byte, error) { return []byte("jpeg"), nil })
	mux := slotMux(lim, newGatedStore(), nil)

	lim.render <- struct{}{}
	done := make([]<-chan *httptest.ResponseRecorder, k)
	for i := range done {
		body, ct := buildMultipart(t, "file", "doc.docx", []byte("doc"))
		req := httptest.NewRequest(http.MethodPost, postDocURL, body)
		req.Header.Set("Content-Type", ct)
		done[i] = serveAsync(mux, req)
	}
	waitSignals(t, inConvert, k, "conversions reached Collabora while the render slot was held")
	close(release)
	waitParkedN(t, lim, k)

	<-lim.render
	for i, ch := range done {
		if rec := waitRec(t, ch); rec.Code != http.StatusOK {
			t.Errorf("conversion %d: got %d, want 200", i, rec.Code)
		}
	}
}

func thumbBody(out *apispec.ThumbnailOutput) string {
	rec := httptest.NewRecorder()
	out.Body(humatest.NewContext(nil, httptest.NewRequest(http.MethodGet, "/", nil), rec))
	return rec.Body.String()
}
