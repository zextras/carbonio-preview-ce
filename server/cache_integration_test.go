// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v3/cache"
)

func newCacheTestServer(t *testing.T, store *mockStore, maxBytes int64) *http.ServeMux {
	t.Helper()
	c := cache.New(maxBytes)
	cfg := testCfg()
	mux := http.NewServeMux()
	api := newHumaAPI(mux, cfg)
	registerImageOps(api, Deps{Cfg: cfg, Store: store, Cache: c, Sem: nil})
	registerPDFOps(api, Deps{Cfg: cfg, Store: store, Cache: c, Sem: nil})
	registerDocumentOps(api, Deps{Cfg: cfg, Store: store, Cache: c, Sem: nil})
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

// TestCacheKey_OwnerDifferentiation: different ownerID ⇒ different cache key.
// This is the ADV delta: PowerStore routing scopes entries per file owner, and
// the shared CE cacheKey must keep two owners' renders distinct. It would fail
// if ownerID were ever dropped from the key construction.
func TestCacheKey_OwnerDifferentiation(t *testing.T) {
	k1 := cacheKey("img-preview", validUUID, 1, "files", 100, 100, "medium", "jpeg", false, "rectangular", 1, 0, "en-US", "ownerA")
	k2 := cacheKey("img-preview", validUUID, 1, "files", 100, 100, "medium", "jpeg", false, "rectangular", 1, 0, "en-US", "ownerB")
	if k1 == k2 {
		t.Error("different ownerID must produce different cache keys (ADV owner-scoping)")
	}
	kEmpty := cacheKey("img-preview", validUUID, 1, "files", 100, 100, "medium", "jpeg", false, "rectangular", 1, 0, "en-US", "")
	if kEmpty == k1 {
		t.Error("empty ownerID (CE) must differ from a populated one")
	}
	// Identical args must produce identical keys (key is a pure function of args).
	if cacheKey("img-preview", validUUID, 1, "files", 100, 100, "medium", "jpeg", false, "rectangular", 1, 0, "en-US", "ownerA") != k1 {
		t.Error("identical args must produce identical cache keys")
	}
}

// TestPDFPreviewCacheHit_SkipsSecondFetch: two identical PDF preview GETs → one fetch.
func TestPDFPreviewCacheHit_SkipsSecondFetch(t *testing.T) {
	store := &mockStore{blob: []byte("pdf-src")}
	restore := stubPDFSlice([]byte("sliced-pdf"), nil)
	defer restore()
	mux := newCacheTestServer(t, store, 8<<20)

	path := "/preview/pdf/" + validUUID + "/1/?service_type=files"
	rec1 := doRequest(mux, http.MethodGet, path)
	if rec1.Code != http.StatusOK || rec1.Body.String() != "sliced-pdf" {
		t.Fatalf("first GET: code=%d body=%q", rec1.Code, rec1.Body.String())
	}
	if ct := rec1.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
	if store.calls != 1 {
		t.Fatalf("after first GET store.calls=%d, want 1", store.calls)
	}
	rec2 := doRequest(mux, http.MethodGet, path)
	if rec2.Code != http.StatusOK || rec2.Body.String() != "sliced-pdf" {
		t.Fatalf("second GET: code=%d body=%q", rec2.Code, rec2.Body.String())
	}
	if store.calls != 1 {
		t.Errorf("second GET must hit cache; store.calls=%d, want 1", store.calls)
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("cached Content-Type = %q, want application/pdf", ct)
	}
}

// TestPDFThumbnailCacheHit_RoundedCachesPNG: rounded thumbnail must cache the
// ACTUAL written content-type (image/png), not the requested output_format
// (jpeg), and a second GET must hit the cache.
func TestPDFThumbnailCacheHit_RoundedCachesPNG(t *testing.T) {
	store := &mockStore{blob: []byte("pdf-src")}
	// rasterize stub returns PNG bytes; rounded shape must cache image/png (the ACTUAL content-type).
	restore := stubPDFRasterize([]byte("png-bytes"), nil)
	defer restore()
	mux := newCacheTestServer(t, store, 8<<20)

	path := "/preview/pdf/" + validUUID + "/1/100x100/thumbnail/?service_type=files&shape=rounded&output_format=jpeg"
	rec1 := doRequest(mux, http.MethodGet, path)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first GET code=%d", rec1.Code)
	}
	if ct := rec1.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("rounded thumb Content-Type=%q, want image/png (forced PNG)", ct)
	}
	rec2 := doRequest(mux, http.MethodGet, path)
	if store.calls != 1 {
		t.Errorf("thumbnail second GET must hit cache; store.calls=%d, want 1", store.calls)
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("cached rounded thumb Content-Type=%q, want image/png", ct)
	}
	if rec2.Body.String() != "png-bytes" {
		t.Errorf("cached body=%q, want png-bytes", rec2.Body.String())
	}
}

