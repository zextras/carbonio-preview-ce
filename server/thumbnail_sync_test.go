// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/server/testsupport"
	"github.com/zextras/carbonio-preview-ce/v4/video"
)

var (
	vipsOnce sync.Once
	vipsErr  error
)

func realVips(t *testing.T) {
	t.Helper()
	vipsOnce.Do(func() { vipsErr = render.InitVips("carbonio-preview-server-test") })
	if vipsErr != nil {
		t.Skipf("libvips unavailable: %v", vipsErr)
	}
}

func realFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(video.FFmpegPath); err == nil {
		return
	}
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	prev := video.FFmpegPath
	video.FFmpegPath = p
	t.Cleanup(func() { video.FFmpegPath = prev })
}

func noisyImage(w, h int, alpha func(x, y int) uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(int64(w*7919 + h)))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha != nil {
				a = alpha(x, y)
			}
			img.SetRGBA(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(x * 255 / w), uint8(y * 255 / h), a})
		}
	}
	return img
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, noisyImage(w, h, nil), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngOf(t *testing.T, w, h int, transparentLeftHalf bool) []byte {
	t.Helper()
	var alpha func(x, y int) uint8
	if transparentLeftHalf {
		alpha = func(x, _ int) uint8 {
			if x < w/2 {
				return 0
			}
			return 255
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, noisyImage(w, h, alpha)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gifOf(t *testing.T, w, h, frames int) []byte {
	t.Helper()
	g := &gif.GIF{}
	pal := color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}, color.RGBA{0, 0, 255, 255}}
	for i := 0; i < frames; i++ {
		f := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		draw.Draw(f, f.Bounds(), &image.Uniform{pal[i%len(pal)]}, image.Point{}, draw.Src)
		step := w / (frames + 1)
		draw.Draw(f, image.Rect(i*step, 0, (i+1)*step+1, h), &image.Uniform{pal[(i+2)%len(pal)]}, image.Point{}, draw.Src)
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 5)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func ffmpegImage(t *testing.T, ext string, w, h int) []byte {
	t.Helper()
	realFFmpeg(t)
	out := filepath.Join(t.TempDir(), "in."+ext)
	cmd := exec.Command(video.FFmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size="+strconv.Itoa(w)+"x"+strconv.Itoa(h),
		"-frames:v", "1", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot write %s: %v %s", ext, err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeInfo(t *testing.T, body []byte) (int, int, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("not an image: %v", err)
	}
	return cfg.Width, cfg.Height, format
}

func (h *harness) thumbKeyOf(r thumbReq, size string) string {
	if r.owner == "" {
		r.owner = testOwner
	}
	if r.serviceType == "" {
		r.serviceType = "files"
	}
	return thumbKey("t", r.serviceType, r.owner, r.id, r.version, size, r.digest)
}

func (h *harness) seedSource(id string, data []byte) {
	h.store.seed(id, testVersion, "files", testOwner, data)
}

func (h *harness) sourceReads() int { return h.store.getCount() }

func TestThumbnailGet_SyncImageSizesRenderedSeparately(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, jpegOf(t, 4000, 3000))

	rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", size: "m"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("m: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if w, hh, f := decodeInfo(t, rec.Body.Bytes()); w != 1280 || hh != 960 || f != "jpeg" {
		t.Fatalf("m = %dx%d %s, want 1280x960 jpeg", w, hh, f)
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Errorf("200 carries cache headers: %v", rec.Header())
	}

	rec = h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", size: "s"})
	if rec.Code != http.StatusOK {
		t.Fatalf("s: %d", rec.Code)
	}
	if w, hh, _ := decodeInfo(t, rec.Body.Bytes()); w != 320 || hh != 240 {
		t.Fatalf("s = %dx%d, want 320x240", w, hh)
	}
	if n := h.sourceReads(); n != 2 {
		t.Errorf("source reads = %d, each size is rendered from the source on its own request", n)
	}
	if n := len(h.store.putKeys()); n != 0 {
		t.Errorf("DB-less generation persisted %d blobs", n)
	}
}

func TestThumbnailGet_UseOriginalBelowS(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	src := jpegOf(t, 300, 200)
	h.seedSource(id, src)
	for _, mime := range []string{"image/jpeg", "image/jpg"} {
		for _, size := range []string{"s", "m"} {
			rec := h.get(thumbReq{id: id, version: testVersion, mime: mime, size: size})
			if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), src) || rec.Header().Get("Content-Type") != "image/jpeg" {
				t.Errorf("%s %s: %d type=%q identical=%v", mime, size, rec.Code, rec.Header().Get("Content-Type"), bytes.Equal(rec.Body.Bytes(), src))
			}
		}
	}
}

