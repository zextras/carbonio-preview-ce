// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// setFastBackoff shortens the CollaboraConvert retry backoff for the duration
// of a test so the retry loop does not sleep real seconds. The original value
// is restored via t.Cleanup. This only touches the test seam variable; the
// production default (500ms) is unchanged.
func setFastBackoff(t *testing.T) {
	t.Helper()
	prev := collaboraInitialBackoff
	collaboraInitialBackoff = time.Millisecond
	t.Cleanup(func() { collaboraInitialBackoff = prev })
}

// TestCollaboraConvert_Success drives the happy path against an httptest
// server: a multipart POST that returns PDF bytes.
func TestCollaboraConvert_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("missing multipart content-type: %q", r.Header.Get("Content-Type"))
		}
		// The multipart body must carry the single "files" field with the
		// expected filename so this faithfully mirrors what Collabora receives.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		if r.MultipartForm == nil || len(r.MultipartForm.File["files"]) != 1 {
			t.Errorf("expected one \"files\" part, got %+v", r.MultipartForm)
		} else if fn := r.MultipartForm.File["files"][0].Filename; fn != "docs-editor-file" {
			t.Errorf("filename=%q, want docs-editor-file", fn)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-1.4 minimal"))
	}))
	defer srv.Close()

	out, err := CollaboraConvert(context.Background(), []byte("doc-bytes"), "en-US", srv.URL+"/pdf", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "%PDF-1.4 minimal" {
		t.Errorf("out=%q", out)
	}
}

// TestCollaboraConvert_SuccessNoLang verifies the no-lang URL branch (the
// "?lang=" suffix is omitted when langTag is empty).
func TestCollaboraConvert_SuccessNoLang(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer srv.Close()

	out, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "%PDF" {
		t.Errorf("out=%q", out)
	}
}

// TestCollaboraConvert_EmptyInput hits the empty-data guard before any HTTP.
func TestCollaboraConvert_EmptyInput(t *testing.T) {
	_, err := CollaboraConvert(context.Background(), nil, "en-US", "http://unused/pdf", time.Second)
	if err == nil {
		t.Fatal("want error for empty data")
	}
	if !strings.Contains(err.Error(), "empty document data") {
		t.Errorf("err=%v, want empty document data", err)
	}
}

// TestCollaboraConvert_PermanentOn4xx verifies a 4xx is classified permanent
// (no retry): the server must be hit exactly once.
func TestCollaboraConvert_PermanentOn4xx(t *testing.T) {
	setFastBackoff(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest) // 4xx → permanent, no retry
	}))
	defer srv.Close()

	_, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", time.Second)
	if err == nil {
		t.Fatal("want error on 4xx")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("4xx must not retry; server hits=%d, want 1", got)
	}
}

// TestCollaboraConvert_RetriesOn5xx verifies a 5xx is classified transient and
// retried: two failures then success on the third attempt (maxRetries=2).
func TestCollaboraConvert_RetriesOn5xx(t *testing.T) {
	setFastBackoff(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer srv.Close()

	out, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", time.Second)
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if string(out) != "%PDF" {
		t.Errorf("out=%q", out)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("server hits=%d, want 3 (2 retries)", got)
	}
}

// TestCollaboraConvert_ExhaustsRetriesOn5xx verifies the loop gives up after
// maxRetries+1 attempts when every attempt is a 5xx, returning the
// "after N attempts" wrapped error.
func TestCollaboraConvert_ExhaustsRetriesOn5xx(t *testing.T) {
	setFastBackoff(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError) // always 5xx
	}))
	defer srv.Close()

	_, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", time.Second)
	if err == nil {
		t.Fatal("want error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Errorf("err=%v, want 'after 3 attempts'", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("server hits=%d, want 3 (initial + 2 retries)", got)
	}
}

// TestCollaboraConvert_EmptyBodyIsError verifies a 200 with an empty body is an
// error (collaboraConvertOnce rejects a zero-length response).
func TestCollaboraConvert_EmptyBodyIsError(t *testing.T) {
	// An empty 200 body is a plain (non-permanent) error, so the loop retries
	// it twice before giving up — shorten the backoff so the test is fast.
	setFastBackoff(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 but no body
	}))
	defer srv.Close()

	_, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", time.Second)
	if err == nil {
		t.Fatal("want error on empty 200 body")
	}
	if !strings.Contains(err.Error(), "empty body") {
		t.Errorf("err=%v, want empty body", err)
	}
}

// TestCollaboraConvert_ContextCancelBeforeRetry cancels the context before the
// first retry so the retry select fires on ctx.Done() rather than time.After.
func TestCollaboraConvert_ContextCancelBeforeRetry(t *testing.T) {
	// Use a non-trivial backoff so ctx.Done() reliably wins the select race
	// against time.After (the ctx is already cancelled when we enter the loop).
	prev := collaboraInitialBackoff
	collaboraInitialBackoff = time.Hour
	t.Cleanup(func() { collaboraInitialBackoff = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // always 5xx → would retry
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately; first 5xx → enters retry select → ctx.Done() fires
	_, err := CollaboraConvert(ctx, []byte("x"), "", srv.URL+"/pdf", time.Second)
	if err == nil {
		t.Fatal("want context-cancel error")
	}
	if !strings.Contains(err.Error(), "context cancelled before retry") {
		t.Errorf("err=%v, want context cancelled before retry", err)
	}
}

// TestCollaboraConvert_TransportErrorRetries verifies a transport-level failure
// (unreachable server) is transient: the loop retries and eventually returns
// the "after N attempts" error. This covers the client.Do error arm.
func TestCollaboraConvert_TransportErrorRetries(t *testing.T) {
	setFastBackoff(t)
	// A server that is started then immediately closed → connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/pdf"
	srv.Close() // now connections are refused

	_, err := CollaboraConvert(context.Background(), []byte("x"), "", url, time.Second)
	if err == nil {
		t.Fatal("want transport error after retries")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Errorf("err=%v, want 'after 3 attempts' (transport error is transient)", err)
	}
}

// TestSanitizeOutputExtension covers the jpeg/png/gif→png mapping and pass-through.
func TestSanitizeOutputExtension(t *testing.T) {
	cases := map[string]string{
		"jpeg": "png", "JPEG": "png", "jpg": "png", "JPG": "png",
		"png": "png", "PNG": "png", "gif": "png", "GIF": "png",
		"pdf": "pdf", "docx": "docx", "": "",
	}
	for in, want := range cases {
		if got := SanitizeOutputExtension(in); got != want {
			t.Errorf("SanitizeOutputExtension(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestIsPermanentErr_WalksChain verifies isPermanentErr finds *permanentError
// through %w wrapping and rejects a plain error. This also exercises the
// permanentError Error()/Unwrap() methods.
func TestIsPermanentErr_WalksChain(t *testing.T) {
	pe := &permanentError{cause: fmt.Errorf("inner")}
	if pe.Error() != "inner" {
		t.Errorf("permanentError.Error()=%q, want inner", pe.Error())
	}
	if pe.Unwrap() == nil {
		t.Error("permanentError.Unwrap() must return the cause")
	}
	if !isPermanentErr(fmt.Errorf("wrap: %w", pe)) {
		t.Error("isPermanentErr must find *permanentError through wrapping")
	}
	if isPermanentErr(fmt.Errorf("plain transient")) {
		t.Error("plain error must not be permanent")
	}
	if isPermanentErr(nil) {
		t.Error("nil error must not be permanent")
	}
}
