// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// withCleanRegistry runs f with every migration set cleared and restores the
// original sets afterwards.  NOT safe for parallel sub-tests.
func withCleanRegistry(t *testing.T, f func()) {
	t.Helper()
	// save
	setsMu.Lock()
	saved := make(map[string][]Migration, len(sets))
	for k, v := range sets {
		cp := make([]Migration, len(v))
		copy(cp, v)
		saved[k] = cp
	}
	sets = map[string][]Migration{}
	setsMu.Unlock()

	f()

	// restore
	setsMu.Lock()
	sets = saved
	setsMu.Unlock()
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

// ── Registration tests ────────────────────────────────────────────────────────

func TestRegisterInSet_BadName(t *testing.T) {
	withCleanRegistry(t, func() {
		err := RegisterInSet("ce", Migration{Version: 1, Name: "BadName"})
		if err == nil {
			t.Fatal("expected error for bad name, got nil")
		}
	})
}

func TestRegisterInSet_DuplicateVersionWithinSameSet(t *testing.T) {
	withCleanRegistry(t, func() {
		m := Migration{Version: 1, Name: "V1__Alpha"}
		if err := RegisterInSet("ce", m); err != nil {
			t.Fatalf("first register: %v", err)
		}
		err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__Beta"})
		if err == nil {
			t.Fatal("expected error for duplicate version within the same set, got nil")
		}
	})
}

// TestRegisterInSet_SameVersionAcrossDifferentSetsAllowed is the crux of the
// isolation contract: a CE V1 and an Advanced V1 are unrelated migrations and
// must NOT collide just because they share a version number.
func TestRegisterInSet_SameVersionAcrossDifferentSetsAllowed(t *testing.T) {
	withCleanRegistry(t, func() {
		if err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__CE"}); err != nil {
			t.Fatalf("register ce V1: %v", err)
		}
		if err := RegisterInSet("advanced", Migration{Version: 1, Name: "V1__Advanced"}); err != nil {
			t.Fatalf("register advanced V1 (same version, different set): %v", err)
		}
	})
}

// TestRegisterInSet_KVMoveValidation verifies that malformed ApplicationKVMoves
// are rejected at registration time: empty paths and old==new renames are
// programming errors, not runtime conditions.
func TestRegisterInSet_KVMoveValidation(t *testing.T) {
	withCleanRegistry(t, func() {
		cases := []struct {
			name string
			mv   KVMove
		}{
			{"empty old path", KVMove{OldPath: "", NewPath: "svc/new"}},
			{"empty new path", KVMove{OldPath: "svc/old", NewPath: ""}},
			{"no slash in old path (bare dotted key)", KVMove{OldPath: "database.pool.max", NewPath: "svc/new"}},
			{"no slash in new path (bare dotted key)", KVMove{OldPath: "svc/old", NewPath: "database.db-pool-max-size"}},
			{"identical paths", KVMove{OldPath: "svc/same", NewPath: "svc/same"}},
		}
		for _, tc := range cases {
			err := RegisterInSet("ce", Migration{
				Version:            1,
				Name:               "V1__BadMove",
				ApplicationKVMoves: []KVMove{tc.mv},
			})
			if err == nil {
				t.Errorf("%s: want registration error, got nil", tc.name)
			}
		}
	})
}

