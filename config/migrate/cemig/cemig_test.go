// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cemig

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v3/config/migrate"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// writeFile creates a temp file with the given content.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected to find %q in:\n%s", needle, haystack)
	}
}

func mustKvPut(t *testing.T, kvPuts map[string]string, key, expectedValue string) {
	t.Helper()
	v, ok := kvPuts[key]
	if !ok {
		t.Errorf("expected KV PUT for key %q, not found in puts: %v", key, kvPuts)
		return
	}
	if v != expectedValue {
		t.Errorf("KV key %q: expected value %q, got %q", key, expectedValue, v)
	}
}

// sampleIni returns a minimal Python-style config.ini content.
const sampleIni = `[carbonio.preview]
name = preview
default_host = 1.2.3.4
default_port = 10000
timeout_in_seconds = 30
docs-timeout = 15
workers = 2
image_name = image
health_name = health
pdf_name = pdf
document_name = document
enable_document_preview = true
enable_document_thumbnail = false

[log]
format = %%(asctime)s
level = info
path = /var/log/carbonio/preview/

[image_constants]
minimum_resolution = 80

[carbonio.storages]
name = slimstore
download_api = download
health_check = health/live
default_protocol = http
default_host = 1.2.3.4
default_port = 20000

[carbonio.docs-editor]
default_protocol = http
default_host = 1.2.3.4
default_port = 20001
service_endpoint = services/docs/editor
convert_api = cool/convert-to
`

// ── init() self-registration ──────────────────────────────────────────────────

// TestInit_RegistersIntoCESet verifies the whole point of this package: simply
// importing it (which init() does automatically) registers V1 into the "ce"
// migration set, and ONLY the "ce" set — nothing lands anywhere else.
func TestInit_RegistersIntoCESet(t *testing.T) {
	dir := t.TempDir()
	iniPath := writeFile(t, dir, "config.ini",
		"[carbonio.preview]\nenable_document_preview = true\n")
	propsPath := filepath.Join(dir, "config.properties")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut, http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		}
	}))
	defer srv.Close()

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		ConsulToken:  "tok",
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if !runner.HasApplicationWork() {
		t.Error("HasApplicationWork must be true: V1 must be registered in the \"ce\" set by this package's init()")
	}

	// Running an unrelated set name must NOT see V1 (no accidental global leak).
	runnerOther, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "some-other-set-that-does-not-exist",
	})
	if err != nil {
		t.Fatalf("NewRunner (other set): %v", err)
	}
	if runnerOther.HasApplicationWork() {
		t.Error("an unrelated migration set must not see CE's V1 migration")
	}
}

// ── V1MigrateFromPythonIni content tests ──────────────────────────────────────

// TestRunner_FullMigration_IdempotencyAndRename runs the real CE V1 migration
// end-to-end against a full legacy config.ini and verifies both networking and
// application entries land correctly, then verifies a second run is a no-op.
func TestRunner_FullMigration_IdempotencyAndRename(t *testing.T) {
	dir := t.TempDir()
	iniPath := writeFile(t, dir, "config.ini", sampleIni)
	propsPath := filepath.Join(dir, "config.properties")

	// Track KV PUT and DELETE calls.
	kvPuts := map[string]string{}
	kvDeletes := map[string]bool{}
	var kvMu sync.Mutex

	// Stub Consul server: GET ?raw returns the last PUT value; DELETE tracks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// strip /v1/kv/ prefix
		path := r.URL.Path[len("/v1/kv/"):]
		if r.Header.Get("X-Consul-Token") != "tok123" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		kvMu.Lock()
		defer kvMu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			kvPuts[path] = string(body)
			kvDeletes[path] = false
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		case http.MethodGet:
			if kvDeletes[path] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if v, ok := kvPuts[path]; ok {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, v)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodDelete:
			kvDeletes[path] = true
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		}
	}))
	defer srv.Close()

	dropInPath := filepath.Join(dir, "log-level.conf")

	// ── First run ────────────────────────────────────────────────────────────
	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		ConsulToken:  "tok123",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	// config.properties must exist and contain correct networking keys.
	propsData, err := os.ReadFile(propsPath)
	if err != nil {
		t.Fatalf("read props: %v", err)
	}
	propsContent := string(propsData)
	mustContain(t, propsContent, "carbonio.service.host=1.2.3.4")
	mustContain(t, propsContent, "carbonio.service.port=10000")
	mustContain(t, propsContent, "carbonio.storages.host=1.2.3.4")
	mustContain(t, propsContent, "carbonio.docs-editor.host=1.2.3.4")
	mustContain(t, propsContent, "# Migrated by carbonio-preview setup")

	// Consul KV: the four application entries must have been PUT.
	mustKvPut(t, kvPuts, "carbonio-preview/document/enable-preview", "true")
	mustKvPut(t, kvPuts, "carbonio-preview/document/enable-thumbnail", "false")
	mustKvPut(t, kvPuts, "carbonio-preview/storage/fetch-timeout-seconds", "30")
	mustKvPut(t, kvPuts, "carbonio-preview/document/conversion-timeout-seconds", "15")

	// The INI file must have been renamed since all CE keys migrated.
	if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
		t.Errorf("expected config.ini to be renamed, still exists at %s", iniPath)
	}
	if _, err := os.Stat(iniPath + ".migrated"); err != nil {
		t.Errorf("expected config.ini.migrated to exist: %v", err)
	}

	// ── Second run (idempotency) ──────────────────────────────────────────────
	// INI is now .migrated so a fresh iniStore will see it as absent.
	runner2, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath, // does not exist → absent
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		ConsulToken:  "tok123",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner second: %v", err)
	}
	// Clear KV puts tracker to detect if any new puts happen.
	kvMu.Lock()
	kvPuts = map[string]string{}
	kvMu.Unlock()

	runner2.Run()

	kvMu.Lock()
	nPuts := len(kvPuts)
	kvMu.Unlock()
	if nPuts != 0 {
		t.Errorf("second run: expected 0 KV PUTs, got %d", nPuts)
	}
}

