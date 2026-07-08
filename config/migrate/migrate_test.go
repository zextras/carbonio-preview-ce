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
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// withCleanRegistry runs f with every migration set AND every registered
// bootstrap cleared, and restores the originals afterwards.  NOT safe for
// parallel sub-tests.
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
	savedBootstraps := make(map[string]Bootstrap, len(bootstraps))
	for k, v := range bootstraps {
		savedBootstraps[k] = v
	}
	sets = map[string][]Migration{}
	bootstraps = map[string]Bootstrap{}
	setsMu.Unlock()

	f()

	// restore
	setsMu.Lock()
	sets = saved
	bootstraps = savedBootstraps
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
		m := Migration{Version: 1, Name: "V1__Alpha", ApplicationKVMoves: []KVMove{{OldPath: "svc/alpha-old", NewPath: "svc/alpha-new"}}}
		if err := RegisterInSet("ce", m); err != nil {
			t.Fatalf("first register: %v", err)
		}
		err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__Beta", ApplicationKVMoves: []KVMove{{OldPath: "svc/beta-old", NewPath: "svc/beta-new"}}})
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
		if err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__CE", ApplicationKVMoves: []KVMove{{OldPath: "ce/old", NewPath: "ce/new"}}}); err != nil {
			t.Fatalf("register ce V1: %v", err)
		}
		if err := RegisterInSet("advanced", Migration{Version: 1, Name: "V1__Advanced", ApplicationKVMoves: []KVMove{{OldPath: "advanced/old", NewPath: "advanced/new"}}}); err != nil {
			t.Fatalf("register advanced V1 (same version, different set): %v", err)
		}
	})
}

