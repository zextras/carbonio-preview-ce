// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import "testing"

const (
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

func TestPipelineFor_Table(t *testing.T) {
	cases := []struct {
		in       string
		canon    string
		kind     kind
		original bool
		native   bool
	}{
		{"image/jpeg", "image/jpeg", kindImage, true, true},
		{"image/png", "image/png", kindImage, true, true},
		{"image/gif", "image/gif", kindImage, true, true},
		{"image/webp", "image/webp", kindImage, false, true},
		{"image/avif", "image/avif", kindImage, false, true},
		{"image/tiff", "image/tiff", kindImage, false, false},
		{"image/heic", "image/heic", kindImage, false, false},
		{"image/heif", "image/heif", kindImage, false, false},
		{"image/svg+xml", "image/svg+xml", kindImage, false, false},
		{"application/pdf", "application/pdf", kindPDF, false, true},
		{"application/msword", "application/msword", kindOffice, false, false},
		{"application/vnd.ms-excel", "application/vnd.ms-excel", kindOffice, false, false},
		{"application/vnd.ms-powerpoint", "application/vnd.ms-powerpoint", kindOffice, false, false},
		{mimeDocx, mimeDocx, kindOffice, false, false},
		{mimeXlsx, mimeXlsx, kindOffice, false, false},
		{mimePptx, mimePptx, kindOffice, false, false},
		{"application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.text", kindOffice, false, false},
		{"application/vnd.oasis.opendocument.spreadsheet", "application/vnd.oasis.opendocument.spreadsheet", kindOffice, false, false},
		{"application/vnd.oasis.opendocument.presentation", "application/vnd.oasis.opendocument.presentation", kindOffice, false, false},
		{"text/plain", "text/plain", kindOffice, false, false},
		{"text/csv", "text/csv", kindOffice, false, false},
		{"video/mp4", "video/mp4", kindVideo, false, true},
		{"image/bmp", "image/bmp", kindImage, false, true},
	}
	for _, c := range cases {
		p, ok := pipelineFor(c.in)
		if !ok {
			t.Errorf("%s: not found", c.in)
			continue
		}
		if p.mime != c.canon || p.kind != c.kind || p.original != c.original || p.native != c.native || p.thumbnail != (c.in != "image/bmp") {
			t.Errorf("%s: got %+v, want mime=%s kind=%s original=%v native=%v", c.in, p, c.canon, c.kind, c.original, c.native)
		}
	}
}

func TestPipelineFor_NormalisesCaseAndParameters(t *testing.T) {
	for _, in := range []string{"IMAGE/JPEG", "image/jpeg; charset=binary", " image/jpeg ", "Image/Jpeg;q=0.5"} {
		p, ok := pipelineFor(in)
		if !ok || p.mime != "image/jpeg" {
			t.Errorf("%q: got %+v ok=%v", in, p, ok)
		}
	}
	if p, ok := pipelineFor("text/plain; charset=utf-8"); !ok || p.mime != "text/plain" || p.kind != kindOffice {
		t.Errorf("text/plain with charset: %+v ok=%v", p, ok)
	}
}

func TestPipelineFor_JPGAlias(t *testing.T) {
	p, ok := pipelineFor("image/jpg")
	if !ok || p.mime != "image/jpeg" || p.kind != kindImage || !p.original {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
}

func TestPipelineFor_AnyVideo(t *testing.T) {
	for _, in := range []string{"video/mp4", "video/webm", "video/quicktime", "video/x-matroska", "VIDEO/X-NEW"} {
		p, ok := pipelineFor(in)
		if !ok || p.kind != kindVideo {
			t.Errorf("%s: got %+v ok=%v", in, p, ok)
		}
	}
}

func TestPipelineFor_Unsupported(t *testing.T) {
	for _, in := range []string{"application/octet-stream", "", "garbage", "image/", "/", "audio/mpeg", "application/zip", ";;"} {
		if p, ok := pipelineFor(in); ok {
			t.Errorf("%q: got %+v, want not found", in, p)
		}
	}
}

func TestPipelineFor_BMPIsPreviewOnly(t *testing.T) {
	p, ok := pipelineFor("image/bmp")
	if !ok || !p.native || p.thumbnail {
		t.Fatalf("got %+v ok=%v, want native with no thumbnail", p, ok)
	}
}
