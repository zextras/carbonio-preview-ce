// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

var (
	gif89Blob = []byte("GIF89a\x02\x00\x02\x00frames")
	gif87Blob = []byte("GIF87a\x02\x00\x02\x00frames")
	pngBlob   = []byte("\x89PNG\r\n\x1a\nnot-a-gif")
)

func countImageRenders(t *testing.T) *int {
	t.Helper()
	calls := 0
	prev := imageThumbnailFunc
	imageThumbnailFunc = func(_ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		calls++
		return []byte("rendered"), nil
	}
	t.Cleanup(func() { imageThumbnailFunc = prev })
	return &calls
}

func TestImageGetPreview_GIFPassthrough(t *testing.T) {
	cases := []struct {
		name        string
		blob        []byte
		path        string
		wantBody    string
		wantType    string
		wantRenders int
	}{
		{"full size gif89a is returned as is", gif89Blob, "/1/0x0/?service_type=files&quality=high&output_format=gif", string(gif89Blob), "image/gif", 0},
		{"full size gif87a is returned as is", gif87Blob, "/1/0x0/?service_type=chats&output_format=gif", string(gif87Blob), "image/gif", 0},
		{"full size gif with crop is returned as is", gif89Blob, "/1/0x0/?service_type=files&output_format=gif&crop=true", string(gif89Blob), "image/gif", 0},
		{"sized gif preview is rendered", gif89Blob, "/1/100x100/?service_type=files&output_format=gif", "rendered", "image/gif", 1},
		{"half full size gif preview is rendered", gif89Blob, "/1/0x100/?service_type=files&output_format=gif", "rendered", "image/gif", 1},
		{"full size jpeg of a gif is rendered", gif89Blob, "/1/0x0/?service_type=files&output_format=jpeg", "rendered", "image/jpeg", 1},
		{"full size gif output of png bytes is rendered", pngBlob, "/1/0x0/?service_type=files&output_format=gif", "rendered", "image/gif", 1},
		{"full size gif thumbnail is rendered", gif89Blob, "/1/0x0/thumbnail/?service_type=files&output_format=gif", "rendered", "image/gif", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			renders := countImageRenders(t)
			mux := buildImageHumaMux(testCfg(), &mockStore{blob: tc.blob})

			rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/preview/image/%s%s", validUUID, tc.path))

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != tc.wantType {
				t.Errorf("Content-Type: got %q, want %q", ct, tc.wantType)
			}
			if rec.Body.String() != tc.wantBody {
				t.Errorf("body: got %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if *renders != tc.wantRenders {
				t.Errorf("renders: got %d, want %d", *renders, tc.wantRenders)
			}
		})
	}
}

func TestImagePostPreview_GIFPassthrough(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		wantBody    string
		wantRenders int
	}{
		{"full size gif preview is returned as is", "/preview/image/0x0/?output_format=gif&quality=high", string(gif89Blob), 0},
		{"gif thumbnail is rendered", "/preview/image/80x80/thumbnail/?output_format=gif", "rendered", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			renders := countImageRenders(t)
			mux := buildImageHumaMux(testCfg(), &mockStore{})

			body, ct := buildMultipart(t, "file", "anim.gif", gif89Blob)
			req := httptest.NewRequest(http.MethodPost, tc.path, body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "image/gif" {
				t.Errorf("Content-Type: got %q, want image/gif", got)
			}
			if rec.Body.String() != tc.wantBody {
				t.Errorf("body: got %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if *renders != tc.wantRenders {
				t.Errorf("renders: got %d, want %d", *renders, tc.wantRenders)
			}
		})
	}
}
