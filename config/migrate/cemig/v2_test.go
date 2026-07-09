// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cemig

import (
	"testing"
)

// ── V2RenameConfigNamespaces (ApplicationKVMoves) end-to-end tests ───────────
//
// newV1Runner (defined in cemig_test.go) builds a runner against the "ce" set
// with an ABSENT legacy ini; despite its V1-era name it is a generic "ce" set
// runner and is reused here unchanged for V2's tests.

// TestV2_MovesRenamedKeysToNewNamespace runs the real "ce" set end to end
// (absent ini) against a Consul KV stub pre-seeded with the four pre-refactor
// keys. It verifies the values land at the new paths (verbatim, no
// transform) and the old keys are deleted.
func TestV2_MovesRenamedKeysToNewNamespace(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/storage/fetch-timeout-seconds":    "45",
		"carbonio-preview/render/cache-max-mb":              "512",
		"carbonio-preview/render/max-concurrent-operations": "4",
		"carbonio-preview/render/pdf-subprocess-pool-size":  "2",
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV1Runner(t, srv.URL)

	runner.Run()

	if got := data["carbonio-preview/image-document/fetch-timeout-seconds"]; got != "45" {
		t.Errorf("image-document/fetch-timeout-seconds = %q, want %q", got, "45")
	}
	if got := data["carbonio-preview/cache-max-mb"]; got != "512" {
		t.Errorf("cache-max-mb = %q, want %q", got, "512")
	}
	if got := data["carbonio-preview/image-document/max-concurrent-operations"]; got != "4" {
		t.Errorf("image-document/max-concurrent-operations = %q, want %q", got, "4")
	}
	if got := data["carbonio-preview/document/subprocess-pool-size"]; got != "2" {
		t.Errorf("document/subprocess-pool-size = %q, want %q", got, "2")
	}

	for _, old := range []string{
		"carbonio-preview/storage/fetch-timeout-seconds",
		"carbonio-preview/render/cache-max-mb",
		"carbonio-preview/render/max-concurrent-operations",
		"carbonio-preview/render/pdf-subprocess-pool-size",
	} {
		if _, ok := data[old]; ok {
			t.Errorf("old key %q must be deleted after a successful move", old)
		}
	}
}

// TestV2_NewKeyAlreadyPresent_NoClobber verifies the never-clobber arm for the
// root-level "cache-max-mb" move: when an operator (or an earlier partial
// run) has already set the new-style root key, V2RenameConfigNamespaces must
// leave it untouched and must not delete the old key either (the move is
// skipped entirely for that pair).
func TestV2_NewKeyAlreadyPresent_NoClobber(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/render/cache-max-mb": "512",
		"carbonio-preview/cache-max-mb":        "999", // operator-set new-style value
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV1Runner(t, srv.URL)

	runner.Run()

	if got := data["carbonio-preview/cache-max-mb"]; got != "999" {
		t.Errorf("cache-max-mb = %q, must never be clobbered (want %q)", got, "999")
	}
	if got := data["carbonio-preview/render/cache-max-mb"]; got != "512" {
		t.Errorf("old key = %q, must survive untouched when the move is skipped (want %q)", got, "512")
	}
}

// TestV2_OldKeyAbsent_NoOp verifies that when none of the four old keys
// exist (a fresh install, or one that never had them), V2RenameConfigNamespaces
// makes no writes and no deletes at all.
func TestV2_OldKeyAbsent_NoOp(t *testing.T) {
	data := map[string]string{}
	srv := newStatefulConsulKV(t, data)
	runner := newV1Runner(t, srv.URL)

	runner.Run()

	if len(data) != 0 {
		t.Errorf("expected zero KV entries when old keys are absent, got %v", data)
	}
}

// TestV1AndV2_BothApplyInASingleRun verifies that a single Run() against the
// "ce" set executes BOTH V1MoveDBPoolKeys and V2RenameConfigNamespaces when
// their respective old-style keys are all present — proving RegisterInSet
// appends multiple migrations into the same set and Runner.Run() walks the
// whole version-ascending series, not just the first entry.
func TestV1AndV2_BothApplyInASingleRun(t *testing.T) {
	data := map[string]string{
		// V1 (database pool) old keys.
		"carbonio-preview/database/pool/max-connections": "15",
		// V2 (namespace rename) old keys.
		"carbonio-preview/storage/fetch-timeout-seconds": "45",
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV1Runner(t, srv.URL)

	runner.Run()

	if got := data["carbonio-preview/database/db-pool-max-size"]; got != "15" {
		t.Errorf("V1 move: db-pool-max-size = %q, want %q", got, "15")
	}
	if got := data["carbonio-preview/image-document/fetch-timeout-seconds"]; got != "45" {
		t.Errorf("V2 move: image-document/fetch-timeout-seconds = %q, want %q", got, "45")
	}
}

// TestV2_IdempotentSecondRun verifies that once the four keys have been moved,
// a second Run() against the same (now new-style) data makes no further
// changes.
func TestV2_IdempotentSecondRun(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/storage/fetch-timeout-seconds":    "45",
		"carbonio-preview/render/cache-max-mb":              "512",
		"carbonio-preview/render/max-concurrent-operations": "4",
		"carbonio-preview/render/pdf-subprocess-pool-size":  "2",
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV1Runner(t, srv.URL)
	runner.Run()

	// Second run against the now-migrated data must be a clean no-op.
	runner2 := newV1Runner(t, srv.URL)
	runner2.Run()

	if got := data["carbonio-preview/image-document/fetch-timeout-seconds"]; got != "45" {
		t.Errorf("second run: image-document/fetch-timeout-seconds = %q, want %q (unchanged)", got, "45")
	}
	if got := data["carbonio-preview/cache-max-mb"]; got != "512" {
		t.Errorf("second run: cache-max-mb = %q, want %q (unchanged)", got, "512")
	}
	for _, old := range []string{
		"carbonio-preview/storage/fetch-timeout-seconds",
		"carbonio-preview/render/cache-max-mb",
		"carbonio-preview/render/max-concurrent-operations",
		"carbonio-preview/render/pdf-subprocess-pool-size",
	} {
		if _, ok := data[old]; ok {
			t.Errorf("old key %q must remain deleted after a second run", old)
		}
	}
}