// TestRunner_AbsentIni_AllSkipped verifies that an absent ini results in zero
// Consul writes and no properties file, using the real CE V1 migration.
func TestRunner_AbsentIni_AllSkipped(t *testing.T) {
	dir := t.TempDir()
	propsPath := filepath.Join(dir, "config.properties")

	kvPuts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			kvPuts++
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "true")
	}))
	defer srv.Close()

	dropInPath := filepath.Join(dir, "log-level.conf")
	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      filepath.Join(dir, "does_not_exist.ini"),
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		ConsulToken:  "tok",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	if kvPuts != 0 {
		t.Errorf("expected 0 KV PUTs for absent ini, got %d", kvPuts)
	}
	if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
		t.Error("properties file must not be created when ini absent")
	}
}

// ── Token gating tests (HasApplicationWork) ──────────────────────────────────

func TestHasApplicationWork_TokenRequired(t *testing.T) {
	dir := t.TempDir()
	iniPath := writeFile(t, dir, "config.ini", "[carbonio.preview]\nenable_document_preview = true\n")
	propsPath := filepath.Join(dir, "config.properties")

	dropInPath := filepath.Join(dir, "log-level.conf")
	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    "http://localhost:8500",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if !runner.HasApplicationWork() {
		t.Error("HasApplicationWork must be true when ini has application keys")
	}
}

// TestHasApplicationWork_TrueWhenAbsentDueToV2Moves used to assert the
// opposite (false) before V2MoveDBPoolKeys existed: with only V1 registered,
// an absent ini meant no application-layer work at all. Since V2's
// ApplicationKVMoves talk to Consul KV directly and their mere registration
// in a set always counts as application work regardless of the ini (see the
// Migration.ApplicationKVMoves doc comment in config/migrate/migrate.go),
// HasApplicationWork is now unconditionally true for the "ce" set — even with
// an absent ini — so SETUP_CONSUL_TOKEN is always required.
func TestHasApplicationWork_TrueWhenAbsentDueToV2Moves(t *testing.T) {
	dir := t.TempDir()
	propsPath := filepath.Join(dir, "config.properties")

	dropInPath := filepath.Join(dir, "log-level.conf")
	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      filepath.Join(dir, "no.ini"),
		PropsPath:    propsPath,
		ConsulURL:    "http://localhost:8500",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if !runner.HasApplicationWork() {
		t.Error("HasApplicationWork must be true: V2's ApplicationKVMoves always count as application work, even with an absent ini")
	}
}