func TestThumbnailGet_UseOriginalOnlyM(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	src := pngOf(t, 1000, 600, false)
	h.seedSource(id, src)

	m := h.get(thumbReq{id: id, version: testVersion, mime: "image/png", size: "m"})
	if m.Code != http.StatusOK || !bytes.Equal(m.Body.Bytes(), src) || m.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("m must be the source: %d %q", m.Code, m.Header().Get("Content-Type"))
	}
	s := h.get(thumbReq{id: id, version: testVersion, mime: "image/png", size: "s"})
	if s.Code != http.StatusOK || bytes.Equal(s.Body.Bytes(), src) {
		t.Fatalf("s must be rendered: %d", s.Code)
	}
	if w, hh, _ := decodeInfo(t, s.Body.Bytes()); w != 320 || hh != 192 {
		t.Errorf("s = %dx%d, want 320x192", w, hh)
	}
}

func TestThumbnailGet_UseOriginalRowsHaveNoBlob(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.seedSource(id, pngOf(t, 1000, 600, false))

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/png", size: "m"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	rows := h.pair(id, testVersion)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want only the requested size written", rows)
	}
	m := rows[db.SizeM]
	if m.Status != db.StatusReady || !m.Original || m.BlobID != nil || m.ContentType == nil || *m.ContentType != "image/png" || m.Kind != "image" {
		t.Errorf("M row = %+v", m)
	}
	if n := len(h.store.putKeys()); n != 0 {
		t.Errorf("blobs written = %d, an original row needs none", n)
	}
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/png", size: "s"}); rec.Code != http.StatusOK {
		t.Fatalf("s status %d", rec.Code)
	}
	if s := h.pair(id, testVersion)[db.SizeS]; s == nil || s.Status != db.StatusReady || s.Original || s.BlobID == nil {
		t.Errorf("S row = %+v, want a stored rendition written by the s request", s)
	}
	if n := len(h.store.putKeys()); n != 1 {
		t.Errorf("blobs written = %d, only S needs one", n)
	}
}

func TestThumbnailGet_PNGOnlyWhenAlphaIsUsed(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	alpha, opaque := newID(), newID()
	h.seedSource(alpha, pngOf(t, 3000, 2000, true))
	h.seedSource(opaque, pngOf(t, 3000, 2000, false))
	for size, side := range map[string]int{"s": 320, "m": 1280} {
		rec := h.get(thumbReq{id: alpha, version: testVersion, mime: "image/png", size: size})
		w, _, f := decodeInfo(t, rec.Body.Bytes())
		if rec.Code != 200 || f != "png" || w != side || rec.Header().Get("Content-Type") != "image/png" {
			t.Errorf("alpha %s: %d %s %d", size, rec.Code, f, w)
		}
		rec = h.get(thumbReq{id: opaque, version: testVersion, mime: "image/png", size: size})
		w, _, f = decodeInfo(t, rec.Body.Bytes())
		if rec.Code != 200 || f != "jpeg" || w != side || rec.Header().Get("Content-Type") != "image/jpeg" {
			t.Errorf("opaque %s: %d %s %d", size, rec.Code, f, w)
		}
	}
}

