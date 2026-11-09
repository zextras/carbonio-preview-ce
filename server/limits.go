// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
)

type slots chan struct{}

func newSlots(n int) slots {
	if n <= 0 {
		return nil
	}
	return make(slots, n)
}

func (s slots) release() {
	if s != nil {
		<-s
	}
}

type flight struct {
	done     chan struct{}
	entry    cache.Entry
	err      error
	waiters  int
	stopWait context.CancelFunc
}

type flightWork func(step runner) (cache.Entry, error)

type limits struct {
	render slots
	parked atomic.Int32

	mu       sync.Mutex
	inflight map[string]*flight
}

func newLimits(renderConcurrency int) *limits {
	return &limits{
		render:   newSlots(renderConcurrency),
		inflight: map[string]*flight{},
	}
}

func (d Deps) slotLimits() *limits {
	if d.Limits != nil {
		return d.Limits
	}
	return newLimits(0)
}

func errServerBusy() error {
	return huma.NewError(http.StatusServiceUnavailable, "server busy, retry")
}

// acquire with evenIfDone takes a free slot after ctx is done; either way it never waits once ctx is done.
func (l *limits) acquire(ctx context.Context, s slots, evenIfDone bool) error {
	if s == nil {
		return nil
	}
	if !evenIfDone && ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case s <- struct{}{}:
		return nil
	default:
	}
	l.parked.Add(1)
	defer l.parked.Add(-1)
	select {
	case s <- struct{}{}:
		if !evenIfDone && ctx.Err() != nil {
			<-s
			return ctx.Err()
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *limits) serve(ctx context.Context, c *cache.Cache, op, key string, work flightWork) (cache.Entry, error) {
	if e, ok := c.Get(key); ok {
		return e, nil
	}
	if ctx.Err() != nil {
		return cache.Entry{}, errServerBusy()
	}
	f, e, cached := l.join(ctx, c, op, key, work)
	if cached {
		return e, nil
	}
	start := time.Now()
	select {
	case <-f.done:
		return f.entry, f.err
	case <-ctx.Done():
		l.leave(key, f)
		slog.Debug("caller left while waiting for shared preview work",
			"operation", op, "waited_ms", time.Since(start).Milliseconds())
		return cache.Entry{}, errServerBusy()
	}
}

func (l *limits) join(ctx context.Context, c *cache.Cache, op, key string, work flightWork) (*flight, cache.Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.inflight[key]
	if !ok {
		if e, hit := c.Get(key); hit {
			return nil, e, true
		}
		workCtx := context.WithoutCancel(ctx)
		waitCtx, stopWait := context.WithCancel(workCtx)
		f = &flight{done: make(chan struct{}), stopWait: stopWait}
		l.inflight[key] = f
		go l.run(key, f, c, runner{l: l, wait: waitCtx, work: workCtx, flight: true, op: op}, work)
	}
	f.waiters++
	return f, cache.Entry{}, false
}

// leave unpublishes a flight together with stopping its waits, so nobody can join a flight that waits for nobody.
func (l *limits) leave(key string, f *flight) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f.waiters--
	if f.waiters == 0 {
		f.stopWait()
		if l.inflight[key] == f {
			delete(l.inflight, key)
		}
	}
}

func (l *limits) run(key string, f *flight, c *cache.Cache, step runner, fn flightWork) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("render work panicked", "panic", p, "stack", string(debug.Stack()))
			f.err = huma.NewError(http.StatusInternalServerError, "internal error")
		}
		l.mu.Lock()
		if l.inflight[key] == f {
			delete(l.inflight, key)
		}
		l.mu.Unlock()
		f.stopWait()
		close(f.done)
	}()
	f.entry, f.err = fn(step)
	if f.err == nil {
		c.Put(key, f.entry)
	}
}
