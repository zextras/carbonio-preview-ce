// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── runDropInEnvEntries filesystem-error arms ─────────────────────────────────
//
// Note: the write/close/chmod arms of runDropInEnvEntries (writing to the
// just-created temp file, closing it, and chmod-ing it) are NOT covered here.
// They are only reachable via fault injection on a file the function itself just
// created successfully in a writable directory — there is no portable,
// non-root way to make those specific syscalls fail without faking the
// filesystem. The MkdirAll, CreateTemp, and rename arms ARE covered below.

// genericDropInMigration returns a literal, framework-only fixture equivalent
// in shape to a real drop-in migration (log.level → PREVIEW_LOG_LEVEL), used
// so these framework-mechanics tests don't need to import a concrete
// migration set (that would defeat the point of moving CE's V1 out of this
// package into config/migrate/cemig).
func genericDropInMigration() Migration {
	return Migration{
		Version: 1,
		Name:    "V1__GenericDropIn",
		DropInEnvEntries: map[string]string{
			"log.level": "PREVIEW_LOG_LEVEL",
		},
	}
}

// TestRunDropInEnvEntries_MkdirAllFails covers the MkdirAll-error arm: the
// drop-in destination's parent cannot be created because an ancestor path
// component is a regular file (not a directory).
func TestRunDropInEnvEntries_MkdirAllFails(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		// Create a regular file where a directory is expected in the path.
		blocker := writeFile(t, dir, "blocker", "x")
		// dest's parent is <blocker>/sub — MkdirAll must fail (blocker is a file).
		dropInPath := filepath.Join(blocker, "sub", "log-level.conf")

		iniPath := writeFile(t, dir, "config.ini", "[log]\nlevel = debug\n")
		srv := newOKConsul(t)

		if err := RegisterInSet("ce", genericDropInMigration()); err != nil {
			t.Fatalf("register: %v", err)
		}
		runner, err := NewRunner(Paths{
			IniPath:      iniPath,
			PropsPath:    filepath.Join(dir, "config.properties"),
			ConsulURL:    srv.URL,
			DropInPath:   dropInPath,
			MigrationSet: "ce",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		// runOne via runDropInEnvEntries must hit the MkdirAll error arm and
		// return 0 migrated for the drop-in; the log.level key must therefore
		// survive in the ini (retryable on next run).
		n := runner.runDropInEnvEntries(genericDropInMigration())
		if n != 0 {
			t.Errorf("runDropInEnvEntries=%d, want 0 on MkdirAll failure", n)
		}
		if _, ok := runner.ini.get("log.level"); !ok {
			t.Error("log.level must survive in ini when drop-in write fails (retryable)")
		}
	})
}

