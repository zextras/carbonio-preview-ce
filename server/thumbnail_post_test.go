// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"image/gif"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/render"
)

func (h *harness) post(size, mime string, body []byte, withFile bool) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(http.MethodPost, "/thumbnail/"+size+"?"+q.Encode(), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *harness) assertUntouched() {
	h.t.Helper()
	if h.store.getCount() != 0 || len(h.store.putKeys()) != 0 || len(h.store.deletedKeys()) != 0 {
		h.t.Errorf("POST touched storages: gets=%d puts=%d deletes=%d", h.store.getCount(), len(h.store.putKeys()), len(h.store.deletedKeys()))
	}
}

func TestThumbnailPost_Image(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	for size, side := range map[string]int{"s": 320, "m": 1280} {
		rec := h.post(size, "image/jpeg", jpegOf(t, 4000, 3000), true)
		w, _, f := decodeInfo(t, rec.Body.Bytes())
		if rec.Code != http.StatusOK || f != "jpeg" || w != side || rec.Header().Get("Content-Type") != "image/jpeg" {
			t.Errorf("%s: %d %s %d", size, rec.Code, f, w)
		}
	}
	h.assertUntouched()
}

func TestThumbnailPost_SmallPNGWithAlphaIsReEncodedAtItsSize(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	src := pngOf(t, 100, 100, true)
	rec := h.post("s", "image/png", src, true)
	w, hh, f := decodeInfo(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || f != "png" || w != 100 || hh != 100 {
		t.Fatalf("%d %q %s %dx%d, want a PNG at 100x100", rec.Code, rec.Header().Get("Content-Type"), f, w, hh)
	}
	if bytes.Equal(rec.Body.Bytes(), src) {
		t.Error("the upload must be re-encoded, never echoed")
	}
}

func TestThumbnailPost_PDF(t *testing.T) {
	prev := pdfThumbFunc
	pdfThumbFunc = func(_ context.Context, _ []byte, side int) (render.Rendition, error) { return stubFor(side), nil }
	t.Cleanup(func() { pdfThumbFunc = prev })
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.post("m", "application/pdf", []byte("%PDF"), true)
	if rec.Code != http.StatusOK || rec.Body.String() != string(stubM.Body) || rec.Header().Get("Content-Type") != stubM.ContentType {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	rec = h.post("s", "application/pdf", []byte("%PDF"), true)
	if rec.Body.String() != string(stubS.Body) {
		t.Fatalf("s: %q", rec.Body)
	}
}

func TestThumbnailPost_OfficeFlagOff400(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.Cfg.ServiceEnableDocumentThumbnail = false
		return d
	}})
	if rec := h.post("m", docxMime, []byte("doc"), true); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestThumbnailPost_Office(t *testing.T) {
	var sems []int
	var h *harness
	calls := stubOffice(t, func() int { return len(h.deps.AsyncSem) }, func(c collaboraCall) ([]byte, error) {
		sems = append(sems, c.slots)
		return []byte("png"), nil
	})
	h = newHarness(t, harnessOpts{noDB: true})
	rec := h.post("m", docxMime, []byte("doc"), true)
	if rec.Code != http.StatusOK || rec.Body.String() != string(stubM.Body) {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if len(*calls) != 1 || (*calls)[0].url[len((*calls)[0].url)-4:] != "/png" || sems[0] != 0 {
		t.Errorf("collabora calls %+v slots %v, want one convert-to png with no slot", *calls, sems)
	}
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return nil, errors.New("collabora down") })
	if rec := h.post("m", docxMime, []byte("doc"), true); rec.Code != http.StatusBadGateway {
		t.Errorf("collabora failure: %d, want 502", rec.Code)
	}
}

func TestThumbnailPost_AnimatedGIF(t *testing.T) {
	realVips(t)
	realFFmpeg(t)
	h := newHarness(t, harnessOpts{noDB: true})
	const frames = 6
	rec := h.post("m", gifMime, gifOf(t, 1600, 900, frames), true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	g, err := gif.DecodeAll(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || len(g.Image) != frames/2 || g.Config.Width != 1280 {
		t.Fatalf("frames=%d width=%d err=%v, want %d frames (the 20 fps fixture capped at 10 fps) 1280 wide", len(g.Image), g.Config.Width, err, frames/2)
	}
	h.assertUntouched()
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("async slot leaked: %d", n)
	}
}

func TestThumbnailPost_AnimatedGIFBelowTargetStillGoesThroughFFmpeg(t *testing.T) {
	realVips(t)
	calls := failingGIF(t)
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.post("m", gifMime, gifOf(t, 600, 400, 5), true)
	if rec.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("%d calls=%d, want one ffmpeg pass even below the target", rec.Code, *calls)
	}
}