// TestHasApplicationWork_TrueWhenOnlyDropEntriesDueToV2Moves used to assert
// the opposite (false) before V2MoveDBPoolKeys existed: an ini containing
// ONLY V1 drop-only keys did not require SETUP_CONSUL_TOKEN, because V1's
// application-layer work is driven entirely by the ini. Now that V2's
// ApplicationKVMoves are registered in the same "ce" set, they always talk to
// Consul KV regardless of what the ini contains, so HasApplicationWork is
// true and a full run does touch Consul (one Get(OldPath) probe per V2 move,
// finding nothing to move).
func TestHasApplicationWork_TrueWhenOnlyDropEntriesDueToV2Moves(t *testing.T) {
	dir := t.TempDir()
	// ini with ONLY drop-only keys — no V1 application KV entries.
	iniContent := "[log]\nlevel = info\n\n[carbonio.preview]\nname = preview\n"
	iniPath := writeFile(t, dir, "config.ini", iniContent)
	propsPath := filepath.Join(dir, "config.properties")

	dropInPath := filepath.Join(dir, "log-level.conf")
	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    "http://localhost:8500",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if !runner.HasApplicationWork() {
		t.Error("HasApplicationWork must be true: V2's ApplicationKVMoves always count as application work, regardless of the ini's content")
	}

	// A full run against a reachable Consul stub (no pool.* keys pre-seeded,
	// so every GET is a clean 404) must still complete cleanly: V1's drop-only
	// keys are consumed from the ini, and V2's moves are no-ops (old paths
	// absent).
	consulHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consulHits++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	runner2, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		ConsulToken:  "tok",
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner2: %v", err)
	}
	runner2.Run()

	// V2 issues exactly one Get(OldPath) per declared move (3 moves); none of
	// them exist, so no Get(NewPath), Put, or Delete follows.
	if consulHits != 3 {
		t.Errorf("expected 3 Consul GETs (one OldPath probe per V2 move), got %d", consulHits)
	}
	// ini must have been renamed (all V1 keys consumed).
	if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
		t.Error("expected config.ini to be renamed to .migrated")
	}
	if _, err := os.Stat(iniPath + ".migrated"); err != nil {
		t.Errorf("expected config.ini.migrated to exist: %v", err)
	}
	// properties file must NOT have been created (log.level writes a drop-in,
	// not a properties entry — the dirty flag on propertiesStore stays false).
	if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
		t.Error("properties file must not be created when no networking entries ran")
	}
}

// TestV1_TimeoutKeysMigrateToConsulKV verifies that timeout keys from a Python
// config.ini are carried into Consul KV as application entries (not dropped).
func TestV1_TimeoutKeysMigrateToConsulKV(t *testing.T) {
	dir := t.TempDir()
	iniContent := "[carbonio.preview]\ntimeout_in_seconds = 60\ndocs-timeout = 45\n"
	iniPath := writeFile(t, dir, "config.ini", iniContent)
	propsPath := filepath.Join(dir, "config.properties")

	kvPuts := map[string]string{}
	var kvMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[len("/v1/kv/"):]
		kvMu.Lock()
		defer kvMu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			kvPuts[path] = string(body)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		}
	}))
	defer srv.Close()

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		ConsulToken:  "tok",
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	kvMu.Lock()
	defer kvMu.Unlock()
	mustKvPut(t, kvPuts, "carbonio-preview/storage/fetch-timeout-seconds", "60")
	mustKvPut(t, kvPuts, "carbonio-preview/document/conversion-timeout-seconds", "45")
}

// ── V1 log.level → systemd drop-in migration tests ────────────────────────────

// TestV1LogLevel_WriteDropIn verifies that V1 with [log] level=debug in the ini
// writes a correctly-formatted drop-in file to Paths.DropInPath.
func TestV1LogLevel_WriteDropIn(t *testing.T) {
	dir := t.TempDir()
	dropInPath := filepath.Join(dir, "log-level.conf")

	iniContent := "[log]\nlevel = debug\n"
	iniPath := writeFile(t, dir, "config.ini", iniContent)
	propsPath := filepath.Join(dir, "config.properties")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "true")
	}))
	defer srv.Close()

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	// Drop-in file must exist with the exact content.
	data, err := os.ReadFile(dropInPath)
	if err != nil {
		t.Fatalf("drop-in file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[Service]") {
		t.Errorf("drop-in missing [Service] section: %q", content)
	}
	if !strings.Contains(content, `Environment="PREVIEW_LOG_LEVEL=debug"`) {
		t.Errorf("drop-in missing Environment line: %q", content)
	}

	// ini key must have been removed (file renamed since [log] is now empty).
	if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
		t.Error("expected config.ini to be renamed to .migrated after all keys consumed")
	}

	// config.properties must NOT have been created (log.level does not write to it).
	if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
		t.Error("config.properties must not be created when only log.level was present")
	}
}

