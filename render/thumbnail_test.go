// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/video"
)

func noisyRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 13), B: uint8((x ^ y) * 5), A: 255})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func dims(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg.Width, cfg.Height
}

func isJPEGBytes(b []byte) bool { return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 }
func isPNGBytes(b []byte) bool  { return len(b) > 8 && string(b[1:4]) == "PNG" }

func spliceAPP1(t *testing.T, jpg []byte, payload []byte) []byte {
	t.Helper()
	seg := make([]byte, 4, 4+len(payload))
	seg[0], seg[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func exifOrientation(o uint16) []byte {
	var b bytes.Buffer
	b.WriteString("Exif\x00\x00")
	b.WriteString("MM\x00\x2a")
	_ = binary.Write(&b, binary.BigEndian, uint32(8))
	_ = binary.Write(&b, binary.BigEndian, uint16(1))
	_ = binary.Write(&b, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&b, binary.BigEndian, uint16(3))
	_ = binary.Write(&b, binary.BigEndian, uint32(1))
	_ = binary.Write(&b, binary.BigEndian, o)
	_ = binary.Write(&b, binary.BigEndian, uint16(0))
	_ = binary.Write(&b, binary.BigEndian, uint32(0))
	return b.Bytes()
}

func jpegSegments(b []byte) map[byte][][]byte {
	out := map[byte][][]byte{}
	i := 2
	for i+4 <= len(b) && b[i] == 0xFF {
		m := b[i+1]
		if m == 0xDA {
			break
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		out[m] = append(out[m], b[i+4:i+2+n])
		i += 2 + n
	}
	return out
}

func lumaDQTSum(t *testing.T, b []byte) int {
	t.Helper()
	for _, seg := range jpegSegments(b)[0xDB] {
		if seg[0]&0x0F == 0 {
			sum := 0
			if seg[0]>>4 == 0 {
				for _, v := range seg[1:65] {
					sum += int(v)
				}
			} else {
				for i := 0; i < 64; i++ {
					sum += int(binary.BigEndian.Uint16(seg[1+2*i:]))
				}
			}
			return sum
		}
	}
	t.Fatal("no luminance DQT")
	return 0
}

func resolveFFmpeg(t *testing.T) {
	t.Helper()
	prev := video.FFmpegPath
	if _, err := os.Stat(prev); err != nil {
		p, lerr := exec.LookPath("ffmpeg")
		if lerr != nil {
			t.Skip("ffmpeg not available")
		}
		video.FFmpegPath = p
		t.Cleanup(func() { video.FFmpegPath = prev })
	}
}

func genGIF(t *testing.T, w, h, frames int) []byte {
	t.Helper()
	resolveFFmpeg(t)
	out := filepath.Join(t.TempDir(), "in.gif")
	cmd := exec.Command(video.FFmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size="+strconv.Itoa(w)+"x"+strconv.Itoa(h)+":rate=10",
		"-frames:v", strconv.Itoa(frames), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProbe_DimensionsAndPages(t *testing.T) {
	info, err := Probe(encodeJPEG(t, noisyRGBA(300, 200)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 300 || info.Height != 200 || info.Pages != 1 {
		t.Fatalf("info=%+v, want 300x200 with 1 page", info)
	}
	g := genGIF(t, 160, 90, 12)
	gi, err := Probe(g)
	if err != nil {
		t.Fatal(err)
	}
	if gi.Pages <= 1 || gi.Width != 160 || gi.Height != 90 {
		t.Fatalf("gif info=%+v, want 160x90 with several pages", gi)
	}
}

func TestProbe_NotAnImage(t *testing.T) {
	if _, err := Probe([]byte("not an image at all")); err == nil {
		t.Fatal("want error")
	}
	if _, err := Probe(nil); err == nil {
		t.Fatal("want error on empty input")
	}
}

func TestImagePair_LongSides(t *testing.T) {
	p, err := ImagePair(encodeJPEG(t, noisyRGBA(6000, 4000)))
	if err != nil {
		t.Fatal(err)
	}
	if p.M.Width != 1280 || p.M.Height != 853 || p.S.Width != 320 || p.S.Height != 213 {
		t.Fatalf("M=%dx%d S=%dx%d", p.M.Width, p.M.Height, p.S.Width, p.S.Height)
	}
	for _, r := range []Rendition{p.M, p.S} {
		if r.ContentType != "image/jpeg" || !isJPEGBytes(r.Body) {
			t.Fatalf("rendition not JPEG: %s", r.ContentType)
		}
		if w, h := dims(t, r.Body); w != r.Width || h != r.Height {
			t.Fatalf("declared %dx%d, decoded %dx%d", r.Width, r.Height, w, h)
		}
	}
}

func TestImagePair_NeverUpscales(t *testing.T) {
	p, err := ImagePair(encodeJPEG(t, noisyRGBA(200, 100)))
	if err != nil {
		t.Fatal(err)
	}
	if p.M.Width != 200 || p.M.Height != 100 || p.S.Width != 200 || p.S.Height != 100 {
		t.Fatalf("M=%dx%d S=%dx%d, want 200x100 both", p.M.Width, p.M.Height, p.S.Width, p.S.Height)
	}
}

func TestImagePair_AlphaUsedIsPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 10, G: 200, B: 30, A: 255}), image.Point{}, draw.Src)
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetNRGBA(x, y, color.NRGBA{A: 0})
		}
	}
	p, err := ImagePair(encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []Rendition{p.M, p.S} {
		if r.ContentType != "image/png" || !isPNGBytes(r.Body) {
			t.Fatalf("want PNG, got %s", r.ContentType)
		}
	}
}

func TestImagePair_OpaqueAlphaIsJPEGOnWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 20, G: 40, B: 60, A: 255}), image.Point{}, draw.Src)
	p, err := ImagePair(encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	if p.M.ContentType != "image/jpeg" || !isJPEGBytes(p.M.Body) || p.S.ContentType != "image/jpeg" {
		t.Fatalf("want JPEG, got %s / %s", p.M.ContentType, p.S.ContentType)
	}
}