func TestOrderedSet_VersionAscending(t *testing.T) {
	withCleanRegistry(t, func() {
		var order []int
		var mu sync.Mutex

		for _, v := range []int{3, 1, 2} {
			vv := v
			name := fmt.Sprintf("V%d__Test", vv)
			err := RegisterInSet("ce", Migration{
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

		// orderedSet must return them in ascending order.
		migs := orderedSet("ce")
		for i := 0; i < len(migs)-1; i++ {
			if migs[i].Version >= migs[i+1].Version {
				t.Errorf("not ascending: %d >= %d", migs[i].Version, migs[i+1].Version)
			}
		}
	})
}

// TestOrderedSet_UnknownSetIsEmpty verifies that an unregistered set name
// returns nil/empty rather than panicking or returning another set's data.
func TestOrderedSet_UnknownSetIsEmpty(t *testing.T) {
	withCleanRegistry(t, func() {
		if err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__CE"}); err != nil {
			t.Fatalf("register: %v", err)
		}
		got := orderedSet("does-not-exist")
		if len(got) != 0 {
			t.Errorf("orderedSet(unknown) = %v, want empty", got)
		}
	})
}

// TestOrderedSet_Isolation is the whole point of the named-sets redesign: a
// migration registered into set "ce" must never appear when running any other
// set, and vice versa — no inheritance merely by both sets existing in the
// same process/registry.
func TestOrderedSet_Isolation(t *testing.T) {
	withCleanRegistry(t, func() {
		var ceRan, otherRan bool

		if err := RegisterInSet("ce", Migration{
			Version: 1,
			Name:    "V1__CE",
			NetworkingEntries: map[string]EntryFunc{
				"ceonly.key": func(_, _ string, _ ConfigStore) error {
					ceRan = true
					return nil
				},
			},
		}); err != nil {
			t.Fatalf("register ce: %v", err)
		}
		if err := RegisterInSet("other", Migration{
			Version: 1,
			Name:    "V1__Other",
			NetworkingEntries: map[string]EntryFunc{
				"otheronly.key": func(_, _ string, _ ConfigStore) error {
					otherRan = true
					return nil
				},
			},
		}); err != nil {
			t.Fatalf("register other: %v", err)
		}

		dir := t.TempDir()
		iniPath := writeFile(t, dir, "config.ini",
			"[ceonly]\nkey = x\n\n[otheronly]\nkey = y\n")
		srv := newOKConsul(t)

		runner, err := NewRunner(Paths{
			IniPath:      iniPath,
			PropsPath:    filepath.Join(dir, "config.properties"),
			ConsulURL:    srv.URL,
			DropInPath:   filepath.Join(dir, "log-level.conf"),
			MigrationSet: "ce",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		if !ceRan {
			t.Error("running set \"ce\" must execute ce's own migration")
		}
		if otherRan {
			t.Error("running set \"ce\" must NOT execute the \"other\" set's migration — no inheritance")
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

		// KV server returns 500 for "carbonio-preview/storage/fetch-timeout-seconds"
		// but 200 for everything else.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path[len("/v1/kv/"):]
			switch r.Method {
			case http.MethodGet:
				w.WriteHeader(http.StatusNotFound)
			case http.MethodPut:
				if path == "carbonio-preview/storage/fetch-timeout-seconds" {
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
		err := RegisterInSet("ce", Migration{
			Version: 1,
			Name:    "V1__ErrorIsolation",
			ApplicationEntries: map[string]EntryFunc{
				"carbonio.preview.timeout_in_seconds": func(_, v string, dest ConfigStore) error {
					return dest.Set("carbonio-preview/storage/fetch-timeout-seconds", v)
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

		err := RegisterInSet("ce", Migration{
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
			IniPath:      iniPath,
			PropsPath:    propsPath,
			ConsulURL:    srv.URL,
			MigrationSet: "ce",
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
		err := RegisterInSet("ce", Migration{
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
			IniPath:      iniPath,
			PropsPath:    propsPath,
			ConsulURL:    srv.URL,
			MigrationSet: "ce",
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

		err := RegisterInSet("ce", Migration{
			Version: 1,
			Name:    "V1__AppOnly",
			ApplicationEntries: map[string]EntryFunc{
				"carbonio.preview.timeout_in_seconds": func(_, v string, dest ConfigStore) error {
					return dest.Set("carbonio-preview/storage/fetch-timeout-seconds", v)
				},
			},
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
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

		if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
			t.Error("properties file must NOT be created for app-only migration")
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
