// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package testsupport provides in-process mock servers and fixtures for the
// full-flow (Phase 2) tests of carbonio-preview.
//
// It is a real, importable package (not a _test.go-only helper) so the wire
// contracts encoded here can be shared. Both direct dependencies of the
// preview service are mocked as net/http/httptest.Server instances:
//
//   - StoragesMock — the CE carbonio-storages download contract
//     GET {base}/{downloadAPI}?node=&version=&type=  (see storage/direct.go buildURL)
//   - CollaboraMock — the docs-editor convert contract
//     POST {addr}/pdf?lang=  with a multipart "files" field
//     (see server/document.go convertDocToPDF + render/document.go CollaboraConvert)
//
// NO docker, NO testcontainers: everything runs in-process.
package testsupport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
)

// fixturesDir resolves the testdata directory relative to THIS source file, so
// callers in other packages (e.g. the server package) still find the fixtures
// regardless of their own working directory.
func fixturesDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		// Fallback: relative to CWD. Callers run from their package dir.
		return filepath.Join("testsupport", "testdata")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata")
}

// LoadFixture reads a fixture file from the package's testdata directory.
// It panics on error: a missing fixture is a programming/setup error, not a
// runtime condition a test should tolerate.
func LoadFixture(name string) []byte {
	b, err := os.ReadFile(filepath.Join(fixturesDir(), name))
	if err != nil {
		panic("testsupport: cannot read fixture " + name + ": " + err.Error())
	}
	return b
}

// Fixture file names.
const (
	FixtureJPEG = "sample.jpg"      // small valid baseline JPEG (16x16)
	FixturePNG  = "sample.png"      // small valid PNG (16x16)
	FixturePDF  = "sample.pdf"      // small valid PDF (the render minimal.pdf)
	FixtureDoc  = "sample.docx.txt" // small "document" source bytes (converted by CollaboraMock)
)

// -------------------------------------------------------------------------
// Storages mock (CE download contract)
// -------------------------------------------------------------------------

// StoragesMock is an in-process httptest.Server emulating carbonio-storages.
//
// Contract (verified against storage/direct.go buildURL, lines 109-120):
//
//	GET {base}/{DownloadAPI}?node={fileID}&version={version}&type={serviceType}
//
// The mock validates the request method and required query params, then returns
// Blob with ContentType. A configurable Status lets tests drive the 404 /
// non-404 error arms. Hits counts every download request (atomic) so the
// cache-hit full-flow test can assert exactly-once fetch.
type StoragesMock struct {
	Server      *httptest.Server
	DownloadAPI string // path segment after base, e.g. "download"

	// Blob is returned on success.
	Blob []byte
	// ContentType is the Content-Type header set on a successful response.
	ContentType string
	// Status, when non-zero and not 200/2xx, is written instead of the blob
	// (used to drive the 404 and 5xx error flows).
	Status int

	hits int64
}

// NewStoragesMock starts a storages mock serving GET /{downloadAPI} with the
// given blob and content-type. Call Close (or t.Cleanup) to stop it.
func NewStoragesMock(downloadAPI string, blob []byte, contentType string) *StoragesMock {
	m := &StoragesMock{
		DownloadAPI: downloadAPI,
		Blob:        blob,
		ContentType: contentType,
		Status:      http.StatusOK,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+downloadAPI, m.handle)
	m.Server = httptest.NewServer(mux)
	return m
}

func (m *StoragesMock) handle(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&m.hits, 1)

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Faithful to the wire contract: node/version/type query params are required.
	q := r.URL.Query()
	if q.Get("node") == "" || q.Get("version") == "" || q.Get("type") == "" {
		// Mirror a real "bad request" — not the path the tests exercise, but
		// keeps the mock honest about the contract.
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if m.Status != 0 && (m.Status < 200 || m.Status >= 300) {
		w.WriteHeader(m.Status)
		return
	}
	if m.ContentType != "" {
		w.Header().Set("Content-Type", m.ContentType)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(m.Blob)
}

// BaseURL returns the scheme://host:port to pass as storageBaseURL to
// storage.NewDirectClient.
func (m *StoragesMock) BaseURL() string { return m.Server.URL }

// Hits returns the number of download requests received so far.
func (m *StoragesMock) Hits() int64 { return atomic.LoadInt64(&m.hits) }

// Close stops the mock server.
func (m *StoragesMock) Close() { m.Server.Close() }

// -------------------------------------------------------------------------
// Collabora / docs-editor mock (convert contract)
// -------------------------------------------------------------------------

// CollaboraMock is an in-process httptest.Server emulating the docs-editor
// (Collabora Online) convert-to endpoint.
//
// Contract (verified against render/document.go collaboraConvertOnce, lines
// 83-156, and server/document.go convertDocToPDF, lines 366-371):
//
//	POST {addr}/pdf?lang={langTag}
//	Content-Type: multipart/form-data, single field "files"
//	→ response body = converted PDF bytes
//
// PDF is the bytes returned on success. A configurable Status drives the
// 5xx error flow (handler → 502).
type CollaboraMock struct {
	Server *httptest.Server

	// PDF is the converted PDF returned on success.
	PDF []byte
	// Status, when non-zero and not 2xx, is written instead of the PDF (used
	// to drive the 5xx → 502 error flow).
	Status int

	hits int64
}

// NewCollaboraMock starts a Collabora convert mock that returns pdf on a
// successful multipart POST to /pdf. Call Close (or t.Cleanup) to stop it.
func NewCollaboraMock(pdf []byte) *CollaboraMock {
	m := &CollaboraMock{PDF: pdf, Status: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/pdf", m.handle)
	m.Server = httptest.NewServer(mux)
	return m
}

func (m *CollaboraMock) handle(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&m.hits, 1)

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if m.Status != 0 && (m.Status < 200 || m.Status >= 300) {
		w.WriteHeader(m.Status)
		return
	}
	// Faithful to the wire contract: must be multipart with a "files" field.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f, _, err := r.FormFile("files")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = f.Close()

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(m.PDF)
}

// ConvertAddress returns the value to set as
// cfg.DocumentConversionFullConvertAddress. The preview code appends "/pdf",
// so we return the bare server URL (no path), which makes the final request
// URL {URL}/pdf — matching the /pdf route registered above.
func (m *CollaboraMock) ConvertAddress() string { return m.Server.URL }

// Hits returns the number of convert requests received so far.
func (m *CollaboraMock) Hits() int64 { return atomic.LoadInt64(&m.hits) }

// Close stops the mock server.
func (m *CollaboraMock) Close() { m.Server.Close() }