// TestRegisterInSet_RejectsMoveLessMigration verifies that a Vn migration
// with an empty ApplicationKVMoves list is rejected at registration time: it
// would do nothing at all, which is now a programming error rather than a
// (formerly valid) ini-only migration.
func TestRegisterInSet_RejectsMoveLessMigration(t *testing.T) {
	withCleanRegistry(t, func() {
		err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__NoMoves"})
		if err == nil {
			t.Fatal("expected error for a migration with no ApplicationKVMoves, got nil")
		}
		if !strings.Contains(err.Error(), "ApplicationKVMoves") {
			t.Errorf("error = %v, want it to mention ApplicationKVMoves", err)
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

// ── Bootstrap registration tests ─────────────────────────────────────────────

// TestRegisterBootstrap_Success verifies the basic happy path: a well-formed
// Bootstrap registers cleanly and bootstrapFor can find it back.
func TestRegisterBootstrap_Success(t *testing.T) {
	withCleanRegistry(t, func() {
		b := Bootstrap{Name: "Bootstrap__MigrateFromPythonIni"}
		if err := RegisterBootstrap("ce", b); err != nil {
			t.Fatalf("RegisterBootstrap: %v", err)
		}
		got, ok := bootstrapFor("ce")
		if !ok {
			t.Fatal("bootstrapFor(\"ce\") ok=false, want true after registration")
		}
		if got.Name != b.Name {
			t.Errorf("bootstrapFor(\"ce\").Name = %q, want %q", got.Name, b.Name)
		}
	})
}

// TestRegisterBootstrap_EmptyNameRejected verifies that a Bootstrap with an
// empty (or all-whitespace) Name is rejected at registration time.
func TestRegisterBootstrap_EmptyNameRejected(t *testing.T) {
	withCleanRegistry(t, func() {
		if err := RegisterBootstrap("ce", Bootstrap{Name: ""}); err == nil {
			t.Error("expected error for empty bootstrap name, got nil")
		}
		if err := RegisterBootstrap("ce", Bootstrap{Name: "   "}); err == nil {
			t.Error("expected error for whitespace-only bootstrap name, got nil")
		}
	})
}

// TestRegisterBootstrap_DuplicateInSameSetRejected verifies that only one
// Bootstrap may be registered per set: a one-time ini→KV import only ever
// happens once per edition.
func TestRegisterBootstrap_DuplicateInSameSetRejected(t *testing.T) {
	withCleanRegistry(t, func() {
		if err := RegisterBootstrap("ce", Bootstrap{Name: "V1__First"}); err != nil {
			t.Fatalf("first RegisterBootstrap: %v", err)
		}
		err := RegisterBootstrap("ce", Bootstrap{Name: "V1__Second"})
		if err == nil {
			t.Fatal("expected error registering a second bootstrap into the same set, got nil")
		}
	})
}

// TestRegisterBootstrap_DifferentSetsAllowed verifies that each set may have
// its own independent bootstrap — e.g. CE and Advanced each register their
// own one-time ini→KV import without colliding.
func TestRegisterBootstrap_DifferentSetsAllowed(t *testing.T) {
	withCleanRegistry(t, func() {
		if err := RegisterBootstrap("ce", Bootstrap{Name: "V1__CE"}); err != nil {
			t.Fatalf("register ce bootstrap: %v", err)
		}
		if err := RegisterBootstrap("advanced", Bootstrap{Name: "V1__Advanced"}); err != nil {
			t.Fatalf("register advanced bootstrap (different set): %v", err)
		}
	})
}

// TestBootstrapFor_UnknownSetNotFound verifies that an unregistered set name
// reports ok=false rather than a zero-value Bootstrap being mistaken for a
// real registration.
func TestBootstrapFor_UnknownSetNotFound(t *testing.T) {
	withCleanRegistry(t, func() {
		_, ok := bootstrapFor("does-not-exist")
		if ok {
			t.Error("bootstrapFor(unknown set) ok=true, want false")
		}
	})
}

func TestOrderedSet_VersionAscending(t *testing.T) {
	withCleanRegistry(t, func() {
		for _, v := range []int{3, 1, 2} {
			name := fmt.Sprintf("V%d__Test", v)
			err := RegisterInSet("ce", Migration{
				Version: v,
				Name:    name,
				ApplicationKVMoves: []KVMove{
					{OldPath: fmt.Sprintf("svc/v%d/old", v), NewPath: fmt.Sprintf("svc/v%d/new", v)},
				},
			})
			if err != nil {
				t.Fatalf("register V%d: %v", v, err)
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
		if err := RegisterInSet("ce", Migration{Version: 1, Name: "V1__CE", ApplicationKVMoves: []KVMove{{OldPath: "ce/old", NewPath: "ce/new"}}}); err != nil {
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
		if err := RegisterInSet("ce", Migration{
			Version:            1,
			Name:               "V1__CE",
			ApplicationKVMoves: []KVMove{{OldPath: "ceonly/old", NewPath: "ceonly/new"}},
		}); err != nil {
			t.Fatalf("register ce: %v", err)
		}
		if err := RegisterInSet("other", Migration{
			Version:            1,
			Name:               "V1__Other",
			ApplicationKVMoves: []KVMove{{OldPath: "otheronly/old", NewPath: "otheronly/new"}},
		}); err != nil {
			t.Fatalf("register other: %v", err)
		}

		data := map[string]string{"ceonly/old": "x", "otheronly/old": "y"}
		srv := newStatefulConsul(t, data, nil, nil)

		dir := t.TempDir()
		runner, err := NewRunner(Paths{
			IniPath:      filepath.Join(dir, "no.ini"),
			PropsPath:    filepath.Join(dir, "config.properties"),
			ConsulURL:    srv.URL,
			ConsulToken:  "tok",
			DropInPath:   filepath.Join(dir, "log-level.conf"),
			MigrationSet: "ce",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		if _, ok := data["ceonly/new"]; !ok {
			t.Error("running set \"ce\" must execute ce's own migration")
		}
		if _, ok := data["otheronly/new"]; ok {
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

		// Register a bootstrap with two application entries.
		err := RegisterBootstrap("ce", Bootstrap{
			Name: "V1__ErrorIsolation",
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

		err := RegisterBootstrap("ce", Bootstrap{
			Name: "V1__Rename",
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

		// Only register a bootstrap entry for "myservice.foo", not for "myservice.advanced_key".
		err := RegisterBootstrap("ce", Bootstrap{
			Name: "V1__PartialMigration",
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

		err := RegisterBootstrap("ce", Bootstrap{
			Name: "V1__AppOnly",
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
