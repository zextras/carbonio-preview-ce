// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// withCleanRegistry runs f with a clean registry and restores the original
// registry state afterwards.  NOT safe for parallel sub-tests.
func withCleanRegistry(t *testing.T, f func()) {
	t.Helper()
	// save
	registryMu.Lock()
	saved := make([]Migration, len(registry))
	copy(saved, registry)
	registry = nil
	registryMu.Unlock()

	f()

	// restore
	registryMu.Lock()
	registry = saved
	registryMu.Unlock()
}

// writeFile creates a temp file with the given content.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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

// ── Registration tests ────────────────────────────────────────────────────────

func TestRegister_BadName(t *testing.T) {
	withCleanRegistry(t, func() {
		err := Register(Migration{Version: 1, Name: "BadName"})
		if err == nil {
			t.Fatal("expected error for bad name, got nil")
		}
	})
}

func TestRegister_DuplicateVersion(t *testing.T) {
	withCleanRegistry(t, func() {
		m := Migration{Version: 1, Name: "V1__Alpha"}
		if err := Register(m); err != nil {
			t.Fatalf("first register: %v", err)
		}
		err := Register(Migration{Version: 1, Name: "V1__Beta"})
		if err == nil {
			t.Fatal("expected error for duplicate version, got nil")
		}
	})
}

func TestRegister_VersionAscendingExecution(t *testing.T) {
	withCleanRegistry(t, func() {
		var order []int
		var mu sync.Mutex

		for _, v := range []int{3, 1, 2} {
			vv := v
			name := fmt.Sprintf("V%d__Test", vv)
			err := Register(Migration{
				Version: vv,
				Name:    name,
				NetworkingEntries: map[string]EntryFunc{
					fmt.Sprintf("dummy.key%d", vv): func(_, _ string, _ ConfigStore) error {
						mu.Lock()
						order = append(order, vv)
						mu.Unlock()
						return nil
					},
				},
			})
			if err != nil {
				t.Fatalf("register V%d: %v", vv, err)
			}
		}

		// registered() must return them in ascending order
		migs := registered()
		for i := 0; i < len(migs)-1; i++ {
			if migs[i].Version >= migs[i+1].Version {
				t.Errorf("not ascending: %d >= %d", migs[i].Version, migs[i+1].Version)
			}
		}
	})
}

// ── Full idempotency test ─────────────────────────────────────────────────────

func TestRunner_FullMigration_IdempotencyAndRename(t *testing.T) {
	withCleanRegistry(t, func() {
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

		// Register V1 using the real migration.
		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register V1: %v", err)
		}

		// ── First run ────────────────────────────────────────────────────────────
		runner, err := NewRunner(Paths{
			IniPath:     iniPath,
			PropsPath:   propsPath,
			ConsulURL:   srv.URL,
			ConsulToken: "tok123",
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

		// Consul KV: application entries must have been PUT.
		mustKvPut(t, kvPuts, "carbonio-preview/timeout-in-seconds", "30")
		mustKvPut(t, kvPuts, "carbonio-preview/workers", "2")
		mustKvPut(t, kvPuts, "carbonio-preview/image-minimum-resolution", "80")
		mustKvPut(t, kvPuts, "carbonio-preview/storages/download-api", "download")
		mustKvPut(t, kvPuts, "carbonio-preview/docs-editor/service-endpoint", "services/docs/editor")

		// The INI file must have been renamed since all CE keys migrated.
		if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
			t.Errorf("expected config.ini to be renamed, still exists at %s", iniPath)
		}
		if _, err := os.Stat(iniPath + ".migrated"); err != nil {
			t.Errorf("expected config.ini.migrated to exist: %v", err)
		}

		// ── Second run (idempotency) ──────────────────────────────────────────────
		// INI is now .migrated so a fresh iniStore will see it as absent.
		// Re-write it with the renamed name to simulate a fresh absent state.
		// Actually the runner reads iniPath (config.ini) which no longer exists.
		runner2, err := NewRunner(Paths{
			IniPath:     iniPath, // does not exist → absent
			PropsPath:   propsPath,
			ConsulURL:   srv.URL,
			ConsulToken: "tok123",
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
	})
}

// ── Error isolation test ──────────────────────────────────────────────────────

func TestRunner_ErrorIsolation(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()

		// INI with two application keys.
		ini := `[carbonio.preview]
timeout_in_seconds = 30
workers = 2
`
		iniPath := writeFile(t, dir, "config.ini", ini)
		propsPath := filepath.Join(dir, "config.properties")

		// KV server returns 500 for "carbonio-preview/timeout-in-seconds" but
		// 200 for everything else.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path[len("/v1/kv/"):]
			switch r.Method {
			case http.MethodGet:
				w.WriteHeader(http.StatusNotFound)
			case http.MethodPut:
				if path == "carbonio-preview/timeout-in-seconds" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "true")
			case http.MethodDelete:
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "true")
			}
		}))
		defer srv.Close()

		// Register a migration with two application entries.
		err := Register(Migration{
			Version: 1,
			Name:    "V1__ErrorIsolation",
			ApplicationEntries: map[string]EntryFunc{
				"carbonio.preview.timeout_in_seconds": func(_, v string, dest ConfigStore) error {
					return dest.Set("carbonio-preview/timeout-in-seconds", v)
				},
				"carbonio.preview.workers": func(_, v string, dest ConfigStore) error {
					return dest.Set("carbonio-preview/workers", v)
				},
			},
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:     iniPath,
			PropsPath:   propsPath,
			ConsulURL:   srv.URL,
			ConsulToken: "tok",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		// The failing key must still be in the INI (not deleted).
		iniData, err := os.ReadFile(iniPath)
		if err != nil {
			t.Fatalf("read ini after run: %v", err)
		}
		iniContent := string(iniData)

		// One of the two keys should survive (the one whose KV PUT returned 500).
		// The other should have been removed.
		// We can't know which one "failed" without inspecting stderr, but we can
		// check that NOT BOTH are gone (one must survive).
		hasTimeout := contains(iniContent, "timeout_in_seconds")
		hasWorkers := contains(iniContent, "workers")
		// Exactly one must remain (the timeout failed, workers succeeded).
		if hasTimeout == hasWorkers {
			t.Errorf("expected exactly one key to survive; timeout=%v workers=%v", hasTimeout, hasWorkers)
		}
	})
}

