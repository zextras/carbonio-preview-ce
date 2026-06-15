// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"testing"

	"github.com/zextras/carbonio-preview-ce/cache"
)

func newCacheTestServer(t *testing.T, store *mockStore, maxBytes int64) *http.ServeMux {
	t.Helper()
	c := cache.New(maxBytes)
	cfg := testCfg()
	mux := http.NewServeMux()
	registerImageRoutes(mux, cfg, store, c, nil)
	registerPDFRoutes(mux, cfg, store, c, nil)
	registerDocumentRoutes(mux, cfg, store, c, nil)
	return mux
}

// TestCacheHit_SkipsSecondFetch: two identical GETs → exactly one storages fetch.
func TestCacheHit_SkipsSecondFetch(t *testing.T) {
	store := &mockStore{blob: []byte("img-source")}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte("rendered-out"), nil)
	defer restore()
	mux := newCacheTestServer(t, store, 8<<20)

	path := "/preview/image/" + validUUID + "/1/100x100/?service_type=files"

	rec1 := doRequest(mux, http.MethodGet, path)
	if rec1.Code != http.StatusOK || rec1.Body.String() != "rendered-out" {
		t.Fatalf("first GET: code=%d body=%q", rec1.Code, rec1.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("after first GET, store.calls = %d, want 1", store.calls)
	}
	if ct := rec1.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}

	// Synchronous cache: the second identical GET MUST hit without re-fetching.
	rec2 := doRequest(mux, http.MethodGet, path)
	if rec2.Code != http.StatusOK || rec2.Body.String() != "rendered-out" {
		t.Fatalf("second GET: code=%d body=%q", rec2.Code, rec2.Body.String())
	}
	if store.calls != 1 {
		t.Errorf("second GET must be served from cache; store.calls = %d, want 1", store.calls)
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("cached Content-Type = %q, want image/jpeg", ct)
	}
}

// TestCacheDisabled_AlwaysFetches: maxBytes=0 → every GET fetches.
func TestCacheDisabled_AlwaysFetches(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte("out"), nil)
	defer restore()
	mux := newCacheTestServer(t, store, 0)

	path := "/preview/image/" + validUUID + "/1/100x100/?service_type=files"
	doRequest(mux, http.MethodGet, path)
	doRequest(mux, http.MethodGet, path)
	if store.calls != 2 {
		t.Errorf("disabled cache: store.calls = %d, want 2", store.calls)
	}
}

// TestCacheVersionDifferentiation: different version → different entry → 2 fetches.
func TestCacheVersionDifferentiation(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte("out"), nil)
	defer restore()
	mux := newCacheTestServer(t, store, 8<<20)

	doRequest(mux, http.MethodGet, "/preview/image/"+validUUID+"/1/100x100/?service_type=files")
	doRequest(mux, http.MethodGet, "/preview/image/"+validUUID+"/2/100x100/?service_type=files")
	if store.calls != 2 {
		t.Errorf("version 1 and 2 must each fetch; store.calls = %d, want 2", store.calls)
	}
	// Re-request v1: must be a cache hit (no third fetch).
	doRequest(mux, http.MethodGet, "/preview/image/"+validUUID+"/1/100x100/?service_type=files")
	if store.calls != 2 {
		t.Errorf("repeat of v1 must hit cache; store.calls = %d, want 2", store.calls)
	}
}
