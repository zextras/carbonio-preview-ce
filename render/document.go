// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

var collaboraHTTP atomic.Pointer[http.Client]

func init() {
	collaboraHTTP.Store(&http.Client{Transport: storage.NewTransport()})
}

// collaboraInitialBackoff is the delay before the first retry in
// CollaboraConvert (it doubles after each retry: 0.5s, 1s). It is a package
// variable purely so tests can shorten it to keep the retry tests fast; in
// production it is never reassigned. Do not mutate it from non-test code.
var collaboraInitialBackoff = 500 * time.Millisecond

// CollaboraConvert converts a document (any format LibreOffice/Collabora
// accepts) to the requested output extension by calling the Collabora Online
// convert-to endpoint.
//
// docsEditorURL is the full convert-to URL including the output extension, e.g.:
//
//	"http://127.78.0.6:20001/services/docs/editor/cool/convert-to/pdf"
//
// The request URL is docsEditorURL?lang={langTag}.
//
// On any error (HTTP, inactivity, connection): returns (nil, error).
// The caller is responsible for mapping this to an appropriate HTTP status.
//
// idle is the maximum silence tolerated per attempt: the attempt is cancelled
// when no bytes move (upload, response wait or download) for that long.
//
// Retry behaviour: up to 3 attempts with backoff (0.5s, 1s), only when Collabora
// cannot have started working — a dial failure or HTTP 503. Anything else
// (inactivity expiry, 4xx, 500/502/504, reset after the request was written,
// empty body) is returned immediately. The backoff waits are bounded by ctx.
func CollaboraConvert(
	ctx context.Context,
	data []byte,
	langTag string,
	docsEditorURL string,
	idle time.Duration,
) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty document data")
	}

	const maxAttempts = 3
	req := newCollaboraPayload(data)
	var lastErr error
	backoff := collaboraInitialBackoff

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Wait before retry, respecting context cancellation.
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled before retry: %w", ctx.Err())
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		out, err := collaboraConvertOnce(ctx, req, langTag, docsEditorURL, idle)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isRetryableErr(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("collabora convert failed after %d attempts: %w", maxAttempts, lastErr)
}

type collaboraPayload struct {
	header      []byte
	data        []byte
	trailer     []byte
	contentType string
}

func newCollaboraPayload(data []byte) *collaboraPayload {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Python: field name "files", filename "docs-editor-file".
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="files"; filename="docs-editor-file"`)
	h.Set("Content-Type", "application/octet-stream")
	_, _ = mw.CreatePart(h)
	header := bytes.Clone(buf.Bytes())
	_ = mw.Close()
	return &collaboraPayload{
		header:      header,
		data:        data,
		trailer:     bytes.Clone(buf.Bytes()[len(header):]),
		contentType: mw.FormDataContentType(),
	}
}

func (p *collaboraPayload) length() int64 {
	return int64(len(p.header) + len(p.data) + len(p.trailer))
}

func (p *collaboraPayload) body() io.Reader {
	return io.MultiReader(bytes.NewReader(p.header), bytes.NewReader(p.data), bytes.NewReader(p.trailer))
}

func collaboraConvertOnce(
	ctx context.Context,
	payload *collaboraPayload,
	langTag string,
	docsEditorURL string,
	idle time.Duration,
) ([]byte, error) {
	reqURL := docsEditorURL
	if langTag != "" {
		reqURL += "?lang=" + url.QueryEscape(langTag)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, payload.body())
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.ContentLength = payload.length()
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(payload.body()), nil }
	req.Header.Set("Content-Type", payload.contentType)

	resp, err := storage.DoWithInactivity(collaboraHTTP.Load(), req, idle)
	if err != nil {
		if isDialErr(err) {
			return nil, &retryableError{fmt.Errorf("HTTP request to collabora: %w", err)}
		}
		return nil, fmt.Errorf("HTTP request to collabora: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, &retryableError{fmt.Errorf("collabora returned HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("collabora returned HTTP %d", resp.StatusCode)
	}

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading collabora response: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("collabora returned empty body")
	}

	return out, nil
}

func isDialErr(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// retryableError marks failures where Collabora cannot have started working.
type retryableError struct{ cause error }

func (e *retryableError) Error() string { return e.cause.Error() }
func (e *retryableError) Unwrap() error { return e.cause }

func isRetryableErr(err error) bool {
	var r *retryableError
	return errors.As(err, &r)
}

// SanitizeOutputExtension replicates the Python _sanitize_output_extension
// logic: JPEG/PNG (any case) are replaced with "png" before sending to
// LibreOffice. "pdf" and other extensions are passed through unchanged.
func SanitizeOutputExtension(ext string) string {
	switch ext {
	case "jpeg", "JPEG", "jpg", "JPG", "png", "PNG", "gif", "GIF":
		return "png"
	default:
		return ext
	}
}
