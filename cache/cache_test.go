// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"strconv"
	"sync"
	"testing"
)

// withClock swaps the package clock for the duration of a test and restores it.
func withClock(t *testing.T, start int64) func(addSeconds float64) {
	t.Helper()
	prev := nowNano
	cur := start
	nowNano = func() int64 { return cur }
	t.Cleanup(func() { nowNano = prev })
	return func(addSeconds float64) { cur += int64(addSeconds * 1e9) }
}

func TestNew_Disabled_NilCache(t *testing.T) {
	c := New(0)
	if c != nil {
		t.Fatalf("New(0) must return a nil *Cache (disabled), got %v", c)
	}
	// nil receiver must be safe.
	if _, ok := c.Get("k"); ok {
		t.Error("nil cache Get must miss")
	}
	c.Put("k", Entry{Body: []byte("x"), ContentType: "image/png"}) // must not panic
}

func TestPutGet_RoundTrip(t *testing.T) {
	withClock(t, 0)
	c := New(8 << 20) // 8 MiB

	want := Entry{Body: []byte("rendered-bytes"), ContentType: "image/jpeg"}
	c.Put("key1", want)

	got, ok := c.Get("key1") // synchronous: immediately visible
	if !ok {
		t.Fatal("expected hit immediately after Put (cache is synchronous)")
	}
	if string(got.Body) != string(want.Body) || got.ContentType != want.ContentType {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDecay_ReducesScoreOverTime(t *testing.T) {
	advance := withClock(t, 0)
	c := New(8 << 20)

	c.Put("k", Entry{Body: []byte("v"), ContentType: "x"}) // score=1 @ t0
	s0 := c.debugDecayedScore("k")

	advance(86400) // one half-life later
	s1 := c.debugDecayedScore("k")

	if !(s1 < s0) {
		t.Errorf("decayed score must drop over time: s0=%v s1=%v", s0, s1)
	}
	// ~half after one half-life (allow tolerance).
	if s1 > s0*0.6 || s1 < s0*0.4 {
		t.Errorf("after one half-life score should ~halve: s0=%v s1=%v", s0, s1)
	}
}

func TestEviction_PicksLowestDecayedScore(t *testing.T) {
	advance := withClock(t, 0)
	// Budget fits exactly 5 × 1 MiB items; a 6th forces one eviction.
	c := New(5 << 20)
	item := make([]byte, 1<<20) // 1 MiB each

	// Insert 5 keys; make one of them hot by repeated access, keep one cold.
	for i := 0; i < 5; i++ {
		c.Put("k"+strconv.Itoa(i), Entry{Body: item, ContentType: "x"})
	}
	advance(1)
	for r := 0; r < 50; r++ { // k0 is hot
		c.Get("k0")
	}
	// Insert a 6th item -> must evict to fit. The hot k0 must survive.
	c.Put("k5", Entry{Body: item, ContentType: "x"})

	if _, ok := c.Get("k0"); !ok {
		t.Error("hot key k0 was evicted despite high decayed score")
	}
	if _, ok := c.Get("k5"); !ok {
		t.Error("newcomer k5 must always be admitted (always-admit policy)")
	}
}

func TestByteBudget_Respected(t *testing.T) {
	withClock(t, 0)
	c := New(4 << 20) // 4 MiB budget
	item := make([]byte, 1<<20)
	for i := 0; i < 20; i++ {
		c.Put("k"+strconv.Itoa(i), Entry{Body: item, ContentType: "x"})
	}
	if used := c.debugUsed(); used > int64(4<<20) {
		t.Errorf("used=%d exceeds budget=%d", used, 4<<20)
	}
}

func TestHotSurvives_ColdEvicted_UnderPressure(t *testing.T) {
	advance := withClock(t, 0)
	c := New(4 << 20)
	item := make([]byte, 1<<20)

	const hot = "HOT"
	c.Put(hot, Entry{Body: item, ContentType: "x"})
	advance(1)
	for r := 0; r < 100; r++ {
		c.Get(hot) // build a high decayed score
	}
	// Flood distinct cold keys, each accessed once.
	for r := 0; r < 50; r++ {
		c.Put("cold-"+strconv.Itoa(r), Entry{Body: item, ContentType: "x"})
	}
	if _, ok := c.Get(hot); !ok {
		t.Error("hot key evicted under cold-key pressure (decay-LFU failed)")
	}
}

func TestSingleOversizedEntry_Skipped(t *testing.T) {
	withClock(t, 0)
	c := New(1 << 20)          // 1 MiB budget
	big := make([]byte, 2<<20) // 2 MiB > budget
	c.Put("toobig", Entry{Body: big, ContentType: "x"})
	if _, ok := c.Get("toobig"); ok {
		t.Error("entry larger than total budget must be skipped, not stored")
	}
	if c.debugUsed() != 0 {
		t.Errorf("oversized skip must not change used bytes, got %d", c.debugUsed())
	}
}

// TestConcurrent_NoRace spins N goroutines doing interleaved Put/Get on a small
// budget. It must pass under `go test -race ./cache/...`, proving the single
// global mutex correctly guards the map and the used-bytes counter.
func TestConcurrent_NoRace(t *testing.T) {
	c := New(1 << 20) // small budget → constant eviction pressure
	item := make([]byte, 4<<10)

	const goroutines = 16
	const opsPerGoroutine = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := "k" + strconv.Itoa((g*opsPerGoroutine+i)%128)
				if i%2 == 0 {
					c.Put(key, Entry{Body: item, ContentType: "x"})
				} else {
					c.Get(key)
				}
			}
		}(g)
	}
	wg.Wait()

	// Invariant: used must never exceed the budget after the storm settles.
	if used := c.debugUsed(); used > int64(1<<20) {
		t.Errorf("after concurrent storm used=%d exceeds budget=%d", used, 1<<20)
	}
}

// TestOverwrite_GrowsPastBudget_EvictsToFit covers the evictToFit loop body
// reached from Put's overwrite branch: overwriting an existing key with a LARGER
// body pushes used past the budget, so evictToFit must run and evict another
// entry until used <= budget. The grown entry (just bumped twice) outscores the
// cold sibling, so the sibling is the victim.
func TestOverwrite_GrowsPastBudget_EvictsToFit(t *testing.T) {
	withClock(t, 0)
	c := New(100) // 100-byte budget

	c.Put("a", Entry{Body: make([]byte, 40), ContentType: "x"})
	c.Put("b", Entry{Body: make([]byte, 40), ContentType: "x"})
	if used := c.debugUsed(); used != 80 {
		t.Fatalf("setup used=%d, want 80", used)
	}

	// Overwrite "a" with a 90-byte body: used becomes 40-40+... actually
	// 80 - 40 + 90 = 130 > 100 → evictToFit must evict "b".
	c.Put("a", Entry{Body: make([]byte, 90), ContentType: "x"})

	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted when a grew past the budget")
	}
	got, ok := c.Get("a")
	if !ok {
		t.Fatal("a (the grown entry) should still be present")
	}
	if len(got.Body) != 90 {
		t.Errorf("a body len=%d, want 90 (the overwritten value)", len(got.Body))
	}
	if used := c.debugUsed(); used > int64(100) {
		t.Errorf("after overwrite used=%d exceeds budget=100", used)
	}
}
