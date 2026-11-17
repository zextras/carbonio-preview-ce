// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v4/db"
)

func exifOrientationSegment(o uint16) []byte {
	var b bytes.Buffer
	b.WriteString("Exif\x00\x00MM\x00\x2a")
	_ = binary.Write(&b, binary.BigEndian, uint32(8))
	_ = binary.Write(&b, binary.BigEndian, uint16(1))
	_ = binary.Write(&b, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&b, binary.BigEndian, uint16(3))
	_ = binary.Write(&b, binary.BigEndian, uint32(1))
	_ = binary.Write(&b, binary.BigEndian, o)
	_ = binary.Write(&b, binary.BigEndian, uint16(0))
	_ = binary.Write(&b, binary.BigEndian, uint32(0))
	payload := b.Bytes()
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func withEXIF(jpg []byte, o uint16) []byte {
	out := append([]byte{}, jpg[:2]...)
	out = append(out, exifOrientationSegment(o)...)
	return append(out, jpg[2:]...)
}

func TestThumbnailGet_SmallJPEGIsReEncodedAtItsSizeAndStored(t *testing.T) {
	realVips(t)
	for _, c := range []struct {
		size        string
		orientation uint16
		w, h        int
	}{{"s", 1, 300, 200}, {"m", 1, 300, 200}, {"s", 6, 200, 300}, {"m", 6, 200, 300}} {
		size := c.size
		t.Run(size+"_orientation_"+string(rune('0'+c.orientation)), func(t *testing.T) {
			h := newHarness(t, harnessOpts{noWorker: true})
			id := newID()
			src := withEXIF(jpegOf(t, 300, 200), c.orientation)
			h.seedSource(id, src)

			rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/jpeg", size: size})
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
				t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
			}
			if bytes.Equal(rec.Body.Bytes(), src) {
				t.Fatal("the source must never be served as the thumbnail")
			}
			if w, hh, _ := decodeInfo(t, rec.Body.Bytes()); w != c.w || hh != c.h {
				t.Errorf("%dx%d, want %dx%d (EXIF applied, never upscaled)", w, hh, c.w, c.h)
			}
			if bytes.Contains(rec.Body.Bytes(), []byte("Exif")) {
				t.Error("metadata must be stripped")
			}
			r := h.pair(id, testVersion)[size]
			if r == nil || r.Status != db.StatusReady || r.BlobID == nil {
				t.Fatalf("row = %+v, want READY with a stored blob", r)
			}
			if blob, err := h.store.get(*r.BlobID, testVersion, "files", testOwner); err != nil || !bytes.Equal(blob, rec.Body.Bytes()) {
				t.Errorf("stored blob differs from the response: %v", err)
			}
		})
	}
}

func TestThumbnailGet_SmallPNGWithAlphaStaysPNGAtItsSize(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, pngOf(t, 300, 200, true))
	rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/png", size: "s"})
	w, hh, f := decodeInfo(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || f != "png" || rec.Header().Get("Content-Type") != "image/png" || w != 300 || hh != 200 {
		t.Fatalf("%d %s %dx%d, want a PNG at 300x200", rec.Code, f, w, hh)
	}
	if r := h.pair(id, testVersion)[db.SizeS]; r == nil || r.BlobID == nil {
		t.Errorf("row = %+v, want a stored blob", r)
	}
}

func TestThumbnailGet_StaticGIFIsAJPEGOrPNGNeverTheSource(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	src := gifOf(t, 200, 100, 1)
	h.seedSource(id, src)
	rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/gif", size: "m"})
	_, _, f := decodeInfo(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || (f != "jpeg" && f != "png") || bytes.Equal(rec.Body.Bytes(), src) {
		t.Fatalf("%d %s, want a re-encoded static frame", rec.Code, f)
	}
	if r := h.pair(id, testVersion)[db.SizeM]; r == nil || r.BlobID == nil {
		t.Errorf("row = %+v, want a stored blob", r)
	}
}

func TestThumbnailGet_SmallAnimatedGIFGoesThroughTheAsyncJob(t *testing.T) {
	realVips(t)
	h := newHarness(t, harnessOpts{noWorker: true})
	id := newID()
	h.seedSource(id, gifOf(t, 200, 100, 6))
	rec := h.get(thumbReq{id: id, version: testVersion, mime: "image/gif", size: "s"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202 with the interim frame", rec.Code)
	}
	if r := h.pair(id, testVersion)[db.SizeS]; r == nil || r.Status != db.StatusPending || r.Kind != "gif" {
		t.Errorf("row = %+v, want a PENDING gif row", r)
	}
}