func TestThumbnailGet_NonEligibleFormatsAreConvertedAtSourceResolution(t *testing.T) {
	realVips(t)
	for _, tc := range []struct{ ext, mime string }{
		{"webp", "image/webp"},
		{"tiff", "image/tiff"},
		{"avif", "image/avif"},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			h := newHarness(t, harnessOpts{noDB: true})
			id := newID()
			src := ffmpegImage(t, tc.ext, 200, 100)
			h.seedSource(id, src)
			for _, size := range []string{"s", "m"} {
				rec := h.get(thumbReq{id: id, version: testVersion, mime: tc.mime, size: size})
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", size, rec.Code, rec.Body)
				}
				w, hh, f := decodeInfo(t, rec.Body.Bytes())
				if f != "jpeg" && f != "png" {
					t.Errorf("%s: format %s", size, f)
				}
				if w != 200 || hh != 100 {
					t.Errorf("%s: %dx%d, want the source 200x100 (never upscaled)", size, w, hh)
				}
				if bytes.Equal(rec.Body.Bytes(), src) {
					t.Errorf("%s: the source must not be served as is", size)
				}
			}
		})
	}
}

func TestThumbnailGet_WebPBelowTargetIsConvertedAndStored(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.seedSource(id, ffmpegImage(t, "webp", 200, 100))

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/webp", size: "m"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	rows := h.pair(id, testVersion)
	if r := rows[db.SizeM]; len(rows) != 1 || r == nil || r.Original || r.BlobID == nil || r.Status != db.StatusReady || r.Kind != "image" {
		t.Errorf("rows = %+v, want only m as a stored conversion", rows)
	}
	if n := len(h.store.putKeys()); n != 1 {
		t.Errorf("blobs written = %d, want 1", n)
	}
}

func TestThumbnailGet_BMPIs415(t *testing.T) {
	h := newHarness(t, harnessOpts{noDB: true})
	rec := h.get(thumbReq{id: newID(), version: 1, mime: "image/bmp"})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", rec.Code)
	}
}

func TestThumbnailGet_PDFRendersEachSizeFromPage1(t *testing.T) {
	var sides []int
	prev := pdfThumbFunc
	pdfThumbFunc = func(_ context.Context, _ []byte, side int) (render.Rendition, error) {
		sides = append(sides, side)
		return stubFor(side), nil
	}
	t.Cleanup(func() { pdfThumbFunc = prev })
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("%PDF-fake"))

	for size, want := range map[string]render.Rendition{"m": stubM, "s": stubS} {
		rec := h.get(thumbReq{id: id, version: testVersion, mime: "application/pdf", size: size})
		if rec.Code != http.StatusOK || rec.Body.String() != string(want.Body) || rec.Header().Get("Content-Type") != want.ContentType {
			t.Errorf("%s: %d %q %q", size, rec.Code, rec.Body, rec.Header().Get("Content-Type"))
		}
	}
	if len(sides) != 2 || h.sourceReads() != 2 {
		t.Errorf("renders=%v source reads=%d, want one render and one read per requested size", sides, h.sourceReads())
	}
}

func TestThumbnailGet_PDFPage1Sizes(t *testing.T) {
	testsupport.InitRealRender(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, testsupport.LoadFixture(testsupport.FixturePDF))
	for size, side := range map[string]int{"m": 1280, "s": 320} {
		rec := h.get(thumbReq{id: id, version: testVersion, mime: "application/pdf", size: size})
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("%s: %d %q %s", size, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
		w, hh, _ := decodeInfo(t, rec.Body.Bytes())
		if max(w, hh) != side {
			t.Errorf("%s = %dx%d, want long side %d", size, w, hh, side)
		}
	}
}

func TestThumbnailGet_PDFRenderFailureIs400(t *testing.T) {
	prev := pdfThumbFunc
	pdfThumbFunc = func(context.Context, []byte, int) (render.Rendition, error) { return render.Rendition{}, os.ErrInvalid }
	t.Cleanup(func() { pdfThumbFunc = prev })
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("x"))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "application/pdf"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestThumbnailGet_ImageRenderFailureIs400AndSource404Is404(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, []byte("not an image"))
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/webp"}); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage: %d, want 400", rec.Code)
	}
	if rec := h.get(thumbReq{id: newID(), version: testVersion, mime: "image/jpeg"}); rec.Code != http.StatusNotFound {
		t.Errorf("missing source: %d, want 404", rec.Code)
	}
}