// TestRunDropInEnvEntries_RenameFails covers the rename-error arm by making the
// drop-in parent directory read-only AFTER MkdirAll would have succeeded: we
// pre-create the parent dir, chmod it 0555, so CreateTemp inside it fails.
func TestRunDropInEnvEntries_CreateTempFails(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		parent := filepath.Join(dir, "ro")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatalf("mkdir parent: %v", err)
		}
		if err := os.Chmod(parent, 0o555); err != nil {
			t.Fatalf("chmod parent: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
		dropInPath := filepath.Join(parent, "log-level.conf")

		iniPath := writeFile(t, dir, "config.ini", "[log]\nlevel = debug\n")
		srv := newOKConsul(t)
		if err := RegisterInSet("ce", genericDropInMigration()); err != nil {
			t.Fatalf("register: %v", err)
		}
		runner, err := NewRunner(Paths{
			IniPath:      iniPath,
			PropsPath:    filepath.Join(dir, "config.properties"),
			ConsulURL:    srv.URL,
			DropInPath:   dropInPath,
			MigrationSet: "ce",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		n := runner.runDropInEnvEntries(genericDropInMigration())
		if n != 0 {
			t.Errorf("runDropInEnvEntries=%d, want 0 on CreateTemp failure", n)
		}
		// Nothing should have been written into the read-only parent.
		if _, err := os.Stat(dropInPath); !os.IsNotExist(err) {
			t.Error("drop-in must not have been created in a read-only directory")
		}
	})
}

// TestRunDropInEnvEntries_RenameFails covers the rename-error arm: the temp file
// is created and written successfully (parent dir writable), but the destination
// path is a pre-existing directory, so os.Rename(tmp, dest) fails. The drop-in
// keys must survive in the ini (retryable).
func TestRunDropInEnvEntries_RenameFails(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		// dest is a DIRECTORY → rename of a file onto it fails.
		dropInPath := filepath.Join(dir, "log-level.conf")
		if err := os.MkdirAll(dropInPath, 0o755); err != nil {
			t.Fatalf("mkdir dest dir: %v", err)
		}

		iniPath := writeFile(t, dir, "config.ini", "[log]\nlevel = debug\n")
		srv := newOKConsul(t)
		if err := RegisterInSet("ce", genericDropInMigration()); err != nil {
			t.Fatalf("register: %v", err)
		}
		runner, err := NewRunner(Paths{
			IniPath:      iniPath,
			PropsPath:    filepath.Join(dir, "config.properties"),
			ConsulURL:    srv.URL,
			DropInPath:   dropInPath,
			MigrationSet: "ce",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		n := runner.runDropInEnvEntries(genericDropInMigration())
		if n != 0 {
			t.Errorf("runDropInEnvEntries=%d, want 0 on rename failure", n)
		}
		if _, ok := runner.ini.get("log.level"); !ok {
			t.Error("log.level must survive in ini when rename fails (retryable)")
		}
		// No stray temp files should be left in the dir.
		matches, _ := filepath.Glob(filepath.Join(dir, "drop-in-*.conf.tmp"))
		if len(matches) != 0 {
			t.Errorf("temp file not cleaned up after rename failure: %v", matches)
		}
	})
}

// ── Run() save-error arms ─────────────────────────────────────────────────────

// TestRun_PropertiesSaveError covers the Run() arm where r.net.dirty is true but
// r.net.Save() fails. We point the properties file inside a read-only directory
// (parent missing at load time → treated as absent; MkdirAll inside Save() fails
// because the read-only ancestor cannot be written). The save warning path must
// not panic and must leave the run otherwise complete.
func TestRun_PropertiesSaveError(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		// Read-only directory; the properties file targets a missing sub-dir
		// under it so MkdirAll fails during Save().
		roDir := filepath.Join(dir, "ro")
		if err := os.MkdirAll(roDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Chmod(roDir, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })
		propsPath := filepath.Join(roDir, "sub", "config.properties")

		iniPath := writeFile(t, dir, "config.ini",
			"[myservice]\nfoo = bar\n")
		srv := newOKConsul(t)

		if err := RegisterInSet("ce", Migration{
			Version: 1,
			Name:    "V1__NetOnly",
			NetworkingEntries: map[string]EntryFunc{
				"myservice.foo": func(_, v string, dest ConfigStore) error {
					return dest.Set("new.foo", v)
				},
			},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
		runner, err := NewRunner(Paths{
			IniPath:      iniPath,
			PropsPath:    propsPath,
			ConsulURL:    srv.URL,
			DropInPath:   filepath.Join(dir, "log-level.conf"),
			MigrationSet: "ce",
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		// Run must complete without panicking even though Save() warns+fails.
		runner.Run()
		if _, err := os.Stat(propsPath); !os.IsNotExist(err) {
			t.Error("properties file must not exist after a Save() that failed")
		}
	})
}

// TestRun_IniSaveError covers the Run() arm where r.ini.save() fails. All ini
// keys migrate, so the store becomes empty and save() takes the rename branch
// (os.Rename to "<path>.migrated"). We make the ini's parent directory read-only
// after load so the rename fails (rename needs write permission on the dir). The
// warning path must not panic.
func TestRun_IniSaveError(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		iniDir := filepath.Join(dir, "inidir")
		if err := os.MkdirAll(iniDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		iniPath := writeFile(t, iniDir, "config.ini",
			"[myservice]\nfoo = bar\n")
		srv := newOKConsul(t)

		// Migrate the only key so the ini becomes empty → save() renames.
		if err := RegisterInSet("ce", Migration{
			Version: 1,
			Name:    "V1__AllMigrate",
			NetworkingEntries: map[string]EntryFunc{
				"myservice.foo": func(_, v string, dest ConfigStore) error {
					return dest.Set("new.foo", v)
				},
			},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
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

		// Make the ini directory read-only so the rename inside ini.save() fails.
		if err := os.Chmod(iniDir, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(iniDir, 0o755) })

		runner.Run() // must warn on ini.save() rename failure without panicking

		// The ini must still exist (rename failed → not renamed away).
		if _, err := os.Stat(iniPath); err != nil {
			t.Errorf("ini should still exist after failed rename: %v", err)
		}
	})
}

// TestRun_IniSaveToError covers the ini.save() SaveTo branch error: the ini is
// left non-empty (an advanced key survives → SaveTo, not rename), and the ini
// file itself is made read-only so ioutil.WriteFile fails. The warning path must
// not panic.
func TestRun_IniSaveToError(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		iniPath := writeFile(t, dir, "config.ini",
			"[myservice]\nfoo = bar\nadvanced_key = keep\n")
		srv := newOKConsul(t)

		if err := RegisterInSet("ce", Migration{
			Version: 1,
			Name:    "V1__PartialKeep",
			NetworkingEntries: map[string]EntryFunc{
				"myservice.foo": func(_, v string, dest ConfigStore) error {
					return dest.Set("new.foo", v)
				},
			},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
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

		// Make the ini FILE read-only so SaveTo's WriteFile (O_TRUNC|O_WRONLY) fails.
		if err := os.Chmod(iniPath, 0o444); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(iniPath, 0o644) })

		runner.Run() // must warn on ini.save() SaveTo failure without panicking
	})
}

// TestRun_NoMigrations covers the early-return arm of Run() when the registry is
// empty.
func TestRun_NoMigrations(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		srv := newOKConsul(t)
		runner, err := NewRunner(Paths{
			IniPath:    filepath.Join(dir, "no.ini"),
			PropsPath:  filepath.Join(dir, "config.properties"),
			ConsulURL:  srv.URL,
			DropInPath: filepath.Join(dir, "log-level.conf"),
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run() // registry empty → "no migrations found", no panic
	})
}

// ── consulKvStore.Set direct error arms ───────────────────────────────────────

// TestConsulKvStore_Set_Non200 covers the non-200 status arm of Set.
func TestConsulKvStore_Set_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newConsulKvStore(srv.URL, "tok")
	err := store.Set("carbonio-preview/key", "value")
	if err == nil {
		t.Fatal("want error on non-200 Consul response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error = %v, want it to mention HTTP 500", err)
	}
}