// TestPDFThumbnailPost_DoesNotCache: POST passes (nil cache, "" key) into
// renderPDFThumbnail, so two identical POSTs both render and storage is never
// touched. This covers the false arm of renderPDFThumbnail's conditional put.
func TestPDFThumbnailPost_DoesNotCache(t *testing.T) {
	store := &mockStore{blob: []byte("unused")}
	restore := stubPDFRasterize([]byte("png-bytes"), nil)
	defer restore()
	mux := newCacheTestServer(t, store, 8<<20)

	post := func() *httptest.ResponseRecorder {
		body, ct := buildMultipart(t, "file", "test.pdf", []byte(fakePDFBytes))
		req := httptest.NewRequest(http.MethodPost, "/preview/pdf/100x100/thumbnail/?shape=rounded", body)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec1 := post()
	if rec1.Code != http.StatusOK || rec1.Body.String() != "png-bytes" {
		t.Fatalf("first POST: code=%d body=%q", rec1.Code, rec1.Body.String())
	}
	rec2 := post()
	if rec2.Code != http.StatusOK || rec2.Body.String() != "png-bytes" {
		t.Fatalf("second POST: code=%d body=%q", rec2.Code, rec2.Body.String())
	}
	// POST handlers never fetch storage, regardless of caching.
	if store.calls != 0 {
		t.Errorf("POST must never fetch storage; store.calls=%d, want 0", store.calls)
	}
}

// TestDocPreviewCacheHit_SkipsSecondFetch: two identical document preview GETs → one fetch.
func TestDocPreviewCacheHit_SkipsSecondFetch(t *testing.T) {
	store := &mockStore{blob: []byte("doc-src")}
	restoreC := stubCollaboraConvert([]byte("converted-pdf"), nil)
	defer restoreC()
	restoreS := stubPDFSlice([]byte("sliced-pdf"), nil)
	defer restoreS()
	mux := newCacheTestServer(t, store, 8<<20)

	path := "/preview/document/" + validUUID + "/1/?service_type=files"
	rec1 := doRequest(mux, http.MethodGet, path)
	if rec1.Code != http.StatusOK || rec1.Body.String() != "sliced-pdf" {
		t.Fatalf("first GET: code=%d body=%q", rec1.Code, rec1.Body.String())
	}
	doRequest(mux, http.MethodGet, path)
	if store.calls != 1 {
		t.Errorf("doc preview second GET must hit cache; store.calls=%d, want 1", store.calls)
	}
}

// TestDocThumbnailCacheHit_SkipsSecondFetch: two identical document thumbnail GETs → one fetch.
func TestDocThumbnailCacheHit_SkipsSecondFetch(t *testing.T) {
	store := &mockStore{blob: []byte("doc-src")}
	restoreC := stubCollaboraConvert([]byte("converted-pdf"), nil)
	defer restoreC()
	restoreR := stubPDFRasterize([]byte("thumb-bytes"), nil)
	defer restoreR()
	mux := newCacheTestServer(t, store, 8<<20)

	path := "/preview/document/" + validUUID + "/1/100x100/thumbnail/?service_type=files"
	rec1 := doRequest(mux, http.MethodGet, path)
	if rec1.Code != http.StatusOK || rec1.Body.String() != "thumb-bytes" {
		t.Fatalf("first GET: code=%d body=%q", rec1.Code, rec1.Body.String())
	}
	doRequest(mux, http.MethodGet, path)
	if store.calls != 1 {
		t.Errorf("doc thumb second GET must hit cache; store.calls=%d, want 1", store.calls)
	}
}

// TestCacheCrossRouteIsolation_ImgVsPdf: the same id/version on img-preview and
// pdf-preview must NOT collide; the leading kind discriminator in cacheKey keeps
// them distinct, so each route fetches storage independently.
func TestCacheCrossRouteIsolation_ImgVsPdf(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	restoreImg := stubImageThumbnail("jpeg", "", "", "", []byte("img-out"), nil)
	defer restoreImg()
	restorePdf := stubPDFSlice([]byte("pdf-out"), nil)
	defer restorePdf()
	mux := newCacheTestServer(t, store, 8<<20)

	imgRec := doRequest(mux, http.MethodGet, "/preview/image/"+validUUID+"/1/100x100/?service_type=files")
	pdfRec := doRequest(mux, http.MethodGet, "/preview/pdf/"+validUUID+"/1/?service_type=files")
	if imgRec.Code != http.StatusOK || pdfRec.Code != http.StatusOK {
		t.Fatalf("img code=%d pdf code=%d", imgRec.Code, pdfRec.Code)
	}
	if bytes.Equal(imgRec.Body.Bytes(), pdfRec.Body.Bytes()) {
		t.Error("img-preview and pdf-preview share a cache key — kind discriminator broken")
	}
	if store.calls != 2 {
		t.Errorf("two different routes must each fetch; store.calls=%d, want 2", store.calls)
	}
}