func TestThumbnailGet_NoDBServesWithoutPersisting(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.seedSource(id, jpegOf(t, 2000, 1000))
	for _, size := range []string{"s", "m"} {
		if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", size: size}); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", size, rec.Code)
		}
	}
	if len(h.store.putKeys()) != 0 || h.store.blobCount() != 1 {
		t.Errorf("DB-less path wrote to storages: puts=%d blobs=%d", len(h.store.putKeys()), h.store.blobCount())
	}
}

func TestThumbnailGet_CacheKeyScopedByOwner(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	src := jpegOf(t, 2000, 1000)
	h.store.seed(id, testVersion, "files", "owner-1", src)
	h.store.seed(id, testVersion, "files", "owner-2", src)
	for _, o := range []string{"owner-1", "owner-2", "owner-1", "owner-2"} {
		if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", owner: o}); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", o, rec.Code)
		}
	}
	if n := h.sourceReads(); n != 2 {
		t.Errorf("source reads = %d, want one per owner", n)
	}
}

func TestThumbnailGet_CacheDisabledAlwaysFetches(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true, cfg: func(_ *testing.T, d Deps) Deps {
		d.Cache = cache.New(0)
		return d
	}})
	id := newID()
	h.seedSource(id, jpegOf(t, 2000, 1000))
	for i := 0; i < 3; i++ {
		if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg"}); rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if n := h.sourceReads(); n != 3 {
		t.Errorf("source reads = %d, want 3 with the cache disabled", n)
	}
}

func TestThumbnailGet_VersionIsPartOfTheCacheKey(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noDB: true})
	id := newID()
	h.store.seed(id, 1, "files", testOwner, jpegOf(t, 2000, 1000))
	h.store.seed(id, 2, "files", testOwner, jpegOf(t, 1000, 2000))
	a := h.get(thumbReq{id: id, version: 1, mime: "image/jpeg"})
	b := h.get(thumbReq{id: id, version: 2, mime: "image/jpeg"})
	wa, _, _ := decodeInfo(t, a.Body.Bytes())
	wb, _, _ := decodeInfo(t, b.Body.Bytes())
	if wa != 1280 || wb != 640 {
		t.Errorf("widths %d,%d: versions share a cache entry", wa, wb)
	}
}

func TestThumbnailGet_SyncPersistsBlobsUnderOwner(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	req := thumbReq{id: id, version: 3, mime: "image/jpeg", serviceType: "chats", owner: "owner-x"}
	h.store.seed(id, 3, "chats", "owner-x", jpegOf(t, 3000, 2000))

	first := h.get(req)
	if first.Code != http.StatusOK {
		t.Fatalf("status %d", first.Code)
	}
	rows := h.pair(id, 3)
	if len(rows) != 1 || rows[db.SizeM] == nil {
		t.Fatalf("rows = %+v, want only the requested m", rows)
	}
	for size, r := range rows {
		if r.Status != db.StatusReady || r.BlobID == nil || r.OwnerID != "owner-x" || r.ServiceType != "chats" || r.Version != 3 {
			t.Errorf("row %s = %+v", size, r)
		}
		if !h.store.has(*r.BlobID, 3, "chats", "owner-x") {
			t.Errorf("blob of %s missing under the owner and source version", size)
		}
	}
	for _, k := range h.store.putKeys() {
		if k.Version != 3 || k.ServiceType != "chats" || k.Owner != "owner-x" {
			t.Errorf("blob written as %+v", k)
		}
	}

	h.deps.Cache.Delete(h.thumbKeyOf(req, "m"))
	before := h.sourceReads()
	again := h.get(req)
	if again.Code != http.StatusOK || !bytes.Equal(again.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("second GET: %d identical=%v", again.Code, bytes.Equal(again.Body.Bytes(), first.Body.Bytes()))
	}
	reads := h.store.gets[before:]
	if len(reads) != 1 || reads[0].Node != *rows[db.SizeM].BlobID {
		t.Errorf("second GET read %+v, want only the stored M blob", reads)
	}
}