// TestConsulKvStore_Set_RequestBuildError covers the http.NewRequest build-error
// arm by passing a base URL containing a control character, which makes the URL
// unparseable.
func TestConsulKvStore_Set_RequestBuildError(t *testing.T) {
	store := newConsulKvStore("http://127.0.0.1:8500", "tok")
	// A key with a control byte forces url parsing inside http.NewRequest to fail.
	err := store.Set("bad\x7fkey", "value")
	if err == nil {
		t.Fatal("want build error for URL with control character")
	}
	if !strings.Contains(err.Error(), "build PUT") {
		t.Errorf("error = %v, want it to wrap the build-request message", err)
	}
}

// TestConsulKvStore_Set_TransportError covers the client.Do error arm by
// pointing the store at a closed port.
func TestConsulKvStore_Set_TransportError(t *testing.T) {
	store := newConsulKvStore("http://127.0.0.1:1", "tok")
	err := store.Set("carbonio-preview/key", "value")
	if err == nil {
		t.Fatal("want transport error when Consul is unreachable")
	}
}

// ── propertiesStore.Save FS-error arm ─────────────────────────────────────────

// TestPropertiesStore_Save_MkdirError covers the MkdirAll-error arm of Save by
// pointing the file at a missing sub-directory under a read-only parent: load()
// sees the file as absent (ENOENT) so construction succeeds, but MkdirAll inside
// Save() cannot create the sub-directory under the read-only ancestor.
func TestPropertiesStore_Save_MkdirError(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "ro")
	if err := os.MkdirAll(roDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	store, err := newPropertiesStore(filepath.Join(roDir, "sub", "config.properties"))
	if err != nil {
		t.Fatalf("newPropertiesStore: %v", err)
	}
	if err := store.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Save(); err == nil {
		t.Fatal("want MkdirAll error when properties parent is a regular file")
	}
}

// TestPropertiesStore_Save_CreateError covers the os.Create-error arm of Save:
// the parent directory exists but is read-only, so creating the file fails
// after MkdirAll (which is a no-op on an existing dir) succeeds.
func TestPropertiesStore_Save_CreateError(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "ro")
	if err := os.MkdirAll(roDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	store, err := newPropertiesStore(filepath.Join(roDir, "config.properties"))
	if err != nil {
		t.Fatalf("newPropertiesStore: %v", err)
	}
	if err := store.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Save(); err == nil {
		t.Fatal("want os.Create error when parent directory is read-only")
	}
}

// newOKConsul returns a Consul stub that accepts PUT/DELETE with 200 and returns
// 404 on GET (KV prefix not populated).
func newOKConsul(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
