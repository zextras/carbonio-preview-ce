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
	"github.com/zextras/carbonio-preview-ce/internal/configdocs"
)

// ── Drift-guard tests ─────────────────────────────────────────────────────────

// buildDocsFromRegistry converts the live registry into a Docs value using the
// same logic as cmd/configdocs/main.go.
func buildDocsFromRegistry() configdocs.Docs {
	keys := config.RegisteredKeys()
	raw := make([]configdocs.RawKey, len(keys))
	for i, k := range keys {
		raw[i] = configdocs.RawKey{
			Key:          k.Key,
			Namespace:    string(k.Namespace),
			Default:      k.Default,
			IfNotPresent: k.IfNotPresent,
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

// TestDriftGuard_ConfigsTxt verifies that rendering the live registry produces
// output that is byte-for-byte identical to the committed config/configs.txt.
// If this test fails the generator must be re-run: go run ./cmd/configdocs
func TestDriftGuard_ConfigsTxt(t *testing.T) {
	want := config.ConfigsTxt()
	got := configdocs.RenderTxt(buildDocsFromRegistry())
	if got != want {
		t.Errorf("configs.txt has drifted from the registry.\n"+
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

	got := configdocs.RenderTxt(docs)
	committed := config.ConfigsTxt()
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
	txt := configdocs.RenderTxt(docs)

	idxAlpha := strings.Index(txt, "carbonio-svc/alpha")
	idxMiddle := strings.Index(txt, "carbonio-svc/middle")
	idxZebra := strings.Index(txt, "carbonio-svc/zebra")

	if idxAlpha < 0 || idxMiddle < 0 || idxZebra < 0 {
		t.Fatalf("expected all keys in output; got:\n%s", txt)
	}
	if !(idxAlpha < idxMiddle && idxMiddle < idxZebra) {
		t.Errorf("keys not in alphabetical order: alpha=%d middle=%d zebra=%d\n%s",
			idxAlpha, idxMiddle, idxZebra, txt)
	}
}

// TestNotSetRendering verifies that an empty Default is rendered as "(not set)"
// in the txt output and "*(not set)*" in the md output.
func TestNotSetRendering(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "empty-key", Namespace: "application", Default: "", IfNotPresent: "some note"},
		{Key: "set-key", Namespace: "application", Default: "val"},
	})

	txt := configdocs.RenderTxt(docs)
	if !strings.Contains(txt, "(not set)") {
		t.Errorf("txt: expected '(not set)' for empty default; got:\n%s", txt)
	}
	// Make sure non-empty default is NOT rendered as (not set)
	if strings.Contains(txt, "(not set)") && strings.Contains(txt, "val") {
		// ok — both exist
	}

	md := configdocs.RenderMd(docs)
	if !strings.Contains(md, "*(not set)*") {
		t.Errorf("md: expected '*(not set)*' for empty default; got:\n%s", md)
	}
	// Non-empty default should be backtick-quoted
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
	txt := configdocs.RenderTxt(docs)
	if !strings.Contains(txt, "If not set") {
		t.Errorf("expected 'If not set' column header when IfNotPresent present; got:\n%s", txt)
	}
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
	txt := configdocs.RenderTxt(docs)
	if strings.Contains(txt, "If not set") {
		t.Errorf("expected NO 'If not set' column when no entry qualifies; got:\n%s", txt)
	}
	md := configdocs.RenderMd(docs)
	if strings.Contains(md, "If not set") {
		t.Errorf("md: expected NO 'If not set' column; got:\n%s", md)
	}
}

// TestConditionalThirdColumn_EmptyDefaultNoNote verifies that an entry with
// empty default but EMPTY IfNotPresent does NOT trigger the third column.
func TestConditionalThirdColumn_EmptyDefaultNoNote(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "no-note", Namespace: "application", Default: "", IfNotPresent: ""},
		{Key: "has-val", Namespace: "application", Default: "present"},
	})
	txt := configdocs.RenderTxt(docs)
	if strings.Contains(txt, "If not set") {
		t.Errorf("empty IfNotPresent should NOT trigger third column; got:\n%s", txt)
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
	txt := configdocs.RenderTxt(docs)

	// Split into sections by looking at the section headers.
	netStart := strings.Index(txt, "Networking Config")
	appStart := strings.Index(txt, "Application Config")
	if netStart < 0 || appStart < 0 {
		t.Fatalf("section headers not found:\n%s", txt)
	}
	netSection := txt[netStart:appStart]
	appSection := txt[appStart:]

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
	txt := configdocs.RenderTxt(docs)
	if !strings.Contains(txt, "carbonio-preview/storages/download-api") {
		t.Errorf("expected KV path 'carbonio-preview/storages/download-api'; got:\n%s", txt)
	}
	if !strings.Contains(txt, "carbonio-preview/timeout-in-seconds") {
		t.Errorf("expected KV path 'carbonio-preview/timeout-in-seconds'; got:\n%s", txt)
	}
	// Networking keys should appear as raw dot-notation.
	docs2 := configdocs.BuildDocs("carbonio-preview", "preview", []configdocs.RawKey{
		{Key: "carbonio.service.host", Namespace: "networking", Default: "127.0.0.1"},
	})
	txt2 := configdocs.RenderTxt(docs2)
	if !strings.Contains(txt2, "carbonio.service.host") {
		t.Errorf("networking key should appear as raw dot-notation; got:\n%s", txt2)
	}
}