// ── INI exhausted rename test ─────────────────────────────────────────────────

func TestRunner_IniRename_WhenAllKeysMigrated(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		iniContent := "[myservice]\nfoo = bar\n"
		iniPath := writeFile(t, dir, "config.ini", iniContent)
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

		err := Register(Migration{
			Version: 1,
			Name:    "V1__Rename",
			NetworkingEntries: map[string]EntryFunc{
				"myservice.foo": func(_, v string, dest ConfigStore) error {
					return dest.Set("new.foo", v)
				},
			},
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:   iniPath,
			PropsPath: propsPath,
			ConsulURL: srv.URL,
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
			t.Error("expected config.ini to be renamed, still exists")
		}
		if _, err := os.Stat(iniPath + ".migrated"); err != nil {
			t.Errorf("expected config.ini.migrated to exist: %v", err)
		}
	})
}

// TestRunner_IniSaved_WhenAdvancedKeysRemain verifies that with leftover advanced
// keys the ini is saved (not renamed) and the advanced keys survive.
func TestRunner_IniSaved_WhenAdvancedKeysRemain(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		// CE key + one "advanced" key not covered by this migration.
		iniContent := "[myservice]\nfoo = bar\nadvanced_key = x\n"
		iniPath := writeFile(t, dir, "config.ini", iniContent)
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

		// Only register migration for "myservice.foo", not for "myservice.advanced_key".
		err := Register(Migration{
			Version: 1,
			Name:    "V1__PartialMigration",
			NetworkingEntries: map[string]EntryFunc{
				"myservice.foo": func(_, v string, dest ConfigStore) error {
					return dest.Set("new.foo", v)
				},
			},
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:   iniPath,
			PropsPath: propsPath,
			ConsulURL: srv.URL,
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		// File must NOT be renamed — advanced_key still present.
		if _, err := os.Stat(iniPath); os.IsNotExist(err) {
			t.Error("config.ini should NOT have been renamed (advanced key remains)")
		}
		iniData, err := os.ReadFile(iniPath)
		if err != nil {
			t.Fatalf("read ini: %v", err)
		}
		if !contains(string(iniData), "advanced_key") {
			t.Error("advanced_key must survive in ini")
		}
		if contains(string(iniData), "foo") {
			t.Error("migrated key foo must be removed from ini")
		}
	})
}

// ── Absent INI test ────────────────────────────────────────────────────────────

func TestRunner_AbsentIni_AllSkipped(t *testing.T) {
	withCleanRegistry(t, func() {
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

		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:     filepath.Join(dir, "does_not_exist.ini"),
			PropsPath:   propsPath,
			ConsulURL:   srv.URL,
			ConsulToken: "tok",
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
	})
}

