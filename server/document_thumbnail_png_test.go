// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/server/testsupport"
)

type pathRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (p *pathRecorder) add(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, s)
}

func (p *pathRecorder) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

func TestDocumentThumbnail_ConvertsToPNGAndUsesImagePipeline(t *testing.T) {
	testsupport.InitRealRender(t)
	pngBytes := testsupport.LoadFixture(testsupport.FixturePNG)
	pdfBytes := testsupport.LoadFixture(testsupport.FixturePDF)

	rec := &pathRecorder{}
	collabora := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/png") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfBytes)
	}))
	t.Cleanup(collabora.Close)

	cfg := testCfg()
	cfg.DocumentConversionFullConvertAddress = collabora.URL + "/cool/convert-to"
	store := &mockStore{blob: []byte("docx-bytes")}
	mux := buildDocMux(cfg, store)

	check := func(t *testing.T, resp *httptest.ResponseRecorder, wantType, wantFormat string) {
		t.Helper()
		if resp.Code != http.StatusOK {
			t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
		}
		if ct := resp.Header().Get("Content-Type"); ct != wantType {
			t.Errorf("Content-Type %q, want %q", ct, wantType)
		}
		c, format, err := image.DecodeConfig(bytes.NewReader(resp.Body.Bytes()))
		if err != nil || format != wantFormat || c.Width != 80 || c.Height != 80 {
			t.Errorf("decoded %+v %q err=%v, want 80x80 %s", c, format, err, wantFormat)
		}
	}
	post := func(path string) *httptest.ResponseRecorder {
		body, ct := buildMultipart(t, "file", "doc.docx", []byte("doc-bytes"))
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", ct)
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)
		return resp
	}

	getPath := fmt.Sprintf("/preview/document/%s/1/80x80/thumbnail/?service_type=files", validUUID)
	t.Run("GET jpeg", func(t *testing.T) {
		check(t, doRequest(mux, http.MethodGet, getPath), "image/jpeg", "jpeg")
	})
	t.Run("GET rounded", func(t *testing.T) {
		check(t, doRequest(mux, http.MethodGet, getPath+"&shape=rounded"), "image/png", "png")
	})
	t.Run("POST jpeg", func(t *testing.T) {
		check(t, post("/preview/document/80x80/thumbnail/"), "image/jpeg", "jpeg")
	})
	t.Run("POST rounded", func(t *testing.T) {
		check(t, post("/preview/document/80x80/thumbnail/?shape=rounded"), "image/png", "png")
	})

	paths := rec.all()
	if len(paths) != 4 {
		t.Fatalf("collabora calls %v, want 4", paths)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "/png") {
			t.Errorf("thumbnail convert path %q does not end with /png", p)
		}
	}

	t.Run("preview still converts to pdf", func(t *testing.T) {
		resp := doRequest(mux, http.MethodGet, fmt.Sprintf("/preview/document/%s/1/?service_type=files", validUUID))
		if resp.Code != http.StatusOK || resp.Header().Get("Content-Type") != "application/pdf" {
			t.Fatalf("preview: %d %q", resp.Code, resp.Header().Get("Content-Type"))
		}
		paths := rec.all()
		if last := paths[len(paths)-1]; !strings.HasSuffix(last, "/pdf") {
			t.Errorf("preview convert path %q does not end with /pdf", last)
		}
	})
}
