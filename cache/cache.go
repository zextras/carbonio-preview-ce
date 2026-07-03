// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package cache provides a frequency-aware in-process cache of rendered preview
// output bytes, implemented with the standard library only (no dependencies).
//
// Algorithm: sampled decay-LFU, modeled on Redis allkeys-lfu. Each entry holds
// a frequency score that decays exponentially with time and is bumped by +1 on
// every access. On insert pressure we sample K random entries (via Go's
// randomized map iteration) and evict the one with the lowest CURRENTLY-decayed
// score, repeating until the newcomer fits. Admission is always-admit: the new
// entry is never rejected by a frequency comparison; the only guard is that an
// entry larger than the whole budget cannot fit and is skipped.
//
// Concurrency: a single global mutex guards the map and the used-bytes counter.
// Critical sections are map-op short, so a single lock is both correct and
// cheap; sharding is intentionally avoided (it would also break the global byte
// budget when single entries can be large). All operations are synchronous: a
// Get immediately after a Put observes the entry — there is no async buffer and
// no eventual-consistency window.
//
// A nil *Cache is the disabled state: Get always misses and Put is a no-op, so
// callers never branch on "enabled".
package cache

import (
	"math"
	"sync"
	"time"
)

// halfLifeSeconds is the exponential-decay half-life of an entry's frequency
// score (1 day). Internal constant — intentionally NOT configurable.
const halfLifeSeconds = 86400.0

// evictionSample is the number of entries sampled per eviction round; the
// lowest decayed score among them is evicted. Internal constant — Redis uses 5
// for allkeys-lfu and it is intentionally NOT configurable here.
const evictionSample = 5

// nowNano returns the current time in unix nanoseconds. It is a package-level
// var so tests can replace it with a deterministic clock (no sleeping).
var nowNano = func() int64 { return time.Now().UnixNano() }

// Entry is a cached rendered response returned to handlers.
type Entry struct {
	// Body is the rendered output bytes (image/pdf payload).
	Body []byte
	// ContentType is the Content-Type header to send with Body.
	ContentType string
}

// item is the internal cache record: the value plus its decay-LFU bookkeeping.
type item struct {
	body        []byte
	contentType string
	score       float64 // frequency score AT lastAccess
	lastAccess  int64   // unix nanos of the last score update
}

// Cache is a sampled decay-LFU byte-budgeted cache. The zero value is not
// usable; build one with New.
type Cache struct {
	mu     sync.Mutex
	m      map[string]*item
	used   int64 // sum of len(body) over all items
	budget int64 // byte budget (render.cache-max-mb × 1024×1024)
}

// New builds a Cache with the given byte budget. A budget <= 0 disables caching:
// New returns nil, and the returned nil *Cache is safe to call.
func New(maxBytes int64) *Cache {
	if maxBytes <= 0 {
		return nil
	}
	return &Cache{
		m:      make(map[string]*item),
		budget: maxBytes,
	}
}

// decayedScore returns the score the item would have at time now, without
// mutating it. Caller must hold c.mu.
func decayedScore(it *item, now int64) float64 {
	elapsed := float64(now-it.lastAccess) / 1e9
	if elapsed <= 0 {
		return it.score
	}
	return it.score * math.Pow(0.5, elapsed/halfLifeSeconds)
}

// bump applies decay-to-now then +1, recording the new lastAccess.
// Caller must hold c.mu.
func bump(it *item, now int64) {
	it.score = decayedScore(it, now) + 1
	it.lastAccess = now
}

// Get returns the cached Entry for key, or (zero, false) on a miss. On a hit it
// bumps the entry's frequency score. Safe on a nil *Cache (always a miss).
func (c *Cache) Get(key string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.m[key]
	if !ok {
		return Entry{}, false
	}
	bump(it, nowNano())
	return Entry{Body: it.body, ContentType: it.contentType}, true
}

// Put stores entry under key with cost = len(entry.Body). It evicts (sampled,
// lowest-decayed-score) until the newcomer fits, then always admits it. An
// entry larger than the whole budget cannot fit and is skipped. No TTL: entries
// are content-immutable because version is part of the key. Safe on a nil
// *Cache (no-op).
func (c *Cache) Put(key string, entry Entry) {
	if c == nil {
		return
	}
	cost := int64(len(entry.Body))
	if cost > c.budget {
		return // cannot fit; skip caching this oversized entry
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := nowNano()

	// Overwrite of an existing key: adjust used bytes, then update in place.
	if old, ok := c.m[key]; ok {
		c.used -= int64(len(old.body))
		c.used += cost
		old.body = entry.Body
		old.contentType = entry.ContentType
		bump(old, now)
		c.evictToFit(now) // overwrite may grow used past budget
		return
	}

	// New key: evict until there is room for cost, then insert.
	for c.used+cost > c.budget && len(c.m) > 0 {
		c.evictOne(now)
	}
	it := &item{body: entry.Body, contentType: entry.ContentType, lastAccess: now}
	bump(it, now) // sets score to 1 at now
	c.m[key] = it
	c.used += cost
}

// evictToFit evicts lowest-decayed-score entries until used <= budget.
// Caller must hold c.mu.
func (c *Cache) evictToFit(now int64) {
	for c.used > c.budget && len(c.m) > 0 {
		c.evictOne(now)
	}
}

// evictOne samples up to evictionSample entries and removes the one with the
// lowest decayed score. Sampling exploits Go's randomized map iteration order,
// so no RNG and no allocation beyond the loop are needed. Caller must hold c.mu.
func (c *Cache) evictOne(now int64) {
	var victimKey string
	var victim *item
	var victimScore float64
	seen := 0
	for k, it := range c.m {
		s := decayedScore(it, now)
		if victim == nil || s < victimScore {
			victimKey, victim, victimScore = k, it, s
		}
		seen++
		if seen >= evictionSample {
			break
		}
	}
	if victim != nil {
		c.used -= int64(len(victim.body))
		delete(c.m, victimKey)
	}
}

// --- test-only accessors (unexported; used by cache_test.go) ---

func (c *Cache) debugUsed() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func (c *Cache) debugDecayedScore(key string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.m[key]
	if !ok {
		return 0
	}
	return decayedScore(it, nowNano())
}
