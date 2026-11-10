// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/storage"
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

	out, err := CollaboraConvert(context.Background(), []byte("doc-\x00\xff-bytes"), "en-US", srv.URL+"/pdf", 5*time.Second)
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

// TestCollaboraConvert_RetriesOn503 verifies a 503 is retried: two failures
// then success on the third attempt.
func TestCollaboraConvert_RetriesOn503(t *testing.T) {
	setFastBackoff(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
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

// TestCollaboraConvert_ExhaustsRetriesOn503 verifies the loop gives up after
// 3 attempts when every attempt is a 503, returning the "after N attempts"
// wrapped error.
func TestCollaboraConvert_ExhaustsRetriesOn503(t *testing.T) {
	setFastBackoff(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
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
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
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
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("empty body must not retry; hits=%d", got)
	}
}

type cancelOn503 struct{ cancel context.CancelFunc }

func (c cancelOn503) RoundTrip(*http.Request) (*http.Response, error) {
	c.cancel()
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: http.Header{}}, nil
}

// TestCollaboraConvert_ContextCancelBeforeRetry cancels the context during the
// first attempt so the retry wait observes ctx.Done() immediately.
func TestCollaboraConvert_ContextCancelBeforeRetry(t *testing.T) {
	prev := collaboraInitialBackoff
	collaboraInitialBackoff = time.Hour
	t.Cleanup(func() { collaboraInitialBackoff = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := collaboraHTTP.Swap(&http.Client{Transport: cancelOn503{cancel}})
	t.Cleanup(func() { collaboraHTTP.Store(old) })

	_, err := CollaboraConvert(ctx, []byte("x"), "", "http://collabora.invalid/pdf", time.Second)
	if err == nil || !strings.Contains(err.Error(), "context cancelled before retry") {
		t.Errorf("err=%v, want context cancelled before retry", err)
	}
}

type poisonableConn struct {
	net.Conn
	poisoned atomic.Bool
}

func (c *poisonableConn) Write(p []byte) (int, error) {
	if c.poisoned.Load() {
		return 0, syscall.EPIPE
	}
	return c.Conn.Write(p)
}

func TestCollaboraConvert_ResendsOnStaleKeepAliveConnection(t *testing.T) {
	var hits, dials atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer srv.Close()

	var first *poisonableConn
	tr := storage.NewTransport()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		pc := &poisonableConn{Conn: c}
		if first == nil {
			first = pc
		}
		return pc, nil
	}
	old := collaboraHTTP.Swap(&http.Client{Transport: tr})
	t.Cleanup(func() { collaboraHTTP.Store(old) })

	if _, err := CollaboraConvert(context.Background(), []byte("one"), "", srv.URL+"/pdf", time.Second); err != nil {
		t.Fatal(err)
	}
	first.poisoned.Store(true)
	if _, err := CollaboraConvert(context.Background(), []byte("two"), "", srv.URL+"/pdf", time.Second); err != nil {
		t.Fatalf("second call on a stale pooled connection: %v", err)
	}
	if hits.Load() != 2 || dials.Load() != 2 {
		t.Fatalf("hits=%d dials=%d, want 2 and 2", hits.Load(), dials.Load())
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

// TestIsRetryableErr_WalksChain verifies isRetryableErr finds *retryableError
// through %w wrapping and rejects a plain error.
func TestIsRetryableErr_WalksChain(t *testing.T) {
	re := &retryableError{cause: fmt.Errorf("inner")}
	if re.Error() != "inner" || re.Unwrap() == nil {
		t.Error("retryableError must expose its cause")
	}
	if !isRetryableErr(fmt.Errorf("wrap: %w", re)) {
		t.Error("isRetryableErr must find *retryableError through wrapping")
	}
	if isRetryableErr(fmt.Errorf("plain")) || isRetryableErr(nil) {
		t.Error("plain/nil error must not be retryable")
	}
}

func TestCollaboraConvert_NoRetryOnOtherFailures(t *testing.T) {
	setFastBackoff(t)
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout, http.StatusBadRequest, http.StatusNotFound} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(status)
		}))
		_, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", time.Second)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: want error", status)
		}
		if got := atomic.LoadInt32(&hits); got != 1 {
			t.Errorf("status %d: hits=%d, want 1", status, got)
		}
	}
}

func TestCollaboraConvert_InactivityNotRetried(t *testing.T) {
	setFastBackoff(t)
	var hits int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release
	}))
	defer srv.Close()
	defer close(release)

	_, err := CollaboraConvert(context.Background(), []byte("x"), "", srv.URL+"/pdf", 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no data for") {
		t.Fatalf("err=%v, want inactivity error", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("hits=%d, want 1", got)
	}
}

func TestCollaboraConvert_WireBodyMatchesMultipartWriter(t *testing.T) {
	data := bytes.Repeat([]byte("doc-\x00\xff-bytes"), 1000)
	var gotBody []byte
	var gotCT string
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		gotLen = r.ContentLength
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer srv.Close()

	if _, err := CollaboraConvert(context.Background(), data, "", srv.URL+"/pdf", time.Second); err != nil {
		t.Fatal(err)
	}

	_, params, err := mime.ParseMediaType(gotCT)
	if err != nil || !strings.HasPrefix(gotCT, "multipart/form-data; boundary=") {
		t.Fatalf("content-type %q err %v", gotCT, err)
	}
	var want bytes.Buffer
	mw := multipart.NewWriter(&want)
	if err := mw.SetBoundary(params["boundary"]); err != nil {
		t.Fatal(err)
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="files"; filename="docs-editor-file"`)
	h.Set("Content-Type", "application/octet-stream")
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(data)
	_ = mw.Close()

	if !bytes.Equal(gotBody, want.Bytes()) {
		t.Fatalf("wire body differs from multipart.Writer output (got %d bytes, want %d)", len(gotBody), want.Len())
	}
	if gotLen != int64(want.Len()) {
		t.Fatalf("Content-Length = %d, want %d", gotLen, want.Len())
	}
}

func TestCollaboraConvert_LangIsEscaped(t *testing.T) {
	var langs []string
	var extra string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		langs = r.URL.Query()["lang"]
		extra = r.URL.Query().Get("x")
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer srv.Close()

	if _, err := CollaboraConvert(context.Background(), []byte("x"), "en&x=1", srv.URL+"/pdf", time.Second); err != nil {
		t.Fatal(err)
	}
	if len(langs) != 1 || langs[0] != "en&x=1" || extra != "" {
		t.Fatalf("lang=%v x=%q", langs, extra)
	}
}
