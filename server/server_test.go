// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
)

// Run is intentionally NOT unit-tested here: it calls render.InitVips, requires
// the carbonio-preview-pdfium-worker binary, and blocks on OS signals. It is
// exercised by the Phase 2 in-process full-flow tests that boot the real
// binary. This file covers everything around it: New, buildMux,
// loggingMiddleware and responseWriter.WriteHeader, driven through a real
// httptest.Server over the real buildMux.

// TestBuildMux_RoutesWired constructs a Server with New, builds the real mux,
// wraps it in loggingMiddleware, and serves it over an httptest.Server. It
// asserts that an image-preview route (with stubbed render + a mockStore) and
// the health/live route are both wired and reachable.
func TestBuildMux_RoutesWired(t *testing.T) {
	store := &mockStore{blob: []byte("src")}
	restore := stubImageThumbnail("jpeg", "", "", "", []byte("rendered-out"), nil)
	defer restore()

	s := New(testCfg(), store, cache.New(8<<20))
	mux := s.buildMux()
	ts := httptest.NewServer(loggingMiddleware(mux))
	defer ts.Close()

	// Image preview route (drives the real handler + cache.Put through buildMux).
	resp, err := http.Get(ts.URL + "/preview/image/" + validUUID + "/1/100x100/?service_type=files")
	if err != nil {
		t.Fatalf("GET image preview: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("image preview status=%d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("image preview Content-Type=%q, want image/jpeg", ct)
	}
	if string(body) != "rendered-out" {
		t.Errorf("image preview body=%q, want rendered-out", string(body))
	}

	// Health live route is wired and always returns 200.
	hresp, err := http.Get(ts.URL + "/health/live/")
	if err != nil {
		t.Fatalf("GET health live: %v", err)
	}
	hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		t.Errorf("health live status=%d, want 200", hresp.StatusCode)
	}
}

// TestNew_ConstructsServer verifies New wires cfg/store/cache onto the Server.
func TestNew_ConstructsServer(t *testing.T) {
	cfg := testCfg()
	store := &mockStore{}
	c := cache.New(1 << 20)
	s := New(cfg, store, c)

	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.cfg != cfg {
		t.Error("cfg not stored")
	}
	if s.store != store {
		t.Error("store not stored")
	}
	if s.cache != c {
		t.Error("cache not stored")
	}
}

// TestLoggingMiddleware_CapturesNon200 drives a method-not-allowed path through
// the wrapped mux. This forces responseWriter.WriteHeader to record a non-200
// status (405) and exercises the loggingMiddleware wrapping for an error
// response.
func TestLoggingMiddleware_CapturesNon200(t *testing.T) {
	store := &mockStore{}
	s := New(testCfg(), store, nil)
	mux := s.buildMux()
	ts := httptest.NewServer(loggingMiddleware(mux))
	defer ts.Close()

	// DELETE on an image route → 405 (the image router writes 405 directly).
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/preview/image/"+validUUID+"/1/100x100/?service_type=files", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", resp.StatusCode)
	}
}