func TestImagePair_FullyTransparentPixelFlattensToWhite(t *testing.T) {
	rgba := image.NewRGBA(image.Rect(0, 0, 400, 300))
	pair, err := rgbaPair(rgba)
	if err != nil {
		t.Fatal(err)
	}
	if pair.M.ContentType != "image/jpeg" {
		t.Fatalf("rgbaPair must flatten to JPEG, got %s", pair.M.ContentType)
	}
	img, err := jpeg.Decode(bytes.NewReader(pair.M.Body))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(10, 10).RGBA()
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Fatalf("transparent pixel is %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
}

func TestImagePair_StripsMetadata(t *testing.T) {
	src := spliceAPP1(t, encodeJPEG(t, noisyRGBA(2000, 1500)), exifOrientation(1))
	if len(jpegSegments(src)[0xE1]) == 0 {
		t.Fatal("fixture has no APP1")
	}
	p, err := ImagePair(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []Rendition{p.M, p.S} {
		segs := jpegSegments(r.Body)
		if len(segs[0xE1]) != 0 {
			t.Fatal("APP1/EXIF survived")
		}
		for _, s := range segs[0xE2] {
			if bytes.HasPrefix(s, []byte("ICC_PROFILE")) {
				t.Fatal("ICC profile survived")
			}
		}
	}
}

func TestImagePair_EXIFAutorotate(t *testing.T) {
	src := spliceAPP1(t, encodeJPEG(t, noisyRGBA(600, 300)), exifOrientation(6))
	p, err := ImagePair(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.M.Width != 300 || p.M.Height != 600 {
		t.Fatalf("M=%dx%d, want portrait 300x600", p.M.Width, p.M.Height)
	}
	if w, h := dims(t, p.M.Body); w != 300 || h != 600 {
		t.Fatalf("decoded %dx%d", w, h)
	}
}

func TestImagePair_QualityS50M80(t *testing.T) {
	p, err := ImagePair(encodeJPEG(t, noisyRGBA(2000, 1500)))
	if err != nil {
		t.Fatal(err)
	}
	if s, m := lumaDQTSum(t, p.S.Body), lumaDQTSum(t, p.M.Body); s <= m {
		t.Fatalf("S quantisation sum %d must exceed M %d (Q50 vs Q80)", s, m)
	}
}

func TestImagePair_SFromM(t *testing.T) {
	p, err := ImagePair(encodeJPEG(t, noisyRGBA(5000, 3000)))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ImagePair(p.M.Body)
	if err != nil {
		t.Fatal(err)
	}
	if again.S.Width != p.S.Width || again.S.Height != p.S.Height {
		t.Fatalf("S %dx%d differs from S of M %dx%d", p.S.Width, p.S.Height, again.S.Width, again.S.Height)
	}
}

func TestImagePair_InvalidInput(t *testing.T) {
	if _, err := ImagePair([]byte("garbage")); err == nil {
		t.Fatal("want error")
	}
	if _, err := ImagePair(nil); err == nil {
		t.Fatal("want error on empty input")
	}
}

func TestFullImage_KeepsResolutionAndAutorotates(t *testing.T) {
	src := spliceAPP1(t, encodeJPEG(t, noisyRGBA(3000, 1000)), exifOrientation(6))
	r, err := FullImage(src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Width != 1000 || r.Height != 3000 || r.ContentType != "image/jpeg" {
		t.Fatalf("got %dx%d %s, want 1000x3000 image/jpeg", r.Width, r.Height, r.ContentType)
	}
	if len(jpegSegments(r.Body)[0xE1]) != 0 {
		t.Fatal("metadata survived")
	}
}

func TestFullImage_AlphaIsPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 500, 400))
	img.SetNRGBA(1, 1, color.NRGBA{R: 1, G: 2, B: 3, A: 10})
	r, err := FullImage(encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentType != "image/png" || !isPNGBytes(r.Body) || r.Width != 500 || r.Height != 400 {
		t.Fatalf("got %dx%d %s", r.Width, r.Height, r.ContentType)
	}
}
