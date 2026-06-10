package render

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

// CollaboraConvert converts a document (any format LibreOffice/Collabora
// accepts) to the requested output extension by calling the Collabora Online
// convert-to endpoint.
//
// docsEditorURL is the base URL of the convert-to endpoint, e.g.:
//
//	"http://127.78.0.6:20001/services/docs/editor/cool/convert-to"
//
// The full request URL is: {docsEditorURL}/{outputExtension}?lang={langTag}
//
// outputExtension is typically "pdf" for document-preview and "png" for
// document-thumbnail. The Python service sanitizes jpeg/JPEG/png/PNG → "png"
// before calling LibreOffice; callers should do the same.
//
// On any error (HTTP, timeout, connection): returns (nil, error).
// The caller is responsible for mapping this to an appropriate HTTP status.
//
// Retry behaviour: up to 2 retries with exponential backoff (0.5s, 1s) on
// transient HTTP 5xx errors and connection errors. The total time including
// retries is bounded by ctx.
func CollaboraConvert(
	ctx context.Context,
	data []byte,
	langTag string,
	docsEditorURL string,
	timeout time.Duration,
) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty document data")
	}

	const maxRetries = 2
	var lastErr error
	backoff := 500 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry, respecting context cancellation.
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled before retry: %w", ctx.Err())
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		out, err := collaboraConvertOnce(ctx, data, langTag, docsEditorURL, timeout)
		if err == nil {
			return out, nil
		}
		lastErr = err

		// Only retry on errors that are likely transient (5xx, connection).
		// 4xx errors (bad input) are permanent — no point retrying.
		if isPermanentErr(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("collabora convert failed after %d attempts: %w", maxRetries+1, lastErr)
}

// collaboraConvertOnce performs a single attempt at the Collabora conversion.
func collaboraConvertOnce(
	ctx context.Context,
	data []byte,
	langTag string,
	docsEditorURL string,
	timeout time.Duration,
) ([]byte, error) {
	// Build multipart body with a single "files" field.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Python: field name "files", filename "docs-editor-file".
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="files"; filename="docs-editor-file"`)
	h.Set("Content-Type", "application/octet-stream")
	fw, err := mw.CreatePart(h)
	if err != nil {
		return nil, &permanentError{fmt.Errorf("building multipart: %w", err)}
	}
	if _, err := fw.Write(data); err != nil {
		return nil, &permanentError{fmt.Errorf("writing multipart body: %w", err)}
	}
	if err := mw.Close(); err != nil {
		return nil, &permanentError{fmt.Errorf("closing multipart writer: %w", err)}
	}

	// Build URL: {docsEditorURL}?lang={langTag}
	// Note: the Python service appends the output extension to docsEditorURL
	// (i.e. docsEditorURL already includes "cool/convert-to").
	// Callers pass the full convert-to URL including the extension. But the
	// spec says the URL is: {FULL_CONVERT_ADDRESS}/{output_extension}?lang=…
	// We expect docsEditorURL to already be the full path including extension.
	reqURL := docsEditorURL
	if langTag != "" {
		reqURL += "?lang=" + langTag
	}

	// Build HTTP request.
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, &body)
	if err != nil {
		return nil, &permanentError{fmt.Errorf("building request: %w", err)}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		// Transport-level error — transient, worth retrying.
		return nil, fmt.Errorf("HTTP request to collabora: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// 4xx — permanent error (bad input, wrong URL, etc.)
		return nil, &permanentError{fmt.Errorf("collabora returned HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode >= 500 {
		// 5xx — transient, retry.
		return nil, fmt.Errorf("collabora returned HTTP %d (transient)", resp.StatusCode)
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

// permanentError wraps errors that should not be retried.
type permanentError struct{ cause error }

func (e *permanentError) Error() string { return e.cause.Error() }
func (e *permanentError) Unwrap() error { return e.cause }

// isPermanentErr returns true for errors that should not be retried.
// It walks the error chain looking for *permanentError.
func isPermanentErr(err error) bool {
	type unwrapper interface{ Unwrap() error }
	for e := err; e != nil; {
		if _, ok := e.(*permanentError); ok {
			return true
		}
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return false
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