func TestThumbnailPost_AnimatedGIFFailureFallsBackToStatic(t *testing.T) {
	realVips(t)
	failingGIF(t)
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.post("s", gifMime, gifOf(t, 1600, 900, 4), true)
	w, _, f := decodeInfo(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || (f != "jpeg" && f != "png") || w != 320 {
		t.Fatalf("%d %s %d, want a 320 px static frame", rec.Code, f, w)
	}
}

func stubVideoCodec(t *testing.T, codec string, sem func() int, slots *[]int) {
	t.Helper()
	prevCodec, prevFrame, prevThumb := videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc, imageThumbFunc
	videoDetectCodecFromFileFunc = func(context.Context, string) (string, error) {
		if sem != nil {
			*slots = append(*slots, sem())
		}
		return codec, nil
	}
	videoFirstFrameFromFileFunc = func(context.Context, string) ([]byte, error) { return []byte("frame"), nil }
	imageThumbFunc = func(_ []byte, side int) (render.Rendition, error) { return stubFor(side), nil }
	t.Cleanup(func() {
		videoDetectCodecFromFileFunc, videoFirstFrameFromFileFunc, imageThumbFunc = prevCodec, prevFrame, prevThumb
	})
}

func TestThumbnailPost_Video(t *testing.T) {
	var h *harness
	var slots []int
	stubVideoCodec(t, "h264", func() int { return len(h.deps.AsyncSem) }, &slots)
	h = newHarness(t, harnessOpts{noDB: true})
	rec := h.post("s", videoMime, []byte("video-bytes"), true)
	if rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if len(slots) != 1 || slots[0] != 1 {
		t.Errorf("probe ran under %v async slots, want 1", slots)
	}
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("async slot leaked: %d", n)
	}
	h.assertUntouched()
}

func TestThumbnailPost_VideoUnsupportedCodec415(t *testing.T) {
	stubVideoCodec(t, "prores-weird", nil, nil)
	h := newHarness(t, harnessOpts{noDB: true})
	if rec := h.post("m", videoMime, []byte("video-bytes"), true); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", rec.Code)
	}
}

func TestThumbnailPost_VideoExtractFailureIs400(t *testing.T) {
	stubVideoCodec(t, "h264", nil, nil)
	videoFirstFrameFromFileFunc = func(context.Context, string) ([]byte, error) { return nil, errors.New("ffmpeg exploded") }
	h := newHarness(t, harnessOpts{noDB: true})
	if rec := h.post("m", videoMime, []byte("video-bytes"), true); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestThumbnailPost_NotCached(t *testing.T) {
	var mu sync.Mutex
	renders := 0
	prev := imageThumbFunc
	imageThumbFunc = func(_ []byte, side int) (render.Rendition, error) {
		mu.Lock()
		renders++
		mu.Unlock()
		return stubFor(side), nil
	}
	t.Cleanup(func() { imageThumbFunc = prev })
	prevProbe := probeFunc
	probeFunc = func([]byte) (render.Info, error) { return render.Info{Width: 4000, Height: 3000, Pages: 1}, nil }
	t.Cleanup(func() { probeFunc = prevProbe })
	h := newHarness(t, harnessOpts{noDB: true})
	for i := 0; i < 2; i++ {
		if rec := h.post("m", "image/jpeg", []byte("same-bytes"), true); rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if renders != 2 {
		t.Errorf("renders = %d, a POST output must never be cached", renders)
	}
	h.assertUntouched()
}

func TestThumbnailPost_NoFile422(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	if rec := h.post("m", "image/jpeg", nil, false); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing file: %d, want 422", rec.Code)
	}
	if rec := h.post("m", "image/jpeg", nil, true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty file: %d, want 422", rec.Code)
	}
}

func TestThumbnailPost_UnsupportedMIME415(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	for _, m := range []string{"application/octet-stream", "image/bmp", "garbage"} {
		if rec := h.post("m", m, []byte("x"), true); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d, want 415", m, rec.Code)
		}
	}
	if rec := h.post("m", "", []byte("x"), true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing mime: %d, want 422", rec.Code)
	}
	if rec := h.post("l", "image/jpeg", []byte("x"), true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid size: %d, want 422", rec.Code)
	}
}

func TestThumbnailPost_RenderFailureIs400(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	if rec := h.post("m", "image/webp", []byte("not an image"), true); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
