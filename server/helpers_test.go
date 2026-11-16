// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type mockStore struct {
	blob            []byte
	err             error
	lastID          string
	lastVersion     int
	lastServiceType string
	calls           int
}

func (m *mockStore) RetrieveData(
	_ context.Context,
	fileID string,
	version int,
	serviceType, _ string,
) (storage.Blob, error) {
	m.calls++
	m.lastID = fileID
	m.lastVersion = version
	m.lastServiceType = serviceType
	return m.blob, m.err
}

func (m *mockStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _, _ string) (io.ReadCloser, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return io.NopCloser(bytes.NewReader(m.blob)), int64(len(m.blob)), nil
}

func (m *mockStore) StoreData(_ context.Context, nodeID string, _ int, _, _ string, _ []byte) (string, error) {
	return nodeID, nil
}

func (m *mockStore) Delete(_ context.Context, _ string, _ int, _, _ string) error {
	return nil
}

func testCfg() *config.Config {
	return &config.Config{
		ServiceName:                          "preview",
		ServiceHealthName:                    "health",
		ServiceTimeoutInSeconds:              30,
		ServiceDocsTimeout:                   15,
		RenderConcurrency:                    runtime.NumCPU(),
		AsyncConcurrency:                     runtime.NumCPU(),
		ServiceEnableDocumentPreview:         true,
		ServiceEnableDocumentThumbnail:       true,
		StorageFullAddress:                   "http://127.0.0.1:20000",
		StorageHealthCheck:                   "health/live",
		DocumentConversionFullServiceAddress: "http://127.0.0.1:20001/services/docs/editor/",
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/services/docs/editor/cool/convert-to",
		AreDocsEnabled:                       true,
	}
}

const validUUID = "123e4567-e89b-12d3-a456-426614174000"

func doRequest(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func assertValidationError(t *testing.T, rec *httptest.ResponseRecorder, paramName string) {
	t.Helper()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want 422", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	var body validationErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON unmarshal: %v (body: %q)", err, rec.Body.String())
	}
	if len(body.Detail) == 0 {
		t.Fatal("expected non-empty detail array")
	}
	if paramName == "" {
		return
	}
	for _, d := range body.Detail {
		for _, l := range d.Loc {
			if l == paramName {
				return
			}
		}
	}
	t.Errorf("expected param %q in detail loc, got %v", paramName, body.Detail)
}

func assertStringDetail(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantSubstr string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Errorf("status: got %d, want %d (body: %q)", rec.Code, wantStatus, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	var body stringDetailBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON unmarshal: %v (body: %q)", err, rec.Body.String())
	}
	if wantSubstr != "" && !strings.Contains(body.Detail, wantSubstr) {
		t.Errorf("detail %q does not contain %q", body.Detail, wantSubstr)
	}
}

func buildMultipart(t *testing.T, fieldName, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
