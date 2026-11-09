// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testIdle = 300 * time.Millisecond

func doReq(t *testing.T, ctx context.Context, srv *httptest.Server, method string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, srv.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	return DoWithInactivity(&http.Client{Transport: NewTransport()}, req, testIdle)
}

func TestNewTransport(t *testing.T) {
	tr := NewTransport()
	if tr.MaxIdleConnsPerHost != 0 || tr.MaxIdleConns != 0 || tr.IdleConnTimeout != idleConnTimeout || tr.DialContext == nil || tr.Proxy != nil {
		t.Fatalf("unexpected transport: %+v", tr)
	}
}

func TestDoWithInactivity_SlowButFlowingBodySucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			_, _ = w.Write([]byte("x"))
			f.Flush()
			time.Sleep(testIdle / 4)
		}
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := doReq(t, context.Background(), srv, http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || len(got) != 8 {
		t.Fatalf("got %d bytes, err %v", len(got), err)
	}
	if time.Since(start) <= testIdle {
		t.Fatalf("transfer should outlast the idle window")
	}
}

func TestDoWithInactivity_StalledBodyFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	resp, err := doReq(t, context.Background(), srv, http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "no data for") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoWithInactivity_DelayedHeadersFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	_, err := doReq(t, context.Background(), srv, http.MethodGet, nil)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "no data for") {
		t.Fatalf("err = %v", err)
	}
}

type slowReader struct {
	left  int
	pause time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.pause)
	r.left--
	p[0] = 'x'
	return 1, nil
}

func TestDoWithInactivity_SlowRequestBodySucceeds(t *testing.T) {
	const total = 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if len(got) != total {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := doReq(t, context.Background(), srv, http.MethodPut, &slowReader{left: total, pause: testIdle / 4})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if time.Since(start) <= testIdle {
		t.Fatalf("upload should outlast the idle window")
	}
}

func TestDoWithInactivity_CallerCancelIsNotInactivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(testIdle/10, cancel)
	_, err := doReq(t, ctx, srv, http.MethodGet, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "no data for") {
		t.Fatalf("caller cancel reported as inactivity: %v", err)
	}
}
