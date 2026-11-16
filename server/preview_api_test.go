// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/server/testsupport"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type previewReq struct {
	id, mime, lang, owner string
	version               int
}

func (r previewReq) path() string {
	q := url.Values{}
	q.Set("service_type", "files")
	if r.mime != "-" {
		q.Set("mime", r.mime)
	}
	if r.lang != "" {
		q.Set("lang", r.lang)
	}
	return "/preview/" + r.id + "/" + strconv.Itoa(r.version) + "?" + q.Encode()
}

func (h *harness) getPreview(r previewReq) *httptest.ResponseRecorder {
	h.t.Helper()
	if r.owner == "" {
		r.owner = testOwner
	}
	req := httptest.NewRequest(http.MethodGet, r.path(), nil)
	req.Header.Set("fileownerid", r.owner)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *harness) postPreview(mime string, body []byte, withFile bool) *httptest.ResponseRecorder {
	h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if withFile {
		part, err := mw.CreateFormFile("file", "upload.bin")
		if err != nil {
			h.t.Fatal(err)
		}
		_, _ = part.Write(body)
	} else {
		_ = mw.WriteField("other", "x")
	}
	_ = mw.Close()
	q := url.Values{}
	if mime != "" {
		q.Set("mime", mime)
	}
	req := httptest.NewRequest(http.MethodPost, "/preview?"+q.Encode(), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func officeCfg(preview, thumbnail bool, convertAddr string) func(*testing.T, Deps) Deps {
	return func(_ *testing.T, d Deps) Deps {
		d.Cfg.ServiceEnableDocumentPreview = preview
		d.Cfg.ServiceEnableDocumentThumbnail = thumbnail
		if convertAddr != "" {
			d.Cfg.DocumentConversionFullConvertAddress = convertAddr
		}
		return d
	}
}

func TestPreviewGet_NativeImagePassthroughBytesAndType(t *testing.T) {
	cases := []struct{ send, want string }{
		{"image/jpeg", "image/jpeg"},
		{"image/jpg", "image/jpeg"},
		{"image/png", "image/png"},
		{"IMAGE/PNG; charset=binary", "image/png"},
		{"image/gif", "image/gif"},
		{"image/webp", "image/webp"},
		{"image/avif", "image/avif"},
		{"image/bmp", "image/bmp"},
	}
	for _, c := range cases {
		t.Run(c.send, func(t *testing.T) {
			h := newHarness(t, harnessOpts{noDB: true})
			id := newID()
			src := []byte("not-decodable-" + c.want + "-bytes")
			h.seedSource(id, src)
			rec := h.getPreview(previewReq{id: id, version: testVersion, mime: c.send})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != c.want {
				t.Errorf("Content-Type = %q, want %q", got, c.want)
			}
			if !bytes.Equal(rec.Body.Bytes(), src) {
				t.Errorf("body differs from the source")
			}
		})
	}
}

func TestPreviewGet_PDFPassthrough(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	src := testsupport.LoadFixture("sample.pdf")
	h.seedSource(id, src)
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "application/pdf"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !bytes.Equal(rec.Body.Bytes(), src) {
		t.Fatalf("%d %q, want the stored PDF unchanged", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPreviewGet_VideoPassthrough(t *testing.T) {
	for _, send := range []string{"video/mp4", "VIDEO/WebM", "video/x-matroska"} {
		h := newHarness(t, harnessOpts{noDB: true})
		id := newID()
		src := []byte("video-bytes-" + send)
		h.seedSource(id, src)
		rec := h.getPreview(previewReq{id: id, version: testVersion, mime: send})
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != strings.ToLower(send) || !bytes.Equal(rec.Body.Bytes(), src) {
			t.Errorf("%s: %d %q", send, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestPreviewGet_FetchesWithOwnerAndServiceType(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.store.seed(id, testVersion, "files", "owner-x", []byte("x"))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/png", owner: "owner-x"})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-scoped read: %d", rec.Code)
	}
	if rec = h.getPreview(previewReq{id: id, version: testVersion, mime: "image/png", owner: "owner-y"}); rec.Code != http.StatusNotFound {
		t.Fatalf("other owner: %d, want 404", rec.Code)
	}
}

func TestPreviewGet_ContentLengthForwarded(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	src := bytes.Repeat([]byte("L"), 12345)
	h.seedSource(id, src)
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/png"})
	if got := rec.Header().Get("Content-Length"); got != "12345" {
		t.Fatalf("Content-Length = %q, want 12345", got)
	}
}

type streamStore struct {
	*memStore
	open func(ctx context.Context) io.ReadCloser
}

func (s streamStore) RetrieveDataStreaming(ctx context.Context, _ string, _ int, _, _ string) (io.ReadCloser, int64, error) {
	return s.open(ctx), -1, nil
}

func streamHarness(t *testing.T, open func(ctx context.Context) io.ReadCloser, timeoutSeconds int) *httptest.Server {
	t.Helper()
	h := newHarness(t, harnessOpts{noDB: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.Store = streamStore{memStore: newMemStore(), open: open}
		if timeoutSeconds > 0 {
			d.Cfg.ServiceTimeoutInSeconds = timeoutSeconds
		}
		return d
	}})
	srv := httptest.NewServer(h.mux)
	t.Cleanup(srv.Close)
	return srv
}

type stallReader struct {
	ctx   context.Context
	first []byte
	rest  <-chan []byte
}

func (r *stallReader) Read(p []byte) (int, error) {
	if len(r.first) > 0 {
		n := copy(p, r.first)
		r.first = r.first[n:]
		return n, nil
	}
	select {
	case b, ok := <-r.rest:
		if !ok {
			return 0, io.EOF
		}
		r.first = b
		return r.Read(p)
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

func (r *stallReader) Close() error { return nil }

func TestPreviewGet_PassthroughStreamsWithoutBuffering(t *testing.T) {
	const chunk = 32 << 10
	first := bytes.Repeat([]byte{'a'}, chunk)
	rest := make(chan []byte)
	srv := streamHarness(t, func(ctx context.Context) io.ReadCloser {
		return &stallReader{ctx: ctx, first: first, rest: rest}
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/preview/"+newID()+"/1?service_type=files&mime=video/mp4", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("no response while the source was still open: the handler buffers the blob: %v", err)
	}
	defer resp.Body.Close()
	got := make([]byte, chunk/2)
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatalf("first bytes did not reach the client before the source finished: %v", err)
	}
	if !bytes.Equal(got, first[:chunk/2]) {
		t.Fatal("first chunk corrupted")
	}
	if resp.Header.Get("Content-Length") != "" {
		t.Errorf("unknown source length must not set Content-Length, got %q", resp.Header.Get("Content-Length"))
	}

	tail := bytes.Repeat([]byte{'z'}, 3*chunk)
	go func() {
		rest <- tail
		close(rest)
	}()
	remaining, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(append(got, remaining...)) != sha256.Sum256(append(append([]byte(nil), first...), tail...)) {
		t.Errorf("streamed body differs: %d bytes after the prefix", len(remaining))
	}
}

func TestPreviewGet_StalledSourceAbortsAfterIdle(t *testing.T) {
	first := bytes.Repeat([]byte{'a'}, 16<<10)
	srv := streamHarness(t, func(ctx context.Context) io.ReadCloser {
		return &stallReader{ctx: ctx, first: first, rest: make(chan []byte)}
	}, 1)

	start := time.Now()
	resp, err := http.Get(srv.URL + "/preview/" + newID() + "/1?service_type=files&mime=application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("a stalled source produced a clean %d-byte response, want an aborted one", len(body))
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("abort took %s, the idle timeout is 1s", d)
	}
}

func TestPreviewGet_ConvertsTIFFToJPEG(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, ffmpegImage(t, "tiff", 1600, 1200))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/tiff"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("%d %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if w, hh, f := decodeInfo(t, rec.Body.Bytes()); w != 1600 || hh != 1200 || f != "jpeg" {
		t.Fatalf("converted = %dx%d %s, want full resolution 1600x1200 jpeg", w, hh, f)
	}
}

const (
	svgOpaque = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect width="200" height="100" fill="#f00"/></svg>`
	svgAlpha  = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect x="100" width="100" height="100" fill="#f00"/></svg>`
)

func TestPreviewGet_SVGIsConverted(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte(svgOpaque))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/svg+xml"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("%d %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if w, hh, f := decodeInfo(t, rec.Body.Bytes()); w != 200 || hh != 100 || f != "jpeg" {
		t.Fatalf("svg = %dx%d %s", w, hh, f)
	}
}

func TestPreviewGet_ConvertsAlphaToPNG(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte(svgAlpha))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/svg+xml"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("%d %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if _, _, f := decodeInfo(t, rec.Body.Bytes()); f != "png" {
		t.Fatalf("format %s", f)
	}
}

func TestPreviewGet_ConvertedRenderFailureIs400(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("this is not a tiff"))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/tiff"})
	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

func TestPreviewGet_OfficeToPDF(t *testing.T) {
	pdf := testsupport.LoadFixture("sample.pdf")
	mock := testsupport.NewCollaboraMock(pdf)
	t.Cleanup(mock.Close)
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(true, false, mock.ConvertAddress())})
	id := newID()
	h.seedSource(id, []byte("docx-bytes"))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: docxMime})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !bytes.Equal(rec.Body.Bytes(), pdf) {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if mock.Hits() != 1 {
		t.Errorf("collabora hits = %d", mock.Hits())
	}
}

func TestPreviewGet_OfficeForwardsLangAndWholeDocument(t *testing.T) {
	var gotLang, gotURL string
	var gotLen int
	prev := collaboraConvertFunc
	collaboraConvertFunc = func(_ context.Context, data []byte, lang, url string, _ time.Duration) ([]byte, error) {
		gotLang, gotURL, gotLen = lang, url, len(data)
		return []byte("%PDF-converted"), nil
	}
	t.Cleanup(func() { collaboraConvertFunc = prev })
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("docx-bytes"))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "text/plain", lang: "it-IT"})
	if rec.Code != http.StatusOK || rec.Body.String() != "%PDF-converted" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if gotLang != "it-IT" || !strings.HasSuffix(gotURL, "/pdf") || gotLen != len("docx-bytes") {
		t.Errorf("collabora call lang=%q url=%q len=%d", gotLang, gotURL, gotLen)
	}
}

