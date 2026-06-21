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

// newTestClient creates a DirectClient pointing at the given test server URL.
func newTestClient(serverURL string) *DirectClient {
	return NewDirectClient(serverURL, "download", 5*time.Second)
}

// TestRetrieveData_HappyPath verifies that a 200 response returns the body
// and no error.  Also checks that the query parameters are correctly
// constructed.
func TestRetrieveData_HappyPath(t *testing.T) {
	const wantBody = "fake-image-bytes"
	const wantNode = "123e4567-e89b-12d3-a456-426614174000"
	const wantVersion = "3"
	const wantType = "files"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("node") != wantNode {
			t.Errorf("node param: got %q, want %q", q.Get("node"), wantNode)
		}
		if q.Get("version") != wantVersion {
			t.Errorf("version param: got %q, want %q", q.Get("version"), wantVersion)
		}
		if q.Get("type") != wantType {
			t.Errorf("type param: got %q, want %q", q.Get("type"), wantType)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(wantBody)) //nolint:errcheck
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	data, err := client.RetrieveData(context.Background(), wantNode, 3, wantType, "")
	if err != nil {
		t.Fatalf("RetrieveData error: %v", err)
	}
	if string(data) != wantBody {
		t.Errorf("body: got %q, want %q", string(data), wantBody)
	}
}

// TestRetrieveData_404 verifies that a storage HTTP 404 maps to ErrNotFound.
// Python: Maybe.from_value(resp) where status==404 → check_for_storage_response_error
// returns ErrNotFound.  Non-404 errors are NOT ErrNotFound.
func TestRetrieveData_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	_, err := client.RetrieveData(context.Background(), "some-id", 1, "files", "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestRetrieveData_5xx verifies that a storage HTTP 500 maps to ErrUnavailable
// and NOT ErrNotFound.  Python: non-404 4xx/5xx returns Nothing → ErrUnavailable.
func TestRetrieveData_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	_, err := client.RetrieveData(context.Background(), "some-id", 1, "files", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("5xx should NOT map to ErrNotFound")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("expected ErrUnavailable, got %v", err)
	}
}

// TestRetrieveData_4xx_non404 verifies that a 400 or other 4xx (non-404)
// maps to ErrUnavailable, not ErrNotFound.
func TestRetrieveData_4xx_non404(t *testing.T) {
	for _, code := range []int{400, 403, 409, 422, 429} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			client := newTestClient(srv.URL)
			_, err := client.RetrieveData(context.Background(), "some-id", 1, "files", "")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if errors.Is(err, ErrNotFound) {
				t.Errorf("HTTP %d should NOT map to ErrNotFound", code)
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Errorf("HTTP %d: expected ErrUnavailable, got %v", code, err)
			}
		})
	}
}

// TestRetrieveData_ConnectionRefused verifies that a connection-level failure
// (server unreachable) maps to ErrUnavailable.
// Python: httpx.ConnectTimeout or httpx.RequestError → Nothing → ErrUnavailable.
func TestRetrieveData_ConnectionRefused(t *testing.T) {
	// Use a closed server to simulate connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close immediately

	client := newTestClient(srv.URL)
	_, err := client.RetrieveData(context.Background(), "some-id", 1, "files", "")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("expected ErrUnavailable on connection error, got %v", err)
	}
}

// TestRetrieveData_CtxTimeout verifies that a request whose context deadline
// fires while the server is hanging maps to ErrUnavailable.
//
// The storage client carries no total-request http.Client.Timeout; the
// per-request deadline is provided by the caller's context (mirroring the
// single-clock design where the video handler's context.WithTimeout governs
// the full operation). A very short context deadline keeps the test fast.
func TestRetrieveData_CtxTimeout(t *testing.T) {
	// Server that never responds (hangs until ctx is cancelled).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := NewDirectClient(srv.URL, "download", 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.RetrieveData(ctx, "some-id", 1, "files", "")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("expected ErrUnavailable on ctx timeout, got %v", err)
	}
}

