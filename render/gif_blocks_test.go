// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"slices"
	"testing"
)

func gifHead(globalTable bool) []byte {
	h := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00")
	if globalTable {
		h[10] = 0x80
		return append(h, 0, 0, 0, 255, 255, 255)
	}
	return h
}

func gceBlock(delay int) []byte {
	return []byte{0x21, 0xF9, 0x04, 0x00, byte(delay), byte(delay >> 8), 0x00, 0x00}
}

func imageBlock(packed byte, tableBytes int) []byte {
	b := []byte{0x2C, 0, 0, 0, 0, 1, 0, 1, 0, packed}
	b = append(b, make([]byte, tableBytes)...)
	return append(b, 0x02, 0x02, 0xAA, 0xBB, 0x00)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

var (
	trailer     = []byte{0x3B}
	fakeGCEData = []byte{0x21, 0xF9, 0x04, 0x00, 0x63, 0x00, 0x00, 0x00}
	commentExt  = cat([]byte{0x21, 0xFE, byte(len(fakeGCEData))}, fakeGCEData, []byte{0x00})
	plainText   = cat([]byte{0x21, 0x01, 0x0C}, make([]byte, 12), []byte{0x03, 'a', 'b', 'c', 0x00})
	netscape    = []byte{0x21, 0xFF, 0x0B, 'N', 'E', 'T', 'S', 'C', 'A', 'P', 'E', '2', '.', '0', 0x03, 0x01, 0x00, 0x00, 0x00}
)

func TestGifDelays_Table(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want []int
		ok   bool
	}{
		{"comment extension that looks like a GCE", cat(gifHead(true), netscape, commentExt, gceBlock(7), imageBlock(0, 0), trailer), []int{7}, true},
		{"plain text extension", cat(gifHead(true), plainText, gceBlock(5), imageBlock(0, 0), trailer), []int{5}, true},
		{"local color table", cat(gifHead(false), gceBlock(3), imageBlock(0x80, 6), trailer), []int{3}, true},
		{"interlaced frame", cat(gifHead(true), gceBlock(4), imageBlock(0x40, 0), trailer), []int{4}, true},
		{"frame without a GCE", cat(gifHead(true), gceBlock(2), imageBlock(0, 0), imageBlock(0, 0), gceBlock(6), imageBlock(0, 0), trailer), []int{2, 0, 6}, true},
		{"padding bytes between blocks", cat(gifHead(true), []byte{0, 0}, gceBlock(9), []byte{0}, imageBlock(0, 0), []byte{0, 0}, trailer), []int{9}, true},
		{"zero and one cs delays are raw", cat(gifHead(true), gceBlock(0), imageBlock(0, 0), gceBlock(1), imageBlock(0, 0), gceBlock(2), imageBlock(0, 0), trailer), []int{0, 1, 2}, true},
		{"no frames", cat(gifHead(true), trailer), []int{}, true},
		{"missing trailer", cat(gifHead(true), gceBlock(5), imageBlock(0, 0)), nil, false},
		{"truncated image data", cat(gifHead(true), gceBlock(5), imageBlock(0, 0)[:12]), nil, false},
		{"garbage", []byte("definitely not a gif"), nil, false},
	}
	for _, c := range cases {
		got, ok := gifDelays(c.data)
		if ok != c.ok || (ok && !slices.Equal(got, c.want)) {
			t.Errorf("%s: gifDelays = %v ok=%v, want %v ok=%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeGIFDelays_Table(t *testing.T) {
	cases := []struct {
		name  string
		data  []byte
		want  []int
		total int
	}{
		{"zero and one become ten", cat(gifHead(true), gceBlock(0), imageBlock(0, 0), gceBlock(1), imageBlock(0, 0), gceBlock(2), imageBlock(0, 0), trailer), []int{10, 10, 2}, 22},
		{"a frame without a GCE gets one at ten", cat(gifHead(true), gceBlock(2), imageBlock(0, 0), imageBlock(0, 0), gceBlock(6), imageBlock(0, 0), trailer), []int{2, 10, 6}, 18},
		{"the fake GCE in a comment is not touched", cat(gifHead(true), commentExt, gceBlock(0), imageBlock(0x80, 6), trailer), []int{10}, 10},
	}
	for _, c := range cases {
		out, total, ok := normalizeGIFDelays(c.data)
		if !ok || total != c.total {
			t.Errorf("%s: total=%d ok=%v, want %d", c.name, total, ok, c.total)
			continue
		}
		got, ok := gifDelays(out)
		if !ok || !slices.Equal(got, c.want) {
			t.Errorf("%s: delays %v ok=%v, want %v", c.name, got, ok, c.want)
		}
	}
	if _, _, ok := normalizeGIFDelays([]byte("garbage")); ok {
		t.Error("garbage must not normalize")
	}
}
