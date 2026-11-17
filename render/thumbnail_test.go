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

func TestMain(m *testing.M) {
	if err := InitVips("carbonio-preview-test"); err != nil {
		panic("InitVips failed: " + err.Error())
	}
	m.Run()
}

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
	return genGIFRate(t, w, h, frames, 10)
}

func genGIFRate(t *testing.T, w, h, frames, rate int) []byte {
	t.Helper()
	resolveFFmpeg(t)
	out := filepath.Join(t.TempDir(), "in.gif")
	cmd := exec.Command(video.FFmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size="+strconv.Itoa(w)+"x"+strconv.Itoa(h)+":rate="+strconv.Itoa(rate),
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

var thumbSides = []int{SideS, SideM}

func thumb(t *testing.T, src []byte, side int) Rendition {
	t.Helper()
	r, err := ImageThumbnail(src, side)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestImageThumbnail_LongSides(t *testing.T) {
	src := encodeJPEG(t, noisyRGBA(6000, 4000))
	want := map[int][2]int{SideM: {1280, 853}, SideS: {320, 213}}
	for _, side := range thumbSides {
		r := thumb(t, src, side)
		if r.Width != want[side][0] || r.Height != want[side][1] {
			t.Fatalf("side %d: %dx%d, want %dx%d", side, r.Width, r.Height, want[side][0], want[side][1])
		}
		if r.ContentType != "image/jpeg" || !isJPEGBytes(r.Body) {
			t.Fatalf("rendition not JPEG: %s", r.ContentType)
		}
		if w, h := dims(t, r.Body); w != r.Width || h != r.Height {
			t.Fatalf("declared %dx%d, decoded %dx%d", r.Width, r.Height, w, h)
		}
	}
}

func TestImageThumbnail_NeverUpscales(t *testing.T) {
	src := encodeJPEG(t, noisyRGBA(200, 100))
	for _, side := range thumbSides {
		if r := thumb(t, src, side); r.Width != 200 || r.Height != 100 {
			t.Fatalf("side %d: %dx%d, want 200x100", side, r.Width, r.Height)
		}
	}
}

func TestImageThumbnail_AlphaUsedIsPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 10, G: 200, B: 30, A: 255}), image.Point{}, draw.Src)
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetNRGBA(x, y, color.NRGBA{A: 0})
		}
	}
	src := encodePNG(t, img)
	for _, side := range thumbSides {
		if r := thumb(t, src, side); r.ContentType != "image/png" || !isPNGBytes(r.Body) {
			t.Fatalf("side %d: want PNG, got %s", side, r.ContentType)
		}
	}
}

func TestImageThumbnail_OpaqueAlphaIsJPEGOnWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 20, G: 40, B: 60, A: 255}), image.Point{}, draw.Src)
	src := encodePNG(t, img)
	for _, side := range thumbSides {
		if r := thumb(t, src, side); r.ContentType != "image/jpeg" || !isJPEGBytes(r.Body) {
			t.Fatalf("side %d: want JPEG, got %s", side, r.ContentType)
		}
	}
}

func TestImageThumbnail_FullyTransparentPixelFlattensToWhite(t *testing.T) {
	rgba := image.NewRGBA(image.Rect(0, 0, 400, 300))
	r, err := rgbaThumbnail(rgba, SideM)
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentType != "image/jpeg" {
		t.Fatalf("rgbaThumbnail must flatten to JPEG, got %s", r.ContentType)
	}
	img, err := jpeg.Decode(bytes.NewReader(r.Body))
	if err != nil {
		t.Fatal(err)
	}
	cr, cg, cb, _ := img.At(10, 10).RGBA()
	if cr>>8 < 250 || cg>>8 < 250 || cb>>8 < 250 {
		t.Fatalf("transparent pixel is %d,%d,%d, want white", cr>>8, cg>>8, cb>>8)
	}
}

func TestImageThumbnail_StripsMetadata(t *testing.T) {
	src := spliceAPP1(t, encodeJPEG(t, noisyRGBA(2000, 1500)), exifOrientation(1))
	if len(jpegSegments(src)[0xE1]) == 0 {
		t.Fatal("fixture has no APP1")
	}
	for _, side := range thumbSides {
		segs := jpegSegments(thumb(t, src, side).Body)
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

func TestImageThumbnail_EXIFAutorotate(t *testing.T) {
	src := spliceAPP1(t, encodeJPEG(t, noisyRGBA(600, 300)), exifOrientation(6))
	r := thumb(t, src, SideM)
	if r.Width != 300 || r.Height != 600 {
		t.Fatalf("M=%dx%d, want portrait 300x600", r.Width, r.Height)
	}
	if w, h := dims(t, r.Body); w != 300 || h != 600 {
		t.Fatalf("decoded %dx%d", w, h)
	}
}

func TestImageThumbnail_QualityS50M80(t *testing.T) {
	src := encodeJPEG(t, noisyRGBA(2000, 1500))
	if s, m := lumaDQTSum(t, thumb(t, src, SideS).Body), lumaDQTSum(t, thumb(t, src, SideM).Body); s <= m {
		t.Fatalf("S quantisation sum %d must exceed M %d (Q50 vs Q80)", s, m)
	}
}

func TestImageThumbnail_SizeIsRenderedFromTheSource(t *testing.T) {
	src := encodeJPEG(t, noisyRGBA(5000, 3000))
	s := thumb(t, src, SideS)
	if s.Width != 320 || s.Height != 192 {
		t.Fatalf("S = %dx%d, want 320x192", s.Width, s.Height)
	}
	if again := thumb(t, src, SideS); !bytes.Equal(again.Body, s.Body) {
		t.Fatal("S from the source must be deterministic")
	}
	if derived := thumb(t, thumb(t, src, SideM).Body, SideS); bytes.Equal(derived.Body, s.Body) {
		t.Fatal("S straight from the source must differ from S derived from M")
	}
}

func TestImageThumbnail_InvalidInput(t *testing.T) {
	if _, err := ImageThumbnail([]byte("garbage"), SideM); err == nil {
		t.Fatal("want error")
	}
	if _, err := ImageThumbnail(nil, SideM); err == nil {
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

func TestProbe_Loader(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		gif  bool
	}{
		{"jpeg", encodeJPEG(t, noisyRGBA(300, 200)), false},
		{"png", encodePNG(t, noisyRGBA(300, 200)), false},
		{"gif", genGIF(t, 160, 90, 12), true},
	}
	for _, c := range cases {
		info, err := Probe(c.data)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if info.Loader == "" || info.IsGIF() != c.gif {
			t.Errorf("%s: info=%+v, want gif=%v", c.name, info, c.gif)
		}
	}
}