func TestPreview_LangTagValidation(t *testing.T) {
	prev := collaboraConvertFunc
	collaboraConvertFunc = func(context.Context, []byte, string, string, time.Duration) ([]byte, error) {
		return []byte("%PDF"), nil
	}
	t.Cleanup(func() { collaboraConvertFunc = prev })
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("docx"))

	for _, tag := range []string{"en&x=1", "x", "en_US", "%20"} {
		q := "service_type=files&mime=" + docxMime + "&lang=" + url.QueryEscape(tag)
		assertValidationError(t, doRequest(h.mux, http.MethodGet, "/preview/"+id+"/1?"+q), "lang")
		body, ct := buildMultipart(t, "file", "x.docx", []byte("doc"))
		req := httptest.NewRequest(http.MethodPost, "/preview?mime="+docxMime+"&lang="+url.QueryEscape(tag), body)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		h.mux.ServeHTTP(rec, req)
		assertValidationError(t, rec, "lang")
	}
	for _, tag := range []string{"pt-BR", "zh-Hans-CN", "it", "en"} {
		if rec := h.getPreview(previewReq{id: id, version: testVersion, mime: docxMime, lang: tag}); rec.Code != http.StatusOK {
			t.Errorf("GET lang=%q: %d", tag, rec.Code)
		}
		body, ct := buildMultipart(t, "file", "x.docx", []byte("doc"))
		req := httptest.NewRequest(http.MethodPost, "/preview?mime="+docxMime+"&lang="+tag, body)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		h.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("POST lang=%q: %d", tag, rec.Code)
		}
	}
}

