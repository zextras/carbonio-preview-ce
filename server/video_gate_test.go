// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// video_gate_test.go covers the DB-resilience behaviour of the video endpoints.
//
// When the video-preview DB is absent or unreachable the video preview +
// thumbnail handlers must degrade to HTTP 424 (Failed Dependency) — an
// OPERATIONAL "the DB dependency is down" signal, distinct from 415 (which means
// the file's codec is genuinely unsupported and requires a working DB to probe).
// copy/delete must no-op with success so the fire-and-forget WSC caller never
// sees an error. It also verifies that flipping the readiness gate (DB comes up
// after boot) re-enables the normal resolve() path with no restart.
//
// The genuine-unsupported-codec → 415 mapping is unchanged and covered in
// video_api_test.go (TestResolve_Unsupported_Returns415).

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v3/cache"
	"github.com/zextras/carbonio-preview-ce/v3/db"
	"github.com/zextras/carbonio-preview-ce/v3/server/apispec"
)

// statusOf extracts the HTTP status carried by a huma error.
func statusOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected a huma error, got nil")
	}
	type statusGetter interface{ GetStatus() int }
	se, ok := err.(statusGetter)
	if !ok {
		t.Fatalf("error %T does not implement GetStatus()", err)
	}
	return se.GetStatus()
}

// noDBDeps builds a Deps with NO video DB (store nil / gate not ready).
func noDBDeps() Deps {
	return Deps{
		Cfg:      testCfg(),
		Store:    &mockStore{blob: []byte("src")},
		Cache:    cache.New(0),
		DB:       nil,
		DBGate:   newVideoGate(),
		VideoSem: nil,
	}
}

// ---------------------------------------------------------------------------
// videoGate unit behaviour
// ---------------------------------------------------------------------------

func TestVideoGate_DefaultNotReady(t *testing.T) {
	g := newVideoGate()
	if g.Store() != nil {
		t.Error("fresh gate: Store() must be nil")
	}
	if g.Ready() {
		t.Error("fresh gate: Ready() must be false")
	}
}

func TestVideoGate_SetMakesReady(t *testing.T) {
	g := newVideoGate()
	// A non-nil sentinel store pointer is enough for the gate's own semantics.
	st := &db.Store{}
	g.Set(st)
	if g.Store() != st {
		t.Error("after Set: Store() must return the set store")
	}
	if !g.Ready() {
		t.Error("after Set: Ready() must be true")
	}
}

// ---------------------------------------------------------------------------
// isDBConnError classification (runtime DB-down detection)
// ---------------------------------------------------------------------------

func TestIsDBConnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"closed pool", errors.New("db.Find: closed pool"), true},
		{"conn refused", errors.New("db.Find: dial tcp 127.78.0.6:20003: connect: connection refused"), true},
		{"eof", errors.New("db.Find: unexpected EOF"), true},
		{"broken pipe", errors.New("db.Find: write: broken pipe"), true},
		{"reset by peer", errors.New("db.Find: read: connection reset by peer"), true},
		{"no such host", errors.New("db.Find: lookup carbonio-db: no such host"), true},
		{"i/o timeout", errors.New("db.Find: i/o timeout"), true},
		{"logic error (not conn)", errors.New("db.Find: scan: sql: no rows"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDBConnError(c.err); got != c.want {
				t.Errorf("isDBConnError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Handler behaviour with NO DB → 424 (operational failed-dependency), NOT 415
// ---------------------------------------------------------------------------

func TestGetVideoPreview_NoDB_Returns424(t *testing.T) {
	deps := noDBDeps()
	handler := buildGetVideoPreview(deps, nil)
	_, err := handler(context.Background(), &apispec.VideoGetPreviewInput{
		ID:           validUUID,
		Version:      1,
		Area:         "100x100",
		ServiceType:  "files",
		Quality:      "medium",
		OutputFormat: "jpeg",
	})
	if got := statusOf(t, err); got != http.StatusFailedDependency {
		t.Errorf("status: got %d, want 424 (DB down, not 415 unsupported)", got)
	}
}

func TestGetVideoThumbnail_NoDB_Returns424(t *testing.T) {
	deps := noDBDeps()
	handler := buildGetVideoThumbnail(deps, nil)
	_, err := handler(context.Background(), &apispec.VideoGetThumbnailInput{
		ID:           validUUID,
		Version:      1,
		Area:         "100x100",
		ServiceType:  "files",
		Quality:      "medium",
		OutputFormat: "jpeg",
		Shape:        "rectangular",
	})
	if got := statusOf(t, err); got != http.StatusFailedDependency {
		t.Errorf("status: got %d, want 424 (DB down, not 415 unsupported)", got)
	}
}

// ---------------------------------------------------------------------------
// copy / delete with NO DB → success no-op (WSC fire-and-forget must not error)
// ---------------------------------------------------------------------------

func TestDeleteVideoPreview_NoDB_NoOpSuccess(t *testing.T) {
	deps := noDBDeps()
	handler := buildDeleteVideoPreview(deps)
	out, err := handler(context.Background(), &apispec.VideoDeleteInput{
		ID:          validUUID,
		Version:     1,
		ServiceType: "files",
	})
	if err != nil {
		t.Fatalf("delete with no DB must not error, got %v (status %d)", err, statusOf(t, err))
	}
	_ = out // nil output → 204
}

func TestCopyVideoPreview_NoDB_NoOpSuccess(t *testing.T) {
	deps := noDBDeps()
	handler := buildCopyVideoPreview(deps)
	out, err := handler(context.Background(), &apispec.VideoCopyInput{
		ID:          validUUID,
		Target:      "223e4567-e89b-12d3-a456-426614174111",
		Version:     1,
		ServiceType: "files",
	})
	if err != nil {
		t.Fatalf("copy with no DB must not error, got %v (status %d)", err, statusOf(t, err))
	}
	if out == nil {
		t.Fatal("copy no-op should still return a (empty) output body")
	}
}

// ---------------------------------------------------------------------------
// End-to-end over the real mux: no DB configured ⇒ image works AND video → 424.
// ---------------------------------------------------------------------------

func TestBuildMux_NoDB_ImageWorks_Video424(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte("rendered-out"), nil)
	defer restore()

	// New server with NO WithDB — DB layer disabled.
	s := New(testCfg(), store, cache.New(8<<20))
	ts := httptest.NewServer(loggingMiddleware(s.buildMux(nil)))
	defer ts.Close()

	// Image preview still works.
	iresp, err := http.Get(ts.URL + "/preview/image/" + validUUID + "/1/100x100/?service_type=files")
	if err != nil {
		t.Fatalf("GET image preview: %v", err)
	}
	iresp.Body.Close()
	if iresp.StatusCode != http.StatusOK {
		t.Errorf("image preview status=%d, want 200 (must be unaffected by absent DB)", iresp.StatusCode)
	}

	// Video preview → 424.
	vresp, err := http.Get(ts.URL + "/preview/video/" + validUUID + "/1/100x100/?service_type=files")
	if err != nil {
		t.Fatalf("GET video preview: %v", err)
	}
	vresp.Body.Close()
	if vresp.StatusCode != http.StatusFailedDependency {
		t.Errorf("video preview status=%d, want 424 (DB absent)", vresp.StatusCode)
	}

	// Health stays 200.
	hresp, err := http.Get(ts.URL + "/health/live/")
	if err != nil {
		t.Fatalf("GET health live: %v", err)
	}
	hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		t.Errorf("health live status=%d, want 200", hresp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// DB becomes ready after boot ⇒ video proceeds through the normal resolve()
// path with no restart (readiness gate flip).
// ---------------------------------------------------------------------------

func TestGetVideoPreview_GateFlipsReady_Proceeds(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	// Start with a NOT-ready gate: handler must return 424.
	gate := newVideoGate()
	deps := Deps{
		Cfg:      testCfg(),
		Store:    &mockStore{blob: []byte("src")},
		Cache:    cache.New(0),
		DB:       nil,
		DBGate:   gate,
		VideoSem: nil,
	}
	handler := buildGetVideoPreview(deps, nil)

	// Use a real UUID so validateUUID passes.
	const fileID = "b1b2c3d4-e5f6-7890-abcd-ef1234567890"

	_, err := handler(ctx, &apispec.VideoGetPreviewInput{
		ID: fileID, Version: 1, Area: "100x100",
		ServiceType: "files", Quality: "medium", OutputFormat: "jpeg",
	})
	if got := statusOf(t, err); got != http.StatusFailedDependency {
		t.Fatalf("before flip: status %d, want 424", got)
	}

	// Flip the gate ready (simulates the background init succeeding).
	gate.Set(dbStore)

	// Same handler instance, no restart: a not-found row now enqueues + returns 202.
	_, err = handler(ctx, &apispec.VideoGetPreviewInput{
		ID: fileID, Version: 1, Area: "100x100",
		ServiceType: "files", Quality: "medium", OutputFormat: "jpeg",
	})
	if got := statusOf(t, err); got != http.StatusAccepted {
		t.Fatalf("after flip: status %d, want 202 (normal resolve path)", got)
	}

	// The row must have been enqueued (proof the DB path ran).
	row, ferr := dbStore.Find(ctx, fileID, 1)
	if ferr != nil {
		t.Fatalf("Find: %v", ferr)
	}
	if row == nil {
		t.Fatal("expected an enqueued row after gate flip, got nil")
	}
}

// ---------------------------------------------------------------------------
// Runtime DB connection error (DB went down after boot) ⇒ 424, not 500/503.
// Simulated by closing the pool out from under a ready gate.
// ---------------------------------------------------------------------------

func TestGetVideoPreview_RuntimeConnError_Returns424(t *testing.T) {
	dbStore := startVideoPostgres(t)
	ctx := context.Background()

	gate := newVideoGate()
	gate.Set(dbStore)
	deps := Deps{
		Cfg:      testCfg(),
		Store:    &mockStore{blob: []byte("src")},
		Cache:    cache.New(0),
		DB:       nil,
		DBGate:   gate,
		VideoSem: nil,
	}
	handler := buildGetVideoPreview(deps, nil)

	// Kill the DB connection: subsequent queries fail with a conn-type error.
	dbStore.Close()

	const fileID = "c1b2c3d4-e5f6-7890-abcd-ef1234567890"
	_, err := handler(ctx, &apispec.VideoGetPreviewInput{
		ID: fileID, Version: 1, Area: "100x100",
		ServiceType: "files", Quality: "medium", OutputFormat: "jpeg",
	})
	if got := statusOf(t, err); got != http.StatusFailedDependency {
		t.Errorf("runtime conn error: status %d, want 424 (graceful degrade, not 500/503)", got)
	}
}
