// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configdocs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/configdocs"
)

// ── Drift-guard tests ─────────────────────────────────────────────────────────

// buildDocsFromRegistry converts the live registry into a Docs value using the
// same logic as cmd/configdocs/main.go.
func buildDocsFromRegistry() configdocs.Docs {
	keys := config.RegisteredKeys()
	raw := make([]configdocs.RawKey, len(keys))
	for i, k := range keys {
		raw[i] = configdocs.RawKey{
			Key:            k.Key,
			Namespace:      string(k.Namespace),
			Default:        k.Default,
			IfNotPresent:   k.IfNotPresent,
			HiddenFromDocs: k.HiddenFromDocs,
		}
	}
	return configdocs.BuildDocs(config.ServiceName, config.ShortName, raw)
}

// repoRoot walks up from this test file's directory to find the directory
// containing go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod not found)")
		}
		dir = parent
	}
}

// TestDriftGuard_ConfigsMd_Embedded verifies that the embedded config/configs.md
// (via config.ConfigsMd()) is byte-for-byte identical to rendering the live
// registry. If this test fails the generator must be re-run: go run ./cmd/configdocs
func TestDriftGuard_ConfigsMd_Embedded(t *testing.T) {
	want := config.ConfigsMd()
	got := configdocs.RenderMd(buildDocsFromRegistry())
	if got != want {
		t.Errorf("config/configs.md (embedded) has drifted from the registry.\n"+
			"Run: go run ./cmd/configdocs\n\n"+
			"--- want (embedded) ---\n%s\n--- got (rendered) ---\n%s",
			want, got)
	}
}

// TestDriftGuard_ConfigsMd verifies that rendering the live registry produces
// output that is byte-for-byte identical to the committed docs/configs.md.
func TestDriftGuard_ConfigsMd(t *testing.T) {
	root := repoRoot(t)
	committed, err := os.ReadFile(filepath.Join(root, "docs", "configs.md"))
	if err != nil {
		t.Fatalf("could not read docs/configs.md: %v", err)
	}
	want := string(committed)
	got := configdocs.RenderMd(buildDocsFromRegistry())
	if got != want {
		t.Errorf("docs/configs.md has drifted from the registry.\n"+
			"Run: go run ./cmd/configdocs\n\n"+
			"--- want (committed) ---\n%s\n--- got (rendered) ---\n%s",
			want, got)
	}
}

// TestDriftGuard_Mutation verifies that mutating the registry (by using a
// modified key list) produces DIFFERENT output, proving the drift-guard is
// not vacuously passing.
func TestDriftGuard_Mutation(t *testing.T) {
	// Build docs with an extra key injected — output must differ from committed.
	keys := config.RegisteredKeys()
	raw := make([]configdocs.RawKey, len(keys)+1)
	for i, k := range keys {
		raw[i] = configdocs.RawKey{
			Key:          k.Key,
			Namespace:    string(k.Namespace),
			Default:      k.Default,
			IfNotPresent: k.IfNotPresent,
		}
	}
	raw[len(keys)] = configdocs.RawKey{
		Key:       "zzz-extra-sentinel-key",
		Namespace: "application",
		Default:   "sentinel",
	}
	docs := configdocs.BuildDocs(config.ServiceName, config.ShortName, raw)

	got := configdocs.RenderMd(docs)
	committed := config.ConfigsMd()
	if got == committed {
		t.Error("mutated registry produced identical output — drift-guard cannot detect drift")
	}
}

// ── Unit tests ────────────────────────────────────────────────────────────────

// TestAlphabeticalOrder verifies that entries are sorted alphabetically by
// DisplayKey regardless of registration order.
func TestAlphabeticalOrder(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "zebra", Namespace: "application", Default: "z"},
		{Key: "alpha", Namespace: "application", Default: "a"},
		{Key: "middle", Namespace: "application", Default: "m"},
	})
	md := configdocs.RenderMd(docs)

	idxAlpha := strings.Index(md, "carbonio-svc/alpha")
	idxMiddle := strings.Index(md, "carbonio-svc/middle")
	idxZebra := strings.Index(md, "carbonio-svc/zebra")

	if idxAlpha < 0 || idxMiddle < 0 || idxZebra < 0 {
		t.Fatalf("expected all keys in output; got:\n%s", md)
	}
	if !(idxAlpha < idxMiddle && idxMiddle < idxZebra) {
		t.Errorf("keys not in alphabetical order: alpha=%d middle=%d zebra=%d\n%s",
			idxAlpha, idxMiddle, idxZebra, md)
	}
}

// TestNotSetRendering verifies that an empty Default is rendered as
// "*(not set)*" in the md output.
func TestNotSetRendering(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "empty-key", Namespace: "application", Default: "", IfNotPresent: "some note"},
		{Key: "set-key", Namespace: "application", Default: "val"},
	})

	md := configdocs.RenderMd(docs)
	if !strings.Contains(md, "*(not set)*") {
		t.Errorf("md: expected '*(not set)*' for empty default; got:\n%s", md)
	}
	// Non-empty default should be backtick-quoted.
	if !strings.Contains(md, "`val`") {
		t.Errorf("md: expected backtick-quoted value; got:\n%s", md)
	}
}

// TestConditionalThirdColumn_Present verifies the "If not set" column is
// present when at least one entry has empty default + non-empty IfNotPresent.
func TestConditionalThirdColumn_Present(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "a", Namespace: "application", Default: ""},
		{Key: "b", Namespace: "application", Default: "", IfNotPresent: "some note"},
	})
	md := configdocs.RenderMd(docs)
	if !strings.Contains(md, "If not set") {
		t.Errorf("md: expected 'If not set' column; got:\n%s", md)
	}
}