func TestPreviewGet_OfficeCollaboraFailureIs502(t *testing.T) {
	mock := testsupport.NewCollaboraMock(nil)
	mock.Status = http.StatusInternalServerError
	t.Cleanup(mock.Close)
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(true, true, mock.ConvertAddress())})
	id := newID()
	h.seedSource(id, []byte("docx-bytes"))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: docxMime})
	assertStringDetail(t, rec, http.StatusBadGateway, config.Msg.StorageUnavailable)
}

func TestPreviewGet_OfficeDisabled400(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(false, true, "")})
	id := newID()
	h.seedSource(id, []byte("docx-bytes"))
	rec := h.getPreview(previewReq{id: id, version: testVersion, mime: docxMime})
	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
	if h.sourceReads() != 0 {
		t.Errorf("a disabled office preview fetched the source")
	}
}

func TestPreviewGet_ImageAndPDFNotGatedByOfficeFlag(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(false, false, "")})
	id := newID()
	h.seedSource(id, []byte("png-bytes"))
	if rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "image/png"}); rec.Code != http.StatusOK {
		t.Fatalf("png: %d", rec.Code)
	}
	if rec := h.getPreview(previewReq{id: id, version: testVersion, mime: "application/pdf"}); rec.Code != http.StatusOK {
		t.Fatalf("pdf: %d", rec.Code)
	}
}

