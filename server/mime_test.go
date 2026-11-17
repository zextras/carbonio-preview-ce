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
		in     string
		canon  string
		kind   kind
		native bool
	}{
		{"image/jpeg", "image/jpeg", kindImage, true},
		{"image/png", "image/png", kindImage, true},
		{"image/gif", "image/gif", kindImage, true},
		{"image/webp", "image/webp", kindImage, true},
		{"image/avif", "image/avif", kindImage, true},
		{"image/tiff", "image/tiff", kindImage, false},
		{"image/heic", "image/heic", kindImage, false},
		{"image/heif", "image/heif", kindImage, false},
		{"image/svg+xml", "image/svg+xml", kindImage, false},
		{"application/pdf", "application/pdf", kindPDF, true},
		{"application/msword", "application/msword", kindOffice, false},
		{"application/vnd.ms-excel", "application/vnd.ms-excel", kindOffice, false},
		{"application/vnd.ms-powerpoint", "application/vnd.ms-powerpoint", kindOffice, false},
		{mimeDocx, mimeDocx, kindOffice, false},
		{mimeXlsx, mimeXlsx, kindOffice, false},
		{mimePptx, mimePptx, kindOffice, false},
		{"application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.text", kindOffice, false},
		{"application/vnd.oasis.opendocument.spreadsheet", "application/vnd.oasis.opendocument.spreadsheet", kindOffice, false},
		{"application/vnd.oasis.opendocument.presentation", "application/vnd.oasis.opendocument.presentation", kindOffice, false},
		{"text/plain", "text/plain", kindOffice, false},
		{"text/csv", "text/csv", kindOffice, false},
		{"video/mp4", "video/mp4", kindVideo, true},
		{"image/bmp", "image/bmp", kindImage, true},
	}
	for _, c := range cases {
		p, ok := pipelineFor(c.in)
		if !ok {
			t.Errorf("%s: not found", c.in)
			continue
		}
		if p.mime != c.canon || p.kind != c.kind || p.native != c.native || p.thumbnail != (c.in != "image/bmp") {
			t.Errorf("%s: got %+v, want mime=%s kind=%s native=%v", c.in, p, c.canon, c.kind, c.native)
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

func TestPipelineFor_MalformedParametersKeepTheType(t *testing.T) {
	for in, want := range map[string]string{
		"image/jpeg; name=a b.jpg": "image/jpeg",
		"text/plain; charset=":     "text/plain",
		"image/jpeg;;":             "image/jpeg",
	} {
		if p, ok := pipelineFor(in); !ok || p.mime != want {
			t.Errorf("%q: got %+v ok=%v, want %s", in, p, ok, want)
		}
	}
	for _, in := range []string{"", "jpeg", "image/", "/jpeg", "image/jp eg", "; charset=x"} {
		if p, ok := pipelineFor(in); ok {
			t.Errorf("%q: accepted as %+v", in, p)
		}
	}
}

func TestPipelineFor_JPGAlias(t *testing.T) {
	p, ok := pipelineFor("image/jpg")
	if !ok || p.mime != "image/jpeg" || p.kind != kindImage {
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
