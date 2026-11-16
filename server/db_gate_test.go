// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// db_gate_test.go covers the DB-resilience behaviour of the thumbnail endpoint.
//
// When the thumbnail DB is absent or unreachable the async GET /thumbnail
// handler must degrade to HTTP 424 (Failed Dependency) — an OPERATIONAL "the DB
// dependency is down" signal, distinct from 415 (which means the file's codec is
// genuinely unsupported and requires a working DB to probe). It also verifies
// that flipping the readiness gate (DB comes up after boot) re-enables the
// normal resolve path with no restart.
//
// The no-DB no-op of DELETE /thumbnail/{id} is covered in thumbnail_api_test.go
// (TestDeleteThumbnails_NoDB_NoOp204).

package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
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

// ---------------------------------------------------------------------------
// dbGate unit behaviour
// ---------------------------------------------------------------------------

func TestDBGate_DefaultNotReady(t *testing.T) {
	g := newDBGate()
	if g.Store() != nil {
		t.Error("fresh gate: Store() must be nil")
	}
	if g.Ready() {
		t.Error("fresh gate: Ready() must be false")
	}
}

func TestDBGate_SetMakesReady(t *testing.T) {
	g := newDBGate()
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
// End-to-end over the real mux: no DB configured ⇒ image works AND video → 424.
// ---------------------------------------------------------------------------

func TestBuildMux_NoDB_ImageWorks_Video424(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	prevPair, prevProbe := imagePairFunc, probeFunc
	imagePairFunc = func([]byte) (render.Pair, error) { return render.Pair{S: stubS, M: stubM}, nil }
	probeFunc = func([]byte) (render.Info, error) { return render.Info{Width: 4000, Height: 3000, Pages: 1}, nil }
	defer func() { imagePairFunc, probeFunc = prevPair, prevProbe }()

	// New server with NO WithDB — DB layer disabled.
	s := New(testCfg(), store, cache.New(8<<20))
	ts := httptest.NewServer(loggingMiddleware(s.buildMux()))
	defer ts.Close()

	// Image thumbnail is generated without the DB.
	iresp, err := http.Get(ts.URL + "/thumbnail/" + validUUID + "/1/m?service_type=files&mime=image/jpeg")
	if err != nil {
		t.Fatalf("GET image thumbnail: %v", err)
	}
	ibody, _ := io.ReadAll(iresp.Body)
	iresp.Body.Close()
	if iresp.StatusCode != http.StatusOK || string(ibody) != string(stubM.Body) {
		t.Errorf("image thumbnail status=%d body=%q, want 200 %q (must be unaffected by absent DB)", iresp.StatusCode, ibody, stubM.Body)
	}

	// Video thumbnail → 424.
	vreq, err := http.NewRequest(http.MethodGet, ts.URL+"/thumbnail/"+validUUID+"/1/m?service_type=files&mime=video/mp4", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	vreq.Header.Set("fileownerid", testOwner)
	vresp, err := http.DefaultClient.Do(vreq)
	if err != nil {
		t.Fatalf("GET video thumbnail: %v", err)
	}
	vresp.Body.Close()
	if vresp.StatusCode != http.StatusFailedDependency {
		t.Errorf("video thumbnail status=%d, want 424 (DB absent)", vresp.StatusCode)
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
// DB becomes ready after boot ⇒ video proceeds through the normal resolve
// path with no restart (readiness gate flip).
// ---------------------------------------------------------------------------

func gateHarness(t *testing.T, gate *dbGate) *harness {
	t.Helper()
	return newHarness(t, harnessOpts{noDB: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.DB = nil
		d.DBGate = gate
		return d
	}})
}

func TestThumbnailGet_GateFlipsReady_Proceeds(t *testing.T) {
	dbStore := startDBStore(t)

	gate := newDBGate()
	h := gateHarness(t, gate)
	fileID := newID()

	if rec := h.get(thumbReq{id: fileID, version: 1, mime: videoMime}); rec.Code != http.StatusFailedDependency {
		t.Fatalf("before flip: status %d, want 424", rec.Code)
	}

	gate.Set(dbStore)

	if rec := h.get(thumbReq{id: fileID, version: 1, mime: videoMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("after flip: status %d, want 202 (normal resolve path)", rec.Code)
	}

	rows, ferr := dbStore.FindPair(context.Background(), fileID, 1)
	if ferr != nil {
		t.Fatalf("FindPair: %v", ferr)
	}
	if len(rows) != 2 {
		t.Fatalf("expected both sizes enqueued after gate flip, got %d rows", len(rows))
	}
}

// ---------------------------------------------------------------------------
// Runtime DB connection error (DB went down after boot) ⇒ 424, not 500/503.
// Simulated by closing the pool out from under a ready gate.
// ---------------------------------------------------------------------------

func TestThumbnailGet_RuntimeConnError_Returns424(t *testing.T) {
	dbStore := startDBStore(t)

	gate := newDBGate()
	gate.Set(dbStore)
	h := gateHarness(t, gate)

	dbStore.Close()

	if rec := h.get(thumbReq{id: newID(), version: 1, mime: videoMime}); rec.Code != http.StatusFailedDependency {
		t.Errorf("runtime conn error: status %d, want 424 (graceful degrade, not 500/503)", rec.Code)
	}
}