func TestPreviewGet_UnsupportedMIME415(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("x"))
	for _, m := range []string{"application/octet-stream", "audio/mpeg", "text/html", "garbage", "video/"} {
		rec := h.getPreview(previewReq{id: id, version: testVersion, mime: m})
		assertStringDetail(t, rec, http.StatusUnsupportedMediaType, config.Msg.FormatNotSupported)
	}
	if h.sourceReads() != 0 {
		t.Errorf("unsupported MIME fetched the source")
	}
}

func TestPreviewGet_Validation(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	cases := []struct{ name, path, param string }{
		{"missing mime", "/preview/" + id + "/1?service_type=files", "mime"},
		{"missing service_type", "/preview/" + id + "/1?mime=image/png", "service_type"},
		{"bad service_type", "/preview/" + id + "/1?mime=image/png&service_type=other", "service_type"},
		{"bad uuid", "/preview/not-a-uuid/1?mime=image/png&service_type=files", "id"},
		{"short uuid", "/preview/1234/1?mime=image/png&service_type=files", "id"},
		{"nil uuid", "/preview/00000000-0000-0000-0000-000000000000/1?mime=image/png&service_type=files", "id"},
		{"negative version", "/preview/" + id + "/-1?mime=image/png&service_type=files", "version"},
		{"alpha version", "/preview/" + id + "/abc?mime=image/png&service_type=files", "version"},
		{"bad lang", "/preview/" + id + "/1?mime=image/png&service_type=files&lang=not%20a%20tag", "lang"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertValidationError(t, doRequest(h.mux, http.MethodGet, c.path), c.param)
		})
	}
	if h.sourceReads() != 0 {
		t.Errorf("a rejected request fetched the source")
	}
}

func TestPreview_MethodNotAllowedAndUnmatched(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	path := "/preview/" + newID() + "/1?service_type=files&mime=image/png"
	if rec := doRequest(h.mux, http.MethodDelete, path); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d, want 405", rec.Code)
	}
	if rec := doRequest(h.mux, http.MethodPut, "/preview?mime=image/png"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /preview: %d, want 405", rec.Code)
	}
	if rec := doRequest(h.mux, http.MethodGet, "/preview/"+newID()+"/1/extra?service_type=files&mime=image/png"); rec.Code != http.StatusNotFound {
		t.Errorf("extra segment: %d, want 404", rec.Code)
	}
}

func TestPreviewGet_Storage404(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.getPreview(previewReq{id: newID(), version: testVersion, mime: "image/png"})
	assertStringDetail(t, rec, http.StatusNotFound, config.Msg.ItemNotFound)
}

func TestPreviewGet_StorageErrorIs422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	h.store.getErr = func(memKey) error { return storage.ErrUnavailable }
	rec := h.getPreview(previewReq{id: newID(), version: testVersion, mime: "application/pdf"})
	assertStringDetail(t, rec, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

func TestPreview_NeverCached(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	cases := []struct {
		mime string
		body []byte
	}{
		{"image/png", []byte("native")},
		{"application/pdf", testsupport.LoadFixture("sample.pdf")},
		{"image/svg+xml", []byte(svgOpaque)},
	}
	for _, c := range cases {
		id := newID()
		h.seedSource(id, c.body)
		before := h.sourceReads()
		for i := 0; i < 2; i++ {
			if rec := h.getPreview(previewReq{id: id, version: testVersion, mime: c.mime}); rec.Code != http.StatusOK {
				t.Fatalf("%s: %d", c.mime, rec.Code)
			}
		}
		if n := h.sourceReads() - before; n != 2 {
			t.Errorf("%s: %d storages fetches for two GETs, want 2 (previews are never cached)", c.mime, n)
		}
	}
}

func TestPreview_OfficeNeverCached(t *testing.T) {
	var calls atomic.Int32
	prev := collaboraConvertFunc
	collaboraConvertFunc = func(context.Context, []byte, string, string, time.Duration) ([]byte, error) {
		calls.Add(1)
		return []byte("%PDF"), nil
	}
	t.Cleanup(func() { collaboraConvertFunc = prev })
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("docx"))
	for i := 0; i < 2; i++ {
		if rec := h.getPreview(previewReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusOK {
			t.Fatalf("%d", rec.Code)
		}
	}
	if h.sourceReads() != 2 || calls.Load() != 2 {
		t.Errorf("fetches=%d conversions=%d, want 2 and 2", h.sourceReads(), calls.Load())
	}
}

func TestPreviewPost_PassthroughFromUpload(t *testing.T) {
	cases := []struct{ send, want string }{
		{"image/jpeg", "image/jpeg"},
		{"image/jpg", "image/jpeg"},
		{"image/gif", "image/gif"},
		{"image/bmp", "image/bmp"},
		{"image/avif", "image/avif"},
		{"application/pdf", "application/pdf"},
		{"video/mp4", "video/mp4"},
	}
	for _, c := range cases {
		h := newHarness(t, harnessOpts{noDB: true})
		src := []byte("upload-" + c.send)
		rec := h.postPreview(c.send, src, true)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != c.want || !bytes.Equal(rec.Body.Bytes(), src) {
			t.Errorf("%s: %d %q", c.send, rec.Code, rec.Header().Get("Content-Type"))
		}
		if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(src)) {
			t.Errorf("%s: Content-Length %q", c.send, got)
		}
		h.assertUntouched()
	}
}

