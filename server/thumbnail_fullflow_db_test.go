//go:build cgo

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/server/testsupport"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

func TestThumbnailFullFlowDB(t *testing.T) {
	testsupport.InitRealRender(t)
	st := startDBStore(t)

	src := jpegOf(t, 3000, 2000)
	storages := testsupport.NewStoragesMock("download", src, "image/jpeg")
	t.Cleanup(storages.Close)

	cfg := testCfg()
	cfg.StorageDownloadAPI = "download"
	cfg.StorageFullAddress = storages.BaseURL()
	c := cache.New(1 << 24)
	s := New(cfg, storage.NewDirectClient(cfg.StorageFullAddress, cfg.StorageDownloadAPI, "upload", "delete", 10*time.Second), c)
	ctx, cancel := context.WithCancel(context.Background())
	s.EnableDB(ctx, st)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		cancel()
		s.worker.Wait()
	})

	id := newID()
	const version = 4
	get := func(size string) (int, string, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/thumbnail/"+id+"/4/"+size+"?service_type=files&mime=image/jpeg", nil)
		req.Header.Set("fileownerid", testOwner)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}

	bodies := map[string][]byte{}
	for size, side := range map[string]int{"s": 320, "m": 1280} {
		code, ct, body := get(size)
		w, _, f := decodeInfo(t, body)
		if code != http.StatusOK || ct != "image/jpeg" || f != "jpeg" || w != side {
			t.Fatalf("%s: %d %q %s %d wide", size, code, ct, f, w)
		}
		bodies[size] = body
	}

	var rows map[string]*db.Thumbnail
	eventually(t, "both background saves done", func() bool {
		var err error
		rows, err = st.FindPair(context.Background(), id, version)
		return err == nil && len(rows) == 2
	})
	uploaded := storages.Uploaded()
	for size, r := range rows {
		if r.Status != db.StatusReady || r.BlobID == nil || r.Original || r.OwnerID != testOwner || r.ServiceType != "files" || r.Version != version {
			t.Fatalf("row %s = %+v", size, r)
		}
		if got := uploaded[testsupport.BlobKey(*r.BlobID, version, "files")]; !bytes.Equal(got, bodies[size]) {
			t.Errorf("blob of %s not uploaded under the source version and service type", size)
		}
	}

	c.Delete(thumbKey("t", "files", testOwner, id, version, "m", ""))
	hits := storages.Hits()
	code, _, body := get("m")
	if code != http.StatusOK || !bytes.Equal(body, bodies["m"]) {
		t.Fatalf("after cache drop: %d identical=%v", code, bytes.Equal(body, bodies["m"]))
	}
	if n := storages.Hits() - hits; n != 1 || len(storages.Uploaded()) != 2 {
		t.Errorf("storages hits +%d uploads %d, want one read of the stored blob and no regeneration", n, len(storages.Uploaded()))
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/thumbnail/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: %d", resp.StatusCode)
	}
	if left, _ := st.FindPair(context.Background(), id, version); len(left) != 0 {
		t.Errorf("rows left: %+v", left)
	}
	deleted := map[string]bool{}
	for _, k := range storages.Deletes() {
		deleted[k] = true
	}
	for size, r := range rows {
		if !deleted[testsupport.BlobKey(*r.BlobID, version, "files")] {
			t.Errorf("blob of %s not deleted from storages", size)
		}
	}
	if n := len(storages.Uploaded()); n != 0 {
		t.Errorf("uploads left: %d", n)
	}
}
