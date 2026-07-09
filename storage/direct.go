// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DirectClient is the CE implementation of Client.
//
// It talks directly to the carbonio-storages HTTP API:
//
//	GET    {storageBaseURL}/{downloadAPI}?node={fileID}&version={version}&type={serviceType}
//	PUT    {storageBaseURL}/{uploadAPI}?node={nodeID}&version={version}&type={serviceType}    (multipart/form-data, part name "file", empty filename)
//	DELETE {storageBaseURL}/{deleteAPI}?node={nodeID}&version={version}&type={serviceType}
//
// ownerID is ignored — CE has no per-owner routing.
//
// Source: carbonio-storages-ce-java-sdk/storages-ce-sdk/src/main/java/com/zextras/storages/internal/retrofitinterface/Filestore.java
// The upload accepts a caller-supplied node id in the query param "node"; the
// response carries only digest/size (no server-assigned id). StoreData echoes
// the caller-supplied nodeID on success, matching the PowerStore contract.
type DirectClient struct {
	downloadURL string // pre-built base URL including the API path, e.g. "http://127.78.0.6:20000/download"
	uploadURL   string // e.g. "http://127.78.0.6:20000/upload"
	deleteURL   string // e.g. "http://127.78.0.6:20000/delete"
	// httpClient is used for the buffered operations: RetrieveData, StoreData,
	// and Delete. It carries fetchTimeout as a hard wall-clock cap covering the
	// full request lifecycle (connect + TLS + write + read-entire-body).
	httpClient *http.Client
	// streamClient is used exclusively by RetrieveDataStreaming. It has
	// Timeout:0 so the overall response-body read is never capped by a
	// wall-clock timer — a large video download is not aborted mid-stream.
	// The caller's ctx (and, in the video worker, the idle-read watchdog) is
	// the actual stall protection for streaming downloads.
	streamClient *http.Client
}