func TestPreviewPost_ConvertsSVG(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.postPreview("image/svg+xml", []byte(svgOpaque), true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if w, hh, _ := decodeInfo(t, rec.Body.Bytes()); w != 200 || hh != 100 {
		t.Errorf("%dx%d", w, hh)
	}
	h.assertUntouched()
}

func TestPreviewPost_ConvertedRenderFailureIs400(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.postPreview("image/tiff", []byte("garbage"), true)
	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.FormatNotSupported)
}

func TestPreviewPost_Office(t *testing.T) {
	pdf := testsupport.LoadFixture("sample.pdf")
	mock := testsupport.NewCollaboraMock(pdf)
	t.Cleanup(mock.Close)
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(true, false, mock.ConvertAddress())})
	rec := h.postPreview(docxMime, []byte("docx"), true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !bytes.Equal(rec.Body.Bytes(), pdf) {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	h.assertUntouched()
}

func TestPreviewPost_OfficeDisabled400(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(false, true, "")})
	rec := h.postPreview(docxMime, []byte("docx"), true)
	assertStringDetail(t, rec, http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
}

func TestPreviewPost_CollaboraFailureIs502(t *testing.T) {
	mock := testsupport.NewCollaboraMock(nil)
	mock.Status = http.StatusBadGateway
	t.Cleanup(mock.Close)
	h := newHarness(t, harnessOpts{noDB: true, cfg: officeCfg(true, true, mock.ConvertAddress())})
	rec := h.postPreview(docxMime, []byte("docx"), true)
	assertStringDetail(t, rec, http.StatusBadGateway, config.Msg.StorageUnavailable)
}

func TestPreviewPost_NoFile422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.postPreview("image/png", nil, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no file part: %d", rec.Code)
	}
	if rec = h.postPreview("image/png", nil, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty file: %d, want 422", rec.Code)
	}
}

func TestPreviewPost_NotMultipart422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	req := httptest.NewRequest(http.MethodPost, "/preview?mime=image/png", strings.NewReader("not-multipart"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d, want 422", rec.Code)
	}
}

func TestPreviewPost_UnsupportedAndMissingMIME(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	assertStringDetail(t, h.postPreview("application/octet-stream", []byte("x"), true),
		http.StatusUnsupportedMediaType, config.Msg.FormatNotSupported)
	if rec := h.postPreview("", []byte("x"), true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing mime: %d", rec.Code)
	}
}

func TestRoutes_OldPathsAre404(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	removed := []struct{ method, path string }{
		{"GET", "/preview/image/" + id + "/1/100x100/"},
		{"GET", "/preview/image/" + id + "/1/100x100/thumbnail/"},
		{"POST", "/preview/image/100x100/"},
		{"POST", "/preview/image/100x100/thumbnail/"},
		{"GET", "/preview/pdf/" + id + "/1/"},
		{"GET", "/preview/pdf/" + id + "/1/100x100/thumbnail/"},
		{"POST", "/preview/pdf/"},
		{"POST", "/preview/pdf/100x100/thumbnail/"},
		{"GET", "/preview/document/" + id + "/1/"},
		{"GET", "/preview/document/" + id + "/1/100x100/thumbnail/"},
		{"POST", "/preview/document/"},
		{"POST", "/preview/document/100x100/thumbnail/"},
		{"GET", "/preview/video/" + id + "/1/"},
		{"GET", "/preview/video/" + id + "/1/100x100/thumbnail/"},
		{"POST", "/preview/video/" + id + "/1/"},
		{"POST", "/preview/video/" + id + "/1/copy/"},
		{"DELETE", "/preview/video/" + id + "/1/"},
	}
	for _, r := range removed {
		req := httptest.NewRequest(r.method, r.path+"?service_type=files", nil)
		rec := httptest.NewRecorder()
		h.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", r.method, r.path, rec.Code)
		}
	}
}