// TestLoggingMiddleware_Captures404 drives an unmatched path through the
// wrapper, covering the WriteHeader status-capture for a 404 response.
// Note: huma registers trailing-slash patterns as subtree matches in Go 1.22
// ServeMux, so extra segments under /preview/image/... now route to the handler.
// We use a path prefix that has no registered route at all.
func TestLoggingMiddleware_Captures404(t *testing.T) {
	store := &mockStore{}
	s := New(testCfg(), store, nil)
	ts := httptest.NewServer(loggingMiddleware(s.buildMux()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/completely/unknown/path/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}

func startProductionServer(t *testing.T, s *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := s.httpServer(ln.Addr().String())
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func TestHandler_BodyBoundedByInactivityNotTotalTime(t *testing.T) {
	const window = 300 * time.Millisecond
	swap(t, &bodyIdleTimeout, func(*config.Config) time.Duration { return window })
	var renders atomic.Int32
	stubRender(t, func([]byte) ([]byte, error) {
		renders.Add(1)
		return []byte("rendered"), nil
	})
	addr := startProductionServer(t, New(testCfg(), &mockStore{}, nil))

	body, ct := buildMultipart(t, "file", "img.jpg", bytes.Repeat([]byte("x"), 4096))
	raw := body.Bytes()

	t.Run("upload trickled slower than the window in total succeeds", func(t *testing.T) {
		conn := startRequest(t, addr, http.MethodPost, "/preview/image/100x100/", ct, len(raw))
		const chunks = 8
		step := (len(raw) + chunks - 1) / chunks
		for off := 0; off < len(raw); off += step {
			if _, err := conn.Write(raw[off:min(off+step, len(raw))]); err != nil {
				t.Fatalf("write: %v", err)
			}
			time.Sleep(window / 5)
		}
		if resp := readResponse(t, conn); resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, want 200", resp.StatusCode)
		}
	})

	t.Run("upload stalled longer than the window fails", func(t *testing.T) {
		before := renders.Load()
		conn := startRequest(t, addr, http.MethodPost, "/preview/image/100x100/", ct, len(raw))
		if _, err := conn.Write(raw[:len(raw)/2]); err != nil {
			t.Fatalf("write: %v", err)
		}
		if resp := readResponse(t, conn); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422 (multipart read failed on the idle deadline)", resp.StatusCode)
		}
		if renders.Load() != before {
			t.Error("render ran for a stalled upload")
		}
	})

	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"unread stalled body on an unknown route is answered", http.MethodPost, "/no/such/route/", http.StatusNotFound},
		{"unread stalled body on a GET route is answered", http.MethodGet, "/health/live/", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := startRequest(t, addr, tc.method, tc.path, "application/octet-stream", 100000)
			if _, err := conn.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
				t.Fatalf("write: %v", err)
			}
			if resp := readResponse(t, conn); resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func startRequest(t *testing.T, addr, method, path, contentType string, length int, headers ...string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(2 * slotWatchdog)); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	extra := ""
	for _, h := range headers {
		extra += h + "\r\n"
	}
	_, err = fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: preview\r\nContent-Type: %s\r\nContent-Length: %d\r\n%s\r\n", method, path, contentType, length, extra)
	if err != nil {
		t.Fatalf("write headers: %v", err)
	}
	return conn
}

func readResponse(t *testing.T, conn net.Conn) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	sets int
}

func (d *deadlineRecorder) SetReadDeadline(time.Time) error {
	d.sets++
	return nil
}

func TestBodyIdleDeadline_ArmedBeforeHandlerAndNotRenewedAfterEOF(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	var armedBeforeHandler int
	h := withBodyIdleDeadline(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		armedBeforeHandler = rec.sets
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("read: %v", err)
		}
		afterEOF := rec.sets
		buf := make([]byte, 8)
		if n, err := r.Body.Read(buf); n != 0 || err != io.EOF {
			t.Errorf("read after EOF: n=%d err=%v, want 0 EOF", n, err)
		}
		if rec.sets != afterEOF {
			t.Errorf("deadline renewed after the body returned EOF (%d -> %d)", afterEOF, rec.sets)
		}
	}), time.Second)

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abc")))
	if armedBeforeHandler != 1 {
		t.Errorf("deadline sets before the handler=%d, want 1", armedBeforeHandler)
	}
}

func TestBodyIdleDeadline_RestoresOriginalBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abc"))
	orig := req.Body
	withBodyIdleDeadline(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Body == orig {
			t.Error("handler did not get the deadline-renewing body")
		}
	}), time.Second).ServeHTTP(&deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, req)
	if req.Body != orig {
		t.Error("original body not restored after the handler returned")
	}
}

func TestHandler_UnreadBodyFastPathsKept(t *testing.T) {
	swap(t, &bodyIdleTimeout, func(*config.Config) time.Duration { return time.Hour })
	addr := startProductionServer(t, New(testCfg(), &mockStore{}, nil))

	t.Run("unread body too large to drain closes without reading it", func(t *testing.T) {
		conn := startRequest(t, addr, http.MethodPost, "/no/such/route/", "application/octet-stream", 1<<20)
		if _, err := conn.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if resp := readResponse(t, conn); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d, want 404", resp.StatusCode)
		}
	})

	t.Run("unread Expect 100-continue body is never requested", func(t *testing.T) {
		conn := startRequest(t, addr, http.MethodPost, "/no/such/route/", "application/octet-stream", 1024, "Expect: 100-continue")
		if resp := readResponse(t, conn); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d, want 404 without a 100 Continue", resp.StatusCode)
		}
	})
}
