// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/db"
	"github.com/zextras/carbonio-preview-ce/v4/render"
)

type collaboraCall struct {
	lang, url string
	slots     int
}

func stubOffice(t *testing.T, sem func() int, fn func(call collaboraCall) ([]byte, error)) *[]collaboraCall {
	t.Helper()
	var mu sync.Mutex
	var calls []collaboraCall
	prevConvert, prevPair := collaboraConvertFunc, imagePairFunc
	collaboraConvertFunc = func(_ context.Context, _ []byte, lang, url string, _ time.Duration) ([]byte, error) {
		c := collaboraCall{lang: lang, url: url}
		if sem != nil {
			c.slots = sem()
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		return fn(c)
	}
	imagePairFunc = func([]byte) (render.Pair, error) {
		return render.Pair{S: stubS, M: stubM}, nil
	}
	t.Cleanup(func() { collaboraConvertFunc, imagePairFunc = prevConvert, prevPair })
	return &calls
}

func officeHarness(t *testing.T) (*harness, string) {
	h := newHarness(t, harnessOpts{})
	id := newID()
	h.store.seed(id, testVersion, "files", testOwner, []byte("docx-bytes"))
	return h, id
}

func TestOfficeJob_ReadyBothSizes(t *testing.T) {
	calls := stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	h, id := officeHarness(t)

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	rows := h.waitStatus(id, testVersion, db.StatusReady)
	for size, r := range rows {
		if r.Kind != "office" || r.BlobID == nil || r.OwnerID != testOwner || r.ServiceType != "files" {
			t.Errorf("size %s = %+v", size, r)
		}
		if !h.store.has(*r.BlobID, testVersion, "files", testOwner) {
			t.Errorf("size %s blob missing under the owner", size)
		}
	}
	rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime, size: "s"})
	if rec.Code != http.StatusOK || rec.Body.String() != string(stubS.Body) || rec.Header().Get("Content-Type") != stubS.ContentType {
		t.Fatalf("GET s: %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0].url, "/png") {
		t.Errorf("collabora calls = %+v, want one convert-to png", *calls)
	}
}

func TestOfficeJob_HoldsAsyncSlotOnlyAroundCollabora(t *testing.T) {
	var h *harness
	var pairSlots []int
	var mu sync.Mutex
	calls := stubOffice(t, func() int { return len(h.deps.AsyncSem) }, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	pairStub := imagePairFunc
	imagePairFunc = func(b []byte) (render.Pair, error) {
		mu.Lock()
		pairSlots = append(pairSlots, len(h.deps.AsyncSem))
		mu.Unlock()
		return pairStub(b)
	}
	var id string
	h, id = officeHarness(t)
	var getSlots []int
	h.store.onGet = func(memKey) {
		mu.Lock()
		getSlots = append(getSlots, len(h.deps.AsyncSem))
		mu.Unlock()
	}

	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitStatus(id, testVersion, db.StatusReady)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 || (*calls)[0].slots != 1 {
		t.Errorf("collabora must run holding exactly one async slot: %+v", *calls)
	}
	for _, n := range append(append([]int{}, pairSlots...), getSlots...) {
		if n != 0 {
			t.Errorf("download and render must not hold the async slot, saw %d", n)
		}
	}
	if len(pairSlots) == 0 || len(getSlots) == 0 {
		t.Errorf("probes did not run: pair=%v get=%v", pairSlots, getSlots)
	}
	if n := len(h.deps.AsyncSem); n != 0 {
		t.Errorf("async slot leaked: %d", n)
	}
}

func TestOfficeJob_CollaboraUnavailableDropsRow(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) {
		return nil, fmt.Errorf("%w: dial tcp: refused", render.ErrCollaboraUnavailable)
	})
	h, id := officeHarness(t)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "rows dropped", func() bool { return len(h.pair(id, testVersion)) == 0 })
	if n := h.store.blobCount(); n != 1 {
		t.Errorf("blobs = %d, only the source must remain", n)
	}
}

func TestOfficeJob_Collabora4xxCountsAttempt(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return nil, errors.New("collabora returned status 400") })
	h, id := officeHarness(t)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "attempt counted and rows released", func() bool {
		rows := h.pair(id, testVersion)
		return len(rows) == 2 && rows["s"].Status == db.StatusPending && rows["s"].Attempts == 1 && rows["m"].Attempts == 1
	})
}

func TestOfficeJob_AtAttemptCapIsFailed(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return nil, errors.New("collabora returned status 400") })
	h, id := officeHarness(t)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	eventually(t, "first attempt", func() bool {
		rows := h.pair(id, testVersion)
		return len(rows) == 2 && rows["m"].Attempts == 1 && rows["m"].Status == db.StatusPending
	})
	h.exec(`UPDATE thumbnail SET attempts = 2 WHERE file_id=$1`, id)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("poll: %d", rec.Code)
	}
	h.waitStatus(id, testVersion, db.StatusFailed)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("FAILED: %d, want 422", rec.Code)
	}
}

func TestOfficeJob_SourceNotFoundIsFailed(t *testing.T) {
	stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	h := newHarness(t, harnessOpts{})
	id := newID()
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitStatus(id, testVersion, db.StatusFailed)
}

func TestOfficeJob_UsesRowLang(t *testing.T) {
	calls := stubOffice(t, nil, func(collaboraCall) ([]byte, error) { return []byte("png"), nil })
	h, id := officeHarness(t)
	if rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime, lang: "it-IT"}); rec.Code != http.StatusAccepted {
		t.Fatalf("miss: %d", rec.Code)
	}
	h.waitStatus(id, testVersion, db.StatusReady)
	if len(*calls) != 1 || (*calls)[0].lang != "it-IT" {
		t.Fatalf("calls = %+v, want lang it-IT from the row", *calls)
	}
	rec := h.get(thumbReq{id: id, version: testVersion, mime: docxMime, lang: "de-DE"})
	if rec.Code != http.StatusOK || len(*calls) != 1 {
		t.Errorf("a later request with another lang reuses the thumbnail: %d, calls=%d", rec.Code, len(*calls))
	}
}
