// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cemig

import "testing"

var v3Moves = map[string]string{
	"carbonio-preview/video/max-concurrent-extractions":       "carbonio-preview/async/max-concurrent-jobs",
	"carbonio-preview/video/max-attempts":                     "carbonio-preview/async/max-attempts",
	"carbonio-preview/video/poll-interval-seconds":            "carbonio-preview/async/poll-interval-seconds",
	"carbonio-preview/video/stuck-generation-timeout-seconds": "carbonio-preview/async/stuck-job-timeout-seconds",
}

func TestV3_MovesVideoKeysToAsyncNamespace(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/video/max-concurrent-extractions":       "4",
		"carbonio-preview/video/max-attempts":                     "5",
		"carbonio-preview/video/poll-interval-seconds":            "20",
		"carbonio-preview/video/stuck-generation-timeout-seconds": "600",
	}
	want := map[string]string{
		"carbonio-preview/async/max-concurrent-jobs":       "4",
		"carbonio-preview/async/max-attempts":              "5",
		"carbonio-preview/async/poll-interval-seconds":     "20",
		"carbonio-preview/async/stuck-job-timeout-seconds": "600",
	}
	srv := newStatefulConsulKV(t, data)

	newV1Runner(t, srv.URL).Run()

	for k, v := range want {
		if got := data[k]; got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	for old := range v3Moves {
		if _, ok := data[old]; ok {
			t.Errorf("old key %q must be deleted after a successful move", old)
		}
	}
}

func TestV3_NewKeyAlreadyPresent_NoClobber(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/video/max-attempts": "5",
		"carbonio-preview/async/max-attempts": "9",
	}
	srv := newStatefulConsulKV(t, data)

	newV1Runner(t, srv.URL).Run()

	if got := data["carbonio-preview/async/max-attempts"]; got != "9" {
		t.Errorf("async/max-attempts = %q, must never be clobbered (want 9)", got)
	}
	if got := data["carbonio-preview/video/max-attempts"]; got != "5" {
		t.Errorf("old key = %q, must survive untouched when the move is skipped (want 5)", got)
	}
}

func TestV3_OldKeysAbsent_NoOp(t *testing.T) {
	data := map[string]string{}
	srv := newStatefulConsulKV(t, data)

	newV1Runner(t, srv.URL).Run()

	if len(data) != 0 {
		t.Errorf("expected zero KV entries when old keys are absent, got %v", data)
	}
}

func TestV3_IdempotentSecondRun(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/video/max-concurrent-extractions": "4",
		"carbonio-preview/video/poll-interval-seconds":      "20",
	}
	srv := newStatefulConsulKV(t, data)
	newV1Runner(t, srv.URL).Run()
	newV1Runner(t, srv.URL).Run()

	if got := data["carbonio-preview/async/max-concurrent-jobs"]; got != "4" {
		t.Errorf("async/max-concurrent-jobs = %q, want 4", got)
	}
	if got := data["carbonio-preview/async/poll-interval-seconds"]; got != "20" {
		t.Errorf("async/poll-interval-seconds = %q, want 20", got)
	}
	for old := range v3Moves {
		if _, ok := data[old]; ok {
			t.Errorf("old key %q must remain deleted after a second run", old)
		}
	}
}
