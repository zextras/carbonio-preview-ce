package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DirectClient is the CE implementation of Client.
//
// It talks directly to the carbonio-storages HTTP API using the download
// endpoint:
//
//	GET {storageBaseURL}/{downloadAPI}?node={fileID}&version={version}&type={serviceType}
//
// ownerID is ignored — CE has no per-owner routing.
type DirectClient struct {
	downloadURL string // pre-built base URL including the API path, e.g. "http://127.78.0.6:20000/download"
	http        *http.Client
}

// NewDirectClient constructs a DirectClient.
//
//   - storageBaseURL — scheme://host:port, e.g. "http://127.78.0.6:20000"
//   - downloadAPI    — path segment, e.g. "download"
//   - dialTimeout    — bounds TCP connection establishment and TLS handshake ONLY.
//     There is intentionally NO http.Client.Timeout (no total-request cap here):
//     the operation deadline is the per-request ctx deadline set by the handler,
//     which governs the full lifecycle (download + ffmpeg + render) via a single
//     context.WithTimeout. The body read is cancelled automatically when that
//     ctx fires, because every request is built with http.NewRequestWithContext.
func NewDirectClient(storageBaseURL, downloadAPI string, dialTimeout time.Duration) *DirectClient {
	return &DirectClient{
		downloadURL: storageBaseURL + "/" + downloadAPI,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout: dialTimeout,
				}).DialContext,
				TLSHandshakeTimeout: dialTimeout,
			},
		},
	}
}

// RetrieveData fetches a file from storage.
//
// Mapping from Python retrieve_data / check_for_storage_response_error:
//
//	200–3xx   → (content, nil)
//	404       → (nil, ErrNotFound)
//	any other → (nil, ErrUnavailable)   — includes timeouts and connection failures
func (c *DirectClient) RetrieveData(
	ctx context.Context,
	fileID string,
	version int,
	serviceType string,
	_ string, // ownerID ignored in CE
) (Blob, error) {
	reqURL, err := buildURL(c.downloadURL, fileID, version, serviceType)
	if err != nil {
		return nil, fmt.Errorf("%w: building URL: %v", ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: creating request: %v", ErrUnavailable, err)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		// Any transport-level error (timeout, DNS, connection refused, etc.)
		// maps to ErrUnavailable, mirroring the Python Nothing path.
		slog.Debug("storage: request failed",
			"method", http.MethodGet,
			"path", req.URL.Path, // no query string — avoid logging file IDs at debug
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	slog.Debug("storage: request",
		"method", http.MethodGet,
		"path", req.URL.Path, // no query string — avoid logging file IDs at debug
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Python: Maybe.from_value(resp) where status==404 → later becomes ErrNotFound
		return nil, ErrNotFound

	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		// Success path.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("%w: reading body: %v", ErrUnavailable, err)
		}
		return body, nil

	default:
		// 4xx/5xx non-404 → ErrUnavailable.
		// Python: for non-404 4xx/5xx the check_for_storage_response_error
		// returns the GENERIC_ERROR_WITH_STORAGE body.  We encode that as
		// ErrUnavailable; the HTTP handler layer constructs the correct body.
		return nil, fmt.Errorf("%w: storage returned HTTP %d", ErrUnavailable, resp.StatusCode)
	}
}

// RetrieveDataStreaming is the streaming variant of RetrieveData. It performs
// the identical GET but returns resp.Body so the caller can read a prefix and
// close early. The body MUST be closed by the caller.
func (c *DirectClient) RetrieveDataStreaming(
	ctx context.Context,
	fileID string,
	version int,
	serviceType string,
	_ string, // ownerID ignored in CE
) (io.ReadCloser, error) {
	reqURL, err := buildURL(c.downloadURL, fileID, version, serviceType)
	if err != nil {
		return nil, fmt.Errorf("%w: building URL: %v", ErrUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: creating request: %v", ErrUnavailable, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrNotFound
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		return resp.Body, nil
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: storage returned HTTP %d", ErrUnavailable, resp.StatusCode)
	}
}

// buildURL assembles the full download URL with query parameters.
func buildURL(base, fileID string, version int, serviceType string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("node", fileID)
	q.Set("version", strconv.Itoa(version))
	q.Set("type", serviceType)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