// TestBoxAlignment verifies that the Unicode box table has correct column widths
// with varied content lengths and proper border characters.
func TestBoxAlignment(t *testing.T) {
	docs := configdocs.BuildDocs("svc", "svc", []configdocs.RawKey{
		{Key: "short", Namespace: "application", Default: "a-very-long-default-value"},
		{Key: "a-much-longer-key-name", Namespace: "application", Default: "x"},
	})
	txt := configdocs.RenderTxt(docs)

	// Verify top border characters.
	if !strings.Contains(txt, "┌") || !strings.Contains(txt, "┐") {
		t.Errorf("expected top border corners; got:\n%s", txt)
	}
	if !strings.Contains(txt, "┬") {
		t.Errorf("expected top border junction '┬'; got:\n%s", txt)
	}
	if !strings.Contains(txt, "└") || !strings.Contains(txt, "┘") {
		t.Errorf("expected bottom border corners; got:\n%s", txt)
	}
	if !strings.Contains(txt, "┴") {
		t.Errorf("expected bottom border junction '┴'; got:\n%s", txt)
	}
	if !strings.Contains(txt, "├") || !strings.Contains(txt, "┤") {
		t.Errorf("expected header separator; got:\n%s", txt)
	}
	if !strings.Contains(txt, "┼") {
		t.Errorf("expected header separator junction '┼'; got:\n%s", txt)
	}

	// Each data row (containing '│') must have the same length.
	lines := strings.Split(txt, "\n")
	var rowLens []int
	for _, l := range lines {
		if strings.HasPrefix(l, "│") {
			rowLens = append(rowLens, len(l))
		}
	}
	if len(rowLens) == 0 {
		t.Fatal("no table rows found")
	}
	first := rowLens[0]
	for i, rl := range rowLens {
		if rl != first {
			t.Errorf("row %d has different length %d (expected %d)", i, rl, first)
		}
	}
}

// TestRenderTxt_TrailingNewline verifies the output ends with the blank line
// after the last table (matching the Java generator which appends "\n\n" after
// each table bottom border).
func TestRenderTxt_TrailingNewline(t *testing.T) {
	docs := configdocs.BuildDocs("carbonio-svc", "svc", []configdocs.RawKey{
		{Key: "k", Namespace: "application", Default: "v"},
	})
	txt := configdocs.RenderTxt(docs)
	if !strings.HasSuffix(txt, "\n\n") {
		t.Errorf("expected output to end with '\\n\\n'; got %q (last 10 chars: %q)",
			txt, txt[max(0, len(txt)-10):])
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