// TestV1LogLevel_IdempotentSecondRun verifies that a second run (ini absent)
// does NOT re-write the drop-in file.
func TestV1LogLevel_IdempotentSecondRun(t *testing.T) {
	dir := t.TempDir()
	dropInPath := filepath.Join(dir, "log-level.conf")
	iniPath := filepath.Join(dir, "config.ini") // does not exist → absent

	propsPath := filepath.Join(dir, "config.properties")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "true")
	}))
	defer srv.Close()

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	// Drop-in must NOT have been created (ini absent → entry skipped).
	if _, err := os.Stat(dropInPath); !os.IsNotExist(err) {
		t.Error("drop-in must not be created when ini is absent (idempotent second run)")
	}
}

// TestV1LogLevel_AbsentIniNoDropIn verifies that when the ini has no [log] level
// key, the drop-in file is not created.
func TestV1LogLevel_AbsentIniNoDropIn(t *testing.T) {
	dir := t.TempDir()
	dropInPath := filepath.Join(dir, "log-level.conf")

	// ini with no [log] section at all.
	iniContent := "[carbonio.preview]\nname = preview\n"
	iniPath := writeFile(t, dir, "config.ini", iniContent)
	propsPath := filepath.Join(dir, "config.properties")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "true")
	}))
	defer srv.Close()

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	// Drop-in must NOT have been created.
	if _, err := os.Stat(dropInPath); !os.IsNotExist(err) {
		t.Error("drop-in must not be created when ini has no log.level key")
	}
}

// TestV1LogLevel_DropInHonorsPathsDropInPath verifies that the drop-in is written
// to Paths.DropInPath (injected), NOT to DefaultDropInPath.
func TestV1LogLevel_DropInHonorsPathsDropInPath(t *testing.T) {
	dir := t.TempDir()
	dropInPath := filepath.Join(dir, "subdir", "log-level.conf") // subdir does not pre-exist

	iniContent := "[log]\nlevel = warn\n"
	iniPath := writeFile(t, dir, "config.ini", iniContent)
	propsPath := filepath.Join(dir, "config.properties")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "true")
	}))
	defer srv.Close()

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    propsPath,
		ConsulURL:    srv.URL,
		DropInPath:   dropInPath,
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.Run()

	// Drop-in must be at the injected path (not DefaultDropInPath).
	data, err := os.ReadFile(dropInPath)
	if err != nil {
		t.Fatalf("drop-in not created at injected path %s: %v", dropInPath, err)
	}
	if !strings.Contains(string(data), `Environment="PREVIEW_LOG_LEVEL=warn"`) {
		t.Errorf("unexpected drop-in content: %q", string(data))
	}

	// DefaultDropInPath must NOT have been touched.
	if _, err := os.Stat(migrate.DefaultDropInPath); !os.IsNotExist(err) {
		t.Errorf("DefaultDropInPath %s must not be written by tests", migrate.DefaultDropInPath)
	}
}

// ── RunSetup tests (end-to-end through the "ce" set) ──────────────────────────

// TestRunSetup_TokenRequiredWhenAppWork covers the token-gate arm: the ini holds
// an application-layer key (enable_document_preview), HasApplicationWork() is
// true, and SETUP_CONSUL_TOKEN is empty → RunSetup returns the token error
// WITHOUT touching Consul.
func TestRunSetup_TokenRequiredWhenAppWork(t *testing.T) {
	t.Setenv("SETUP_CONSUL_TOKEN", "")
	dir := t.TempDir()
	iniPath := writeFile(t, dir, "config.ini",
		"[carbonio.preview]\nenable_document_preview = true\n")

	consulHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consulHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	paths := migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    filepath.Join(dir, "config.properties"),
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce",
	}
	err := migrate.RunSetup(srv.URL, paths, "DOCS-TXT")
	if err == nil {
		t.Fatal("want SETUP_CONSUL_TOKEN error, got nil")
	}
	if !strings.Contains(err.Error(), "SETUP_CONSUL_TOKEN") {
		t.Errorf("error = %v, want it to mention SETUP_CONSUL_TOKEN", err)
	}
	if consulHits != 0 {
		t.Errorf("token gate must run before any Consul request; consulHits=%d, want 0", consulHits)
	}
}