// TestRetrieveData_CorrectHTTPMethod verifies that RetrieveData sends a GET
// request (not POST, PUT, etc.).
func TestRetrieveData_CorrectHTTPMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	_, err := client.RetrieveData(context.Background(), "abc", 0, "chats", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRetrieveData_ChatsServiceType verifies that service_type=chats is passed
// correctly as the "type" query parameter.
func TestRetrieveData_ChatsServiceType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("type")
		if got != "chats" {
			t.Errorf("type param: got %q, want %q", got, "chats")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data")) //nolint:errcheck
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	_, err := client.RetrieveData(context.Background(), "abc", 1, "chats", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildURL verifies that buildURL assembles the correct query string.
func TestBuildURL(t *testing.T) {
	tests := []struct {
		name        string
		base        string
		fileID      string
		version     int
		serviceType string
		wantContain []string
	}{
		{
			name:        "basic files",
			base:        "http://127.78.0.6:20000/download",
			fileID:      "abc-123",
			version:     5,
			serviceType: "files",
			wantContain: []string{"node=abc-123", "version=5", "type=files"},
		},
		{
			name:        "chats version 0",
			base:        "http://localhost:8080/get",
			fileID:      "xyz",
			version:     0,
			serviceType: "chats",
			wantContain: []string{"node=xyz", "version=0", "type=chats"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildURL(tt.base, tt.fileID, tt.version, tt.serviceType)
			if err != nil {
				t.Fatalf("buildURL error: %v", err)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("URL %q does not contain %q", got, want)
				}
			}
		})
	}
}

// errReader is an io.ReadCloser whose Read always fails, used to exercise the
// body-read error arm of RetrieveData.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("simulated body read error") }
func (errReader) Close() error             { return nil }

// errBodyRoundTripper returns a 200 response whose Body errors on Read.
type errBodyRoundTripper struct{}

func (errBodyRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       errReader{},
		Header:     make(http.Header),
	}, nil
}

// TestRetrieveData_BodyReadError verifies that a failure while reading the
// response body on the success path maps to ErrUnavailable.
func TestRetrieveData_BodyReadError(t *testing.T) {
	client := &DirectClient{
		downloadURL: "http://127.0.0.1:20000/download",
		http:        &http.Client{Transport: errBodyRoundTripper{}},
	}
	_, err := client.RetrieveData(context.Background(), "id", 1, "files", "")
	if err == nil {
		t.Fatal("expected error from body read failure, got nil")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("expected ErrUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "reading body") {
		t.Errorf("error = %v, want it to mention reading body", err)
	}
}

// TestBuildURL_ParseError verifies that an unparseable base URL makes buildURL
// return a non-nil error.
func TestBuildURL_ParseError(t *testing.T) {
	_, err := buildURL("http://[::1", "id", 1, "files") // unterminated IPv6 → url.Parse fails
	if err == nil {
		t.Fatal("expected url.Parse error for malformed base, got nil")
	}
}

// TestRetrieveData_BuildURLError verifies that a malformed downloadURL surfaces
// as an ErrUnavailable-wrapped error from RetrieveData (the buildURL error arm).
func TestRetrieveData_BuildURLError(t *testing.T) {
	client := &DirectClient{
		downloadURL: "http://[::1", // unparseable
		http:        &http.Client{},
	}
	_, err := client.RetrieveData(context.Background(), "id", 1, "files", "")
	if err == nil {
		t.Fatal("expected error from malformed downloadURL, got nil")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("expected ErrUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "building URL") {
		t.Errorf("error = %v, want it to mention building URL", err)
	}
}

var _ io.ReadCloser = errReader{}

func TestRetrieveDataStreaming_HappyPath(t *testing.T) {
	want := []byte("the-blob-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	client := NewDirectClient(srv.URL, "download", 5*time.Second)
	rc, err := client.RetrieveDataStreaming(context.Background(), "node-1", 1, "files", "")
	if err != nil {
		t.Fatalf("RetrieveDataStreaming: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRetrieveDataStreaming_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client := NewDirectClient(srv.URL, "download", 5*time.Second)
	_, err := client.RetrieveDataStreaming(context.Background(), "x", 1, "files", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestRetrieveData_2xxVariants verifies that non-200 success codes (201, 206,
// 302) are also treated as successful.
func TestRetrieveData_2xxVariants(t *testing.T) {
	for _, code := range []int{200, 201, 206} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				w.Write([]byte("payload")) //nolint:errcheck
			}))
			defer srv.Close()

			client := newTestClient(srv.URL)
			data, err := client.RetrieveData(context.Background(), "id", 1, "files", "")
			if err != nil {
				t.Errorf("HTTP %d: expected no error, got %v", code, err)
			}
			if len(data) == 0 {
				t.Errorf("HTTP %d: expected non-empty body", code)
			}
		})
	}
}

// TestDirectClient_StoreData_NotSupported verifies that the CE DirectClient
// rejects writes: generation is Advanced-only.
func TestDirectClient_StoreData_NotSupported(t *testing.T) {
	c := NewDirectClient("http://127.0.0.1:20000", "download", 0)
	_, err := c.StoreData(context.Background(), "11111111-1111-1111-1111-111111111111", 0, "chats", "owner", []byte("jpg"))
	if !errors.Is(err, ErrStoreNotSupported) {
		t.Fatalf("expected ErrStoreNotSupported, got %v", err)
	}
}
