// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"mime"
	"strings"
)

type kind string

const (
	kindImage  kind = "image"
	kindGIF    kind = "gif"
	kindPDF    kind = "pdf"
	kindOffice kind = "office"
	kindVideo  kind = "video"
)

type pipeline struct {
	mime      string
	kind      kind
	original  bool
	native    bool
	thumbnail bool
}

const (
	mimeJPEG = "image/jpeg"
	mimePDF  = "application/pdf"
)

var pipelines = func() map[string]pipeline {
	m := map[string]pipeline{
		mimeJPEG:        {kind: kindImage, original: true, native: true},
		"image/png":     {kind: kindImage, original: true, native: true},
		"image/gif":     {kind: kindImage, original: true, native: true},
		"image/webp":    {kind: kindImage, native: true},
		"image/avif":    {kind: kindImage, native: true},
		"image/bmp":     {kind: kindImage, native: true},
		"image/tiff":    {kind: kindImage},
		"image/heic":    {kind: kindImage},
		"image/heif":    {kind: kindImage},
		"image/svg+xml": {kind: kindImage},
		mimePDF:         {kind: kindPDF, native: true},
	}
	for _, t := range []string{
		"application/msword",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.oasis.opendocument.text",
		"application/vnd.oasis.opendocument.spreadsheet",
		"application/vnd.oasis.opendocument.presentation",
		"text/plain",
		"text/csv",
	} {
		m[t] = pipeline{kind: kindOffice}
	}
	for t, p := range m {
		p.mime = t
		p.thumbnail = t != "image/bmp"
		m[t] = p
	}
	return m
}()

func pipelineFor(raw string) (pipeline, bool) {
	mt, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return pipeline{}, false
	}
	mt = strings.ToLower(mt)
	if mt == "image/jpg" {
		mt = mimeJPEG
	}
	if strings.HasPrefix(mt, "video/") && len(mt) > len("video/") {
		return pipeline{mime: mt, kind: kindVideo, native: true, thumbnail: true}, true
	}
	p, ok := pipelines[mt]
	return p, ok
}