func TestThumbnailGet_SyncDigestMismatchReplacesAndDeletesOldBlob(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.seedSource(id, jpegOf(t, 3000, 2000))

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", digest: "d1"}); rec.Code != http.StatusOK {
		t.Fatalf("d1: %d", rec.Code)
	}
	old := h.pair(id, testVersion)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", digest: "d2"}); rec.Code != http.StatusOK {
		t.Fatalf("d2: %d", rec.Code)
	}
	now := h.pair(id, testVersion)
	for size, r := range now {
		if r.Digest == nil || *r.Digest != "d2" || *r.BlobID == *old[size].BlobID {
			t.Errorf("row %s not replaced: %+v", size, r)
		}
		if h.blobStatus(*old[size].BlobID) != "TOMBSTONE" {
			t.Errorf("old blob of %s is not tombstoned", size)
		}
		if !h.store.has(*r.BlobID, testVersion, "files", testOwner) {
			t.Errorf("new blob of %s missing", size)
		}
	}
	h.purge()
	if n := h.store.blobCount(); n != 2 {
		t.Errorf("blobs after the purge = %d, want source + 1", n)
	}
}

func TestThumbnailGet_SyncBlobMissingRegenerates(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	req := thumbReq{id: id, version: testVersion, mime: "image/jpeg"}
	h.seedSource(id, jpegOf(t, 3000, 2000))
	if rec := h.get(req); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	lost := *h.pair(id, testVersion)[db.SizeM].BlobID
	h.store.mu.Lock()
	delete(h.store.blobs, memKey{lost, testVersion, "files", testOwner})
	h.store.mu.Unlock()
	h.deps.Cache.Delete(h.thumbKeyOf(req, "m"))

	rec := h.get(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	now := *h.pair(id, testVersion)[db.SizeM].BlobID
	if now == lost || !h.store.has(now, testVersion, "files", testOwner) {
		t.Errorf("blob not regenerated: %s -> %s", lost, now)
	}
}

func TestThumbnailGet_SyncConcurrentWritersKeepOneBlob(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.seedSource(id, jpegOf(t, 3000, 2000))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(size string) {
			defer wg.Done()
			if rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", size: size}); rec.Code != http.StatusOK {
				t.Errorf("%s: %d", size, rec.Code)
			}
		}([]string{"s", "m"}[i%2])
	}
	wg.Wait()

	rows := h.pair(id, testVersion)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for size, r := range rows {
		if r.BlobID == nil || !h.store.has(*r.BlobID, testVersion, "files", testOwner) {
			t.Errorf("row %s references a missing blob", size)
		}
	}
	if n := h.store.blobCount(); n != 3 {
		t.Errorf("blobs = %d, want the source and exactly one per row (losers delete theirs)", n)
	}
	if puts, dels := len(h.store.putKeys()), len(h.store.deletedKeys()); puts-dels != 2 {
		t.Errorf("puts=%d deletes=%d, want a net of 2", puts, dels)
	}
}

func TestThumbnailGet_UploadFailureStillServes(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{})
	id := newID()
	req := thumbReq{id: id, version: testVersion, mime: "image/jpeg"}
	h.seedSource(id, jpegOf(t, 3000, 2000))
	h.store.putErr = func(memKey) error { return os.ErrDeadlineExceeded }

	rec := h.get(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want the generated thumbnail served", rec.Code)
	}
	if rows := h.pair(id, testVersion); len(rows) != 0 {
		t.Errorf("rows = %+v, a size whose upload failed must not be persisted", rows)
	}
	if rec := h.get(req); rec.Code != http.StatusOK {
		t.Errorf("served again from the cache: %d", rec.Code)
	}
}
