// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

/*
#cgo pkg-config: vips
#include <vips/vips.h>
#include <stdlib.h>

#define TB_PNG_COMPRESSION 6

typedef struct {
	void *buf;
	size_t len;
	int png;
	int w;
	int h;
} tb_out;

static int tb_encode(VipsImage *img, int q, tb_out *o) {
	int rc;
	o->png = 0;
	o->buf = NULL;
	o->len = 0;
	o->w = img->Xsize;
	o->h = img->Ysize;
	if (vips_image_hasalpha(img)) {
		VipsImage *a = NULL;
		double mn = 255;
		if (vips_extract_band(img, &a, img->Bands - 1, NULL)) return -1;
		rc = vips_min(a, &mn, NULL);
		g_object_unref(a);
		if (rc) return -1;
		if (mn < 255) {
			o->png = 1;
			return vips_pngsave_buffer(img, &o->buf, &o->len, "compression", TB_PNG_COMPRESSION, "keep", VIPS_FOREIGN_KEEP_NONE, NULL);
		}
		VipsImage *flat = NULL;
		VipsArrayDouble *bg = vips_array_double_newv(1, 255.0);
		rc = vips_flatten(img, &flat, "background", bg, NULL);
		vips_area_unref(VIPS_AREA(bg));
		if (rc) return -1;
		rc = vips_jpegsave_buffer(flat, &o->buf, &o->len, "Q", q, "keep", VIPS_FOREIGN_KEEP_NONE, NULL);
		g_object_unref(flat);
		return rc;
	}
	return vips_jpegsave_buffer(img, &o->buf, &o->len, "Q", q, "keep", VIPS_FOREIGN_KEEP_NONE, NULL);
}

static int tb_probe(void *buf, size_t len, int *w, int *h, int *pages, int *orientation, const char **loader) {
	VipsImage *img = vips_image_new_from_buffer(buf, len, "", "access", VIPS_ACCESS_SEQUENTIAL, NULL);
	if (!img) return -1;
	*w = img->Xsize;
	*h = vips_image_get_page_height(img);
	*pages = vips_image_get_n_pages(img);
	*orientation = vips_image_get_orientation(img);
	*loader = NULL;
	vips_image_get_string(img, VIPS_META_LOADER, loader);
	if (*loader) *loader = g_strdup(*loader);
	g_object_unref(img);
	return 0;
}

static int tb_pair_from(VipsImage *m, int qm, int qs, int side_s, tb_out *om, tb_out *os) {
	VipsImage *s = NULL;
	if (tb_encode(m, qm, om)) return -1;
	if (vips_thumbnail_image(m, &s, side_s, "height", side_s, "size", VIPS_SIZE_DOWN, NULL)) return -1;
	int rc = tb_encode(s, qs, os);
	g_object_unref(s);
	return rc;
}

static int tb_pair(void *buf, size_t len, int side_m, int side_s, int qm, int qs, tb_out *om, tb_out *os) {
	VipsImage *t = NULL;
	if (vips_thumbnail_buffer(buf, len, &t, side_m, "height", side_m, "size", VIPS_SIZE_DOWN, NULL)) return -1;
	VipsImage *m = vips_image_copy_memory(t);
	g_object_unref(t);
	if (!m) return -1;
	int rc = tb_pair_from(m, qm, qs, side_s, om, os);
	g_object_unref(m);
	return rc;
}

static int tb_pair_rgba(void *px, size_t size, int iw, int ih, int side_m, int side_s, int qm, int qs, tb_out *om, tb_out *os) {
	VipsImage *raw = vips_image_new_from_memory(px, size, iw, ih, 4, VIPS_FORMAT_UCHAR);
	if (!raw) return -1;
	VipsImage *c = NULL;
	if (vips_copy(raw, &c, "interpretation", VIPS_INTERPRETATION_sRGB, NULL)) { g_object_unref(raw); return -1; }
	g_object_unref(raw);
	VipsImage *flat = NULL;
	VipsArrayDouble *bg = vips_array_double_newv(1, 255.0);
	int rc = vips_flatten(c, &flat, "background", bg, NULL);
	vips_area_unref(VIPS_AREA(bg));
	g_object_unref(c);
	if (rc) return -1;
	VipsImage *t = NULL;
	rc = vips_thumbnail_image(flat, &t, side_m, "height", side_m, "size", VIPS_SIZE_DOWN, NULL);
	g_object_unref(flat);
	if (rc) return -1;
	VipsImage *m = vips_image_copy_memory(t);
	g_object_unref(t);
	if (!m) return -1;
	rc = tb_pair_from(m, qm, qs, side_s, om, os);
	g_object_unref(m);
	return rc;
}

static int tb_full(void *buf, size_t len, int q, tb_out *o) {
	VipsImage *t = NULL;
	if (vips_thumbnail_buffer(buf, len, &t, VIPS_MAX_COORD, "height", VIPS_MAX_COORD, "size", VIPS_SIZE_DOWN, NULL)) return -1;
	VipsImage *m = vips_image_copy_memory(t);
	g_object_unref(t);
	if (!m) return -1;
	int rc = tb_encode(m, q, o);
	g_object_unref(m);
	return rc;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"unsafe"
)

const (
	SideS = 320
	SideM = 1280

	qualityS       = 50
	qualityM       = 80
	previewQuality = 80
)

type Info struct {
	Width, Height, Pages int
	Orientation          int
	Loader               string
}

// Original reports whether the source can be served as is: JPEG, PNG or GIF content (whatever its declared type) that needs no rotation.
func (i Info) Original() bool {
	if i.Orientation > 1 {
		return false
	}
	return strings.HasPrefix(i.Loader, "jpegload") || strings.HasPrefix(i.Loader, "pngload") || strings.HasPrefix(i.Loader, "gifload")
}

// IsGIF reports whether the libvips loader read the source as GIF.
func (i Info) IsGIF() bool { return strings.HasPrefix(i.Loader, "gifload") }

type Rendition struct {
	Body        []byte
	ContentType string
	Width       int
	Height      int
}

type Pair struct{ S, M Rendition }

func vipsError(op string) error {
	msg := C.GoString(C.vips_error_buffer())
	C.vips_error_clear()
	return fmt.Errorf("vips %s: %s", op, msg)
}

func takeRendition(o *C.tb_out) Rendition {
	ct := "image/jpeg"
	if o.png != 0 {
		ct = "image/png"
	}
	body := C.GoBytes(o.buf, C.int(o.len))
	C.g_free(C.gpointer(o.buf))
	o.buf = nil
	return Rendition{Body: body, ContentType: ct, Width: int(o.w), Height: int(o.h)}
}

func freeOut(o *C.tb_out) {
	if o.buf != nil {
		C.g_free(C.gpointer(o.buf))
		o.buf = nil
	}
}

func Probe(data []byte) (Info, error) {
	if len(data) == 0 {
		return Info{}, errors.New("empty input buffer")
	}
	var w, h, pages, orientation C.int
	var loader *C.char
	if C.tb_probe(unsafe.Pointer(&data[0]), C.size_t(len(data)), &w, &h, &pages, &orientation, &loader) != 0 {
		return Info{}, vipsError("probe")
	}
	name := ""
	if loader != nil {
		name = C.GoString(loader)
		C.g_free(C.gpointer(loader))
	}
	return Info{Width: int(w), Height: int(h), Pages: int(pages), Orientation: int(orientation), Loader: name}, nil
}

func ImagePair(data []byte) (Pair, error) {
	if len(data) == 0 {
		return Pair{}, errors.New("empty input buffer")
	}
	var om, os C.tb_out
	rc := C.tb_pair(unsafe.Pointer(&data[0]), C.size_t(len(data)), SideM, SideS, qualityM, qualityS, &om, &os)
	if rc != 0 {
		freeOut(&om)
		freeOut(&os)
		return Pair{}, vipsError("pair")
	}
	return Pair{M: takeRendition(&om), S: takeRendition(&os)}, nil
}

func FullImage(data []byte) (Rendition, error) {
	if len(data) == 0 {
		return Rendition{}, errors.New("empty input buffer")
	}
	var o C.tb_out
	if C.tb_full(unsafe.Pointer(&data[0]), C.size_t(len(data)), previewQuality, &o) != 0 {
		freeOut(&o)
		return Rendition{}, vipsError("full")
	}
	return takeRendition(&o), nil
}

func rgbaPair(img *image.RGBA) (Pair, error) {
	if img == nil {
		return Pair{}, errors.New("nil RGBA image")
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return Pair{}, errors.New("empty RGBA image")
	}
	packed := img.Pix
	if img.Stride != w*4 {
		packed = make([]byte, w*h*4)
		for y := 0; y < h; y++ {
			off := img.PixOffset(img.Bounds().Min.X, img.Bounds().Min.Y+y)
			copy(packed[y*w*4:(y+1)*w*4], img.Pix[off:off+w*4])
		}
	}
	var om, os C.tb_out
	rc := C.tb_pair_rgba(unsafe.Pointer(&packed[0]), C.size_t(len(packed)), C.int(w), C.int(h), SideM, SideS, qualityM, qualityS, &om, &os)
	runtime.KeepAlive(packed)
	if rc != 0 {
		freeOut(&om)
		freeOut(&os)
		return Pair{}, vipsError("pair_rgba")
	}
	return Pair{M: takeRendition(&om), S: takeRendition(&os)}, nil
}