// TestRunSetup_SuccessWithToken covers the success arm: an ini with an
// application key plus a token present → Run() executes and PUTs to the Consul
// stub, RunSetup returns nil.
func TestRunSetup_SuccessWithToken(t *testing.T) {
	t.Setenv("SETUP_CONSUL_TOKEN", "tok123")
	dir := t.TempDir()
	iniPath := writeFile(t, dir, "config.ini",
		"[carbonio.preview]\nenable_document_preview = true\n")

	var puts int
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			puts++
			gotToken = r.Header.Get("X-Consul-Token")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	paths := migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    filepath.Join(dir, "config.properties"),
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce",
	}
	if err := migrate.RunSetup(srv.URL, paths, "DOCS-TXT"); err != nil {
		t.Fatalf("RunSetup returned error: %v", err)
	}
	if puts == 0 {
		t.Error("expected at least one Consul PUT during a successful run")
	}
	if gotToken != "tok123" {
		t.Errorf("X-Consul-Token = %q, want %q", gotToken, "tok123")
	}
}

// TestRunSetup_TokenRequiredWhenIniAbsentDueToV2Moves covers the token-gate
// arm introduced by V2MoveDBPoolKeys: even with an absent ini (so V1 has
// nothing to do), V2's ApplicationKVMoves always count as application work —
// they talk to Consul KV directly, independently of the ini — so
// SETUP_CONSUL_TOKEN is still required. Before V2 existed, an absent ini made
// RunSetup succeed with no token (see TestRunSetup_SuccessAbsentIni below for
// the corresponding success arm now that a token is supplied).
func TestRunSetup_TokenRequiredWhenIniAbsentDueToV2Moves(t *testing.T) {
	t.Setenv("SETUP_CONSUL_TOKEN", "")
	dir := t.TempDir()

	consulHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consulHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	paths := migrate.Paths{
		IniPath:      filepath.Join(dir, "does-not-exist.ini"),
		PropsPath:    filepath.Join(dir, "config.properties"),
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce",
	}
	err := migrate.RunSetup(srv.URL, paths, "DOCS-TXT")
	if err == nil {
		t.Fatal("want SETUP_CONSUL_TOKEN error (V2's moves need Consul even with an absent ini), got nil")
	}
	if !strings.Contains(err.Error(), "SETUP_CONSUL_TOKEN") {
		t.Errorf("error = %v, want it to mention SETUP_CONSUL_TOKEN", err)
	}
	if consulHits != 0 {
		t.Errorf("token gate must run before any Consul request; consulHits=%d, want 0", consulHits)
	}
}

// TestRunSetup_SuccessAbsentIni covers the success arm with an absent ini and
// a token present: V1 has nothing to migrate (ini missing), and V2's moves
// find none of the old pool.* keys (stub has nothing pre-seeded), so the run
// completes cleanly.
func TestRunSetup_SuccessAbsentIni(t *testing.T) {
	t.Setenv("SETUP_CONSUL_TOKEN", "tok123")
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // no keys pre-seeded
	}))
	defer srv.Close()

	paths := migrate.Paths{
		IniPath:      filepath.Join(dir, "does-not-exist.ini"),
		PropsPath:    filepath.Join(dir, "config.properties"),
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce",
	}
	if err := migrate.RunSetup(srv.URL, paths, "DOCS-TXT"); err != nil {
		t.Fatalf("RunSetup with absent ini and V2's moves finding nothing should not fail: %v", err)
	}
}

// ── V2MoveDBPoolKeys (ApplicationKVMoves) end-to-end tests ───────────────────