// NewDirectClient constructs a DirectClient.
//
//   - storageBaseURL — scheme://host:port, e.g. "http://127.78.0.6:20000"
//   - downloadAPI    — path segment for GET download, e.g. "download"
//   - uploadAPI      — path segment for POST upload, e.g. "upload"
//   - deleteAPI      — path segment for DELETE, e.g. "delete"
//   - fetchTimeout   — TOTAL wall-clock timeout applied to the buffered
//     operations (RetrieveData, StoreData, Delete) via http.Client.Timeout.
//     This bounds connect + TLS handshake + request write + full body read
//     as one deadline — mirroring the Advanced edition's PowerStoreClient
//     (internal/powerstore/powerstore.go), which uses the same value as its
//     httpClient.Timeout.
//
// RetrieveDataStreaming does NOT use fetchTimeout: it is served by a separate
// streamClient with Timeout:0, so a legitimate large video download is never
// aborted by this wall-clock cap. Both clients share one empty *http.Transport
// (no DialContext/TLSHandshakeTimeout override) — there is no separate dial
// timeout because fetchTimeout on httpClient already bounds connect+TLS+body
// for the buffered path, and the streaming path is deliberately unbounded.
func NewDirectClient(storageBaseURL, downloadAPI, uploadAPI, deleteAPI string, fetchTimeout time.Duration) *DirectClient {
	sharedTransport := &http.Transport{}
	return &DirectClient{
		downloadURL: storageBaseURL + "/" + downloadAPI,
		uploadURL:   storageBaseURL + "/" + uploadAPI,
		deleteURL:   storageBaseURL + "/" + deleteAPI,
		httpClient: &http.Client{
			Timeout:   fetchTimeout,
			Transport: sharedTransport,
		},
		streamClient: &http.Client{
			Timeout:   0,
			Transport: sharedTransport,
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
	resp, err := c.httpClient.Do(req)
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
//
// It is served by streamClient (Timeout:0), NOT httpClient — the buffered
// fetchTimeout wall-clock cap does not apply here, so a legitimate large
// video download is never aborted mid-stream. Cancelling ctx still aborts the
// in-flight transfer; the video worker's idle-read watchdog uses this to
// bound stalled downloads at the byte-progress level instead.
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
	resp, err := c.streamClient.Do(req)
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

// StoreData uploads data to carbonio-storages via:
//
//	PUT {uploadURL}?node={nodeID}&version={version}&type={serviceType}
//	Content-Type: multipart/form-data; part name "file", empty filename
//
// The CALLER supplies nodeID; carbonio-storages does NOT mint it (confirmed in
// StoragesClientImp.uploadPut — node is passed as a query param and the
// response only returns digest/size, not a server-assigned id).
// On success, StoreData echoes nodeID back.
//
// ownerID is ignored in CE (no per-owner routing).
//
// Source: carbonio-storages-ce-java-sdk/storages-ce-sdk/src/main/java/com/zextras/storages/internal/retrofitinterface/Filestore.java (line 29-31)
// and StoragesClientImp.buildPart: CreateFormData("file", "", ...) — filename is empty.
func (c *DirectClient) StoreData(
	ctx context.Context,
	nodeID string,
	version int,
	serviceType string,
	_ string, // ownerID ignored in CE
	data []byte,
) (string, error) {
	reqURL, err := buildURL(c.uploadURL, nodeID, version, serviceType)
	if err != nil {
		return "", fmt.Errorf("%w: building upload URL: %v", ErrUnavailable, err)
	}

	// Build multipart body: part name "file", empty filename — mirrors
	// StoragesClientImp.buildPart which calls CreateFormData("file", "", ...).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "")
	if err != nil {
		return "", fmt.Errorf("%w: building multipart: %v", ErrUnavailable, err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("%w: writing multipart: %v", ErrUnavailable, err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("%w: closing multipart writer: %v", ErrUnavailable, err)
	}

	// SDK uses uploadPut (@PUT "upload") — use PUT, not POST.
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, &body)
	if err != nil {
		return "", fmt.Errorf("%w: creating upload request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.Debug("storage: upload request failed",
			"method", http.MethodPut,
			"path", req.URL.Path,
			"duration_ms", durationMs,
			"err", err,
		)
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	slog.Debug("storage: upload response",
		"method", http.MethodPut,
		"path", req.URL.Path,
		"status", resp.StatusCode,
		"duration_ms", durationMs,
	)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrNotFound
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		// Caller-supplied nodeID is echoed back (no server-minted id in response).
		return nodeID, nil
	default:
		return "", fmt.Errorf("%w: storage upload returned HTTP %d", ErrUnavailable, resp.StatusCode)
	}
}

// Delete removes a node from carbonio-storages via:
//
//	DELETE {deleteURL}?node={nodeID}&version={version}&type={serviceType}
//
// 2xx responses (including 200 soft-delete) are treated as success.
// 404 means the node is already gone — treated as success for cleanup purposes.
//
// ownerID is ignored in CE (no per-owner routing).
//
// Source: carbonio-storages-ce-java-sdk/storages-ce-sdk/src/main/java/com/zextras/storages/internal/retrofitinterface/Filestore.java (line 23-24)
func (c *DirectClient) Delete(
	ctx context.Context,
	nodeID string,
	version int,
	serviceType string,
	_ string, // ownerID ignored in CE
) error {
	reqURL, err := buildURL(c.deleteURL, nodeID, version, serviceType)
	if err != nil {
		return fmt.Errorf("%w: building delete URL: %v", ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("%w: creating delete request: %v", ErrUnavailable, err)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.Debug("storage: delete request failed",
			"method", http.MethodDelete,
			"path", req.URL.Path,
			"duration_ms", durationMs,
			"err", err,
		)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	slog.Debug("storage: delete response",
		"method", http.MethodDelete,
		"path", req.URL.Path,
		"status", resp.StatusCode,
		"duration_ms", durationMs,
	)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Node already gone — acceptable for cleanup purposes.
		return nil
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		return nil
	default:
		return fmt.Errorf("%w: storage delete returned HTTP %d", ErrUnavailable, resp.StatusCode)
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