// TestConditionalThirdColumn_Absent verifies the "If not set" column is
// OMITTED when no entry has empty default AND non-empty IfNotPresent.
func TestConditionalThirdColumn_Absent(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "a", Namespace: "application", Default: "val1"},
		{Key: "b", Namespace: "application", Default: "val2"},
	})
	md := configdocs.RenderMd(docs)
	if strings.Contains(md, "If not set") {
		t.Errorf("md: expected NO 'If not set' column when no entry qualifies; got:\n%s", md)
	}
}

// TestConditionalThirdColumn_EmptyDefaultNoNote verifies that an entry with
// empty default but EMPTY IfNotPresent does NOT trigger the third column.
func TestConditionalThirdColumn_EmptyDefaultNoNote(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "no-note", Namespace: "application", Default: "", IfNotPresent: ""},
		{Key: "has-val", Namespace: "application", Default: "present"},
	})
	md := configdocs.RenderMd(docs)
	if strings.Contains(md, "If not set") {
		t.Errorf("empty IfNotPresent should NOT trigger third column; got:\n%s", md)
	}
}

// TestConditionalThirdColumn_PerSection verifies asymmetric behaviour: the
// networking section has no (not set) entries so uses 2 columns, while the
// application section uses 3 columns.  This is the actual behaviour with the
// current preview registry keys.
func TestConditionalThirdColumn_PerSection(t *testing.T) {
	// Networking: all have defaults → 2 columns.
	// Application: one has empty default + IfNotPresent → 3 columns.
	docs := configdocs.BuildDocs("carbonio-preview", "preview", []configdocs.RawKey{
		{Key: "carbonio.service.host", Namespace: "networking", Default: "127.0.0.1"},
		{Key: "workers", Namespace: "application", Default: "2"},
		{Key: "pdf-workers", Namespace: "application", Default: "", IfNotPresent: "Defaults to CPUs"},
	})
	md := configdocs.RenderMd(docs)

	// Split into sections by looking at the section headers.
	netStart := strings.Index(md, "## Networking Config")
	appStart := strings.Index(md, "## Application Config")
	if netStart < 0 || appStart < 0 {
		t.Fatalf("section headers not found:\n%s", md)
	}
	netSection := md[netStart:appStart]
	appSection := md[appStart:]

	if strings.Contains(netSection, "If not set") {
		t.Errorf("networking section should NOT have 'If not set' column; got:\n%s", netSection)
	}
	if !strings.Contains(appSection, "If not set") {
		t.Errorf("application section SHOULD have 'If not set' column; got:\n%s", appSection)
	}
}

// TestKvPathDisplay verifies application keys are shown as Consul KV paths.
func TestKvPathDisplay(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-preview", "preview", []configdocs.RawKey{
		{Key: "storages.download-api", Namespace: "application", Default: "download"},
		{Key: "timeout-in-seconds", Namespace: "application", Default: "30"},
	})
	md := configdocs.RenderMd(docs)
	if !strings.Contains(md, "carbonio-preview/storages/download-api") {
		t.Errorf("expected KV path 'carbonio-preview/storages/download-api'; got:\n%s", md)
	}
	if !strings.Contains(md, "carbonio-preview/timeout-in-seconds") {
		t.Errorf("expected KV path 'carbonio-preview/timeout-in-seconds'; got:\n%s", md)
	}
	// Networking keys should appear as raw dot-notation.
	docs2 := configdocs.BuildDocs("carbonio-preview", "preview", []configdocs.RawKey{
		{Key: "carbonio.service.host", Namespace: "networking", Default: "127.0.0.1"},
	})
	md2 := configdocs.RenderMd(docs2)
	if !strings.Contains(md2, "carbonio.service.host") {
		t.Errorf("networking key should appear as raw dot-notation; got:\n%s", md2)
	}
}

// TestHiddenFromDocs_Filtered verifies that keys with HiddenFromDocs=true are
// excluded from the md rendered output.
func TestHiddenFromDocs_Filtered(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "visible-key", Namespace: "application", Default: "v"},
		{Key: "hidden-key", Namespace: "application", Default: "h", HiddenFromDocs: true},
	})

	md := configdocs.RenderMd(docs)
	if strings.Contains(md, "hidden-key") {
		t.Errorf("md: HiddenFromDocs key must not appear in output; got:\n%s", md)
	}
	if !strings.Contains(md, "visible-key") {
		t.Errorf("md: visible key must appear in output; got:\n%s", md)
	}
}

// TestTimeoutKeysPresentInGeneratedDocs verifies that the live registry's
// timeout keys ("timeout-in-seconds", "docs-timeout-in-seconds") ARE present
// in the rendered md output. They are documented (not hidden) because
// the V1 upgrade migration carries an operator's customized timeout into these
// Consul KV keys, so operators must be able to discover them.
func TestTimeoutKeysPresentInGeneratedDocs(t *testing.T) {
	docs := buildDocsFromRegistry()
	md := configdocs.RenderMd(docs)

	for _, key := range []string{"timeout-in-seconds", "docs-timeout-in-seconds"} {
		if !strings.Contains(md, key) {
			t.Errorf("md: documented key %q must appear in output but is missing:\n%s", key, md)
		}
	}
}

// TestRenderMd_TrailingNewline verifies the Markdown output ends with a blank
// line after the last table.
func TestRenderMd_TrailingNewline(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "k", Namespace: "application", Default: "v"},
	})
	md := configdocs.RenderMd(docs)
	if !strings.HasSuffix(md, "\n\n") {
		t.Errorf("expected output to end with '\\n\\n'; got %q (last 10 chars: %q)",
			md, md[max(0, len(md)-10):])
	}
}