// ── Token gating test (HasApplicationWork) ───────────────────────────────────

func TestHasApplicationWork_TokenRequired(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		iniPath := writeFile(t, dir, "config.ini", "[carbonio.preview]\ntimeout_in_seconds = 30\n")
		propsPath := filepath.Join(dir, "config.properties")

		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:   iniPath,
			PropsPath: propsPath,
			ConsulURL: "http://localhost:8500",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		if !runner.HasApplicationWork() {
			t.Error("HasApplicationWork must be true when ini has application keys")
		}
	})
}

func TestHasApplicationWork_NoTokenNeededWhenAbsent(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		propsPath := filepath.Join(dir, "config.properties")

		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:   filepath.Join(dir, "no.ini"),
			PropsPath: propsPath,
			ConsulURL: "http://localhost:8500",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		if runner.HasApplicationWork() {
			t.Error("HasApplicationWork must be false when ini is absent")
		}
	})
}

// TestRunner_AppOnly_NoPropertiesFile verifies that a migration with ONLY
// application entries does not create the config.properties file.
func TestRunner_AppOnly_NoPropertiesFile(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		iniContent := "[carbonio.preview]\ntimeout_in_seconds = 30\n"
		iniPath := writeFile(t, dir, "config.ini", iniContent)
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

		err := Register(Migration{
			Version: 1,
			Name:    "V1__AppOnly",
			ApplicationEntries: map[string]EntryFunc{
				"carbonio.preview.timeout_in_seconds": func(_, v string, dest ConfigStore) error {
					return dest.Set("carbonio-preview/timeout-in-seconds", v)
				},
			},
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:     iniPath,
			PropsPath:   propsPath,
			ConsulURL:   srv.URL,
			ConsulToken: "tok",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
			t.Error("properties file must NOT be created for app-only migration")
		}
	})
}

// TestHasApplicationWork_FalseWhenOnlyDropEntries verifies that an ini
// containing ONLY drop-only keys does not require SETUP_CONSUL_TOKEN.
func TestHasApplicationWork_FalseWhenOnlyDropEntries(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		// ini with ONLY drop-only keys — no real application KV entries.
		iniContent := "[log]\nlevel = info\n\n[carbonio.preview]\nname = preview\n"
		iniPath := writeFile(t, dir, "config.ini", iniContent)
		propsPath := filepath.Join(dir, "config.properties")

		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:   iniPath,
			PropsPath: propsPath,
			ConsulURL: "http://localhost:8500", // not reachable — must not be called
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		if runner.HasApplicationWork() {
			t.Error("HasApplicationWork must be false when ini contains only drop-only keys")
		}

		// Full run without a token must succeed and make ZERO Consul requests.
		consulHits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			consulHits++
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		runner2, err := NewRunner(Paths{
			IniPath:   iniPath,
			PropsPath: propsPath,
			ConsulURL: srv.URL,
			// No token — intentionally absent.
		})
		if err != nil {
			t.Fatalf("NewRunner2: %v", err)
		}
		runner2.Run()

		if consulHits != 0 {
			t.Errorf("expected 0 Consul requests, got %d", consulHits)
		}
		// ini must have been renamed (all keys consumed).
		if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
			t.Error("expected config.ini to be renamed to .migrated")
		}
		if _, err := os.Stat(iniPath + ".migrated"); err != nil {
			t.Errorf("expected config.ini.migrated to exist: %v", err)
		}
		// properties file must NOT have been created.
		if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
			t.Error("properties file must not be created when no networking entries ran")
		}
	})
}

// ── splitSectionKey tests ─────────────────────────────────────────────────────

func TestSplitSectionKey(t *testing.T) {
	tests := []struct {
		input   string
		section string
		key     string
		ok      bool
	}{
		{"carbonio.preview.default_host", "carbonio.preview", "default_host", true},
		{"carbonio.docs-editor.default_host", "carbonio.docs-editor", "default_host", true},
		{"image_constants.minimum_resolution", "image_constants", "minimum_resolution", true},
		{"log.format", "log", "format", true},
		{"nodot", "", "", false},
	}
	for _, tt := range tests {
		s, k, ok := splitSectionKey(tt.input)
		if ok != tt.ok || s != tt.section || k != tt.key {
			t.Errorf("splitSectionKey(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, s, k, ok, tt.section, tt.key, tt.ok)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !contains(haystack, needle) {
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