// newStatefulConsulKV returns a Consul KV stub backed by the given in-memory
// data map: GET /v1/kv/<path>?raw serves the map (404 when absent), PUT
// writes into it, DELETE removes from it. Mirrors config/migrate's
// newStatefulConsul so ApplicationKVMoves are exercised against the same
// GET-?raw / PUT / DELETE contract the real consulKvStore uses.
func newStatefulConsulKV(t *testing.T, data map[string]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if v, ok := data[path]; ok {
				fmt.Fprint(w, v)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			data[path] = string(body)
			fmt.Fprint(w, "true")
		case http.MethodDelete:
			delete(data, path)
			fmt.Fprint(w, "true")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newV2Runner builds a runner against the "ce" set with an ABSENT legacy ini
// (V2's ApplicationKVMoves talk to Consul KV directly and must run regardless
// of the ini's presence), pointed at consulURL.
func newV2Runner(t *testing.T, consulURL string) *migrate.Runner {
	t.Helper()
	dir := t.TempDir()
	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:      filepath.Join(dir, "no.ini"),
		PropsPath:    filepath.Join(dir, "config.properties"),
		ConsulURL:    consulURL,
		ConsulToken:  "tok",
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce",
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return runner
}

// TestV2_MovesOldPoolKeysToNewDBPoolKeys runs the real "ce" set end to end
// (absent ini) against a Consul KV stub pre-seeded with the three pre-8d14b8c
// database-pool keys. It verifies the values land at the new db-pool-* paths,
// the old keys are deleted, and the lifetime value is converted seconds->ms.
func TestV2_MovesOldPoolKeysToNewDBPoolKeys(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/database/pool/max-connections":                 "15",
		"carbonio-preview/database/pool/min-connections":                 "3",
		"carbonio-preview/database/pool/connection-max-lifetime-seconds": "900",
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV2Runner(t, srv.URL)

	runner.Run()

	if got := data["carbonio-preview/database/db-pool-max-size"]; got != "15" {
		t.Errorf("db-pool-max-size = %q, want %q", got, "15")
	}
	if got := data["carbonio-preview/database/db-pool-min-size"]; got != "3" {
		t.Errorf("db-pool-min-size = %q, want %q", got, "3")
	}
	if got := data["carbonio-preview/database/db-pool-max-lifetime"]; got != "900000" {
		t.Errorf("db-pool-max-lifetime = %q, want %q (900s -> 900000ms)", got, "900000")
	}
	for _, old := range []string{
		"carbonio-preview/database/pool/max-connections",
		"carbonio-preview/database/pool/min-connections",
		"carbonio-preview/database/pool/connection-max-lifetime-seconds",
	} {
		if _, ok := data[old]; ok {
			t.Errorf("old key %q must be deleted after a successful move", old)
		}
	}
}

// TestV2_NewKeyAlreadyPresent_NoClobber verifies the never-clobber arm: when
// an operator (or an earlier partial run) has already set a new-style
// db-pool-* value, V2 must leave it untouched and must not delete the old key
// either (the move is skipped entirely for that pair).
func TestV2_NewKeyAlreadyPresent_NoClobber(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/database/pool/max-connections": "15",
		"carbonio-preview/database/db-pool-max-size":     "42", // operator-set new-style value
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV2Runner(t, srv.URL)

	runner.Run()

	if got := data["carbonio-preview/database/db-pool-max-size"]; got != "42" {
		t.Errorf("db-pool-max-size = %q, must never be clobbered (want %q)", got, "42")
	}
	if got := data["carbonio-preview/database/pool/max-connections"]; got != "15" {
		t.Errorf("old key = %q, must survive untouched when the move is skipped (want %q)", got, "15")
	}
}

// TestV2_OldKeyAbsent_NoOp verifies that when none of the old pool.* keys
// exist (a fresh install, or one that never had them), V2 makes no writes and
// no deletes at all.
func TestV2_OldKeyAbsent_NoOp(t *testing.T) {
	data := map[string]string{}
	srv := newStatefulConsulKV(t, data)
	runner := newV2Runner(t, srv.URL)

	runner.Run()

	if len(data) != 0 {
		t.Errorf("expected zero KV entries when old keys are absent, got %v", data)
	}
}

// TestV2_NonNumericLifetime_WarnAndSkip verifies the Transform-error arm: a
// non-numeric lifetime value must NOT be written to the new path, must NOT be
// deleted from the old path (retryable), and must not stop the run or the
// setup (warn-and-skip, per Runner.runApplicationKVMoves).
func TestV2_NonNumericLifetime_WarnAndSkip(t *testing.T) {
	data := map[string]string{
		"carbonio-preview/database/pool/connection-max-lifetime-seconds": "not-a-number",
	}
	srv := newStatefulConsulKV(t, data)
	runner := newV2Runner(t, srv.URL)

	runner.Run()

	if got := data["carbonio-preview/database/pool/connection-max-lifetime-seconds"]; got != "not-a-number" {
		t.Errorf("old lifetime key = %q, must be preserved when the transform fails (want %q)", got, "not-a-number")
	}
	if _, ok := data["carbonio-preview/database/db-pool-max-lifetime"]; ok {
		t.Error("new lifetime key must NOT be written when the transform fails")
	}
}
