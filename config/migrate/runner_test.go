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
	"strconv"
	"strings"
	"sync"
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

// ── consulKvStore.Get / Delete direct arms ────────────────────────────────────

// TestConsulKvStore_Get_PresentAndAbsent covers the 200 (value, true, nil) and
// 404 ("", false, nil) arms of Get, and verifies the token header is sent and
// the ?raw query is on the wire (without it Consul would return a JSON
// metadata array, not the bare value).
func TestConsulKvStore_Get_PresentAndAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Consul-Token") != "tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.RawQuery != "raw" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/v1/kv/carbonio-preview/present" {
			fmt.Fprint(w, "the-value")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	store := newConsulKvStore(srv.URL, "tok")
	v, ok, err := store.Get("carbonio-preview/present")
	if err != nil || !ok || v != "the-value" {
		t.Errorf("Get(present) = (%q, %v, %v), want (\"the-value\", true, nil)", v, ok, err)
	}
	v, ok, err = store.Get("carbonio-preview/missing")
	if err != nil || ok || v != "" {
		t.Errorf("Get(missing) = (%q, %v, %v), want (\"\", false, nil) — 404 is not an error", v, ok, err)
	}
}

// TestConsulKvStore_Get_Non200 covers the non-200/non-404 status arm of Get.
func TestConsulKvStore_Get_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newConsulKvStore(srv.URL, "tok")
	_, _, err := store.Get("carbonio-preview/key")
	if err == nil {
		t.Fatal("want error on non-200/non-404 Consul response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error = %v, want it to mention HTTP 500", err)
	}
}

// TestConsulKvStore_Get_BodyReadError covers the io.ReadAll error arm: the
// server promises more bytes (Content-Length) than it writes, so the client's
// body read fails with an unexpected EOF.
func TestConsulKvStore_Get_BodyReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "short")
	}))
	defer srv.Close()

	store := newConsulKvStore(srv.URL, "tok")
	_, _, err := store.Get("carbonio-preview/key")
	if err == nil {
		t.Fatal("want body-read error when server truncates the response")
	}
}

// TestConsulKvStore_Get_RequestBuildError covers the http.NewRequest build-error
// arm of Get (control byte in the key makes the URL unparseable).
func TestConsulKvStore_Get_RequestBuildError(t *testing.T) {
	store := newConsulKvStore("http://127.0.0.1:8500", "tok")
	_, _, err := store.Get("bad\x7fkey")
	if err == nil {
		t.Fatal("want build error for URL with control character")
	}
	if !strings.Contains(err.Error(), "build GET") {
		t.Errorf("error = %v, want it to wrap the build-request message", err)
	}
}

// TestConsulKvStore_Get_TransportError covers the client.Do error arm of Get.
func TestConsulKvStore_Get_TransportError(t *testing.T) {
	store := newConsulKvStore("http://127.0.0.1:1", "tok")
	_, _, err := store.Get("carbonio-preview/key")
	if err == nil {
		t.Fatal("want transport error when Consul is unreachable")
	}
}

// TestConsulKvStore_Delete_Non200 covers the non-200 status arm of Delete.
func TestConsulKvStore_Delete_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newConsulKvStore(srv.URL, "tok")
	err := store.Delete("carbonio-preview/key")
	if err == nil {
		t.Fatal("want error on non-200 Consul response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error = %v, want it to mention HTTP 500", err)
	}
}

// TestConsulKvStore_Delete_RequestBuildError covers the http.NewRequest
// build-error arm of Delete.
func TestConsulKvStore_Delete_RequestBuildError(t *testing.T) {
	store := newConsulKvStore("http://127.0.0.1:8500", "tok")
	err := store.Delete("bad\x7fkey")
	if err == nil {
		t.Fatal("want build error for URL with control character")
	}
	if !strings.Contains(err.Error(), "build DELETE") {
		t.Errorf("error = %v, want it to wrap the build-request message", err)
	}
}

// TestConsulKvStore_Delete_TransportError covers the client.Do error arm of Delete.
func TestConsulKvStore_Delete_TransportError(t *testing.T) {
	store := newConsulKvStore("http://127.0.0.1:1", "tok")
	err := store.Delete("carbonio-preview/key")
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

// ── Application KV-move tests ─────────────────────────────────────────────────

// newStatefulConsul returns a Consul KV stub backed by the given in-memory data
// map: GET /v1/kv/<path>?raw serves the map (404 when absent; 500 when the
// ?raw query is missing — a real Consul would answer a JSON metadata array,
// not the bare value), PUT writes into it, DELETE removes from it.  Paths
// listed in failPut / failDelete return HTTP 500 for that method instead, to
// simulate per-key Consul failures.
// The test may inspect the data map after the runner has finished.
func newStatefulConsul(t *testing.T, data map[string]string, failPut, failDelete map[string]bool) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if r.URL.RawQuery != "raw" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if v, ok := data[path]; ok {
				fmt.Fprint(w, v)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			if failPut[path] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			body, _ := io.ReadAll(r.Body)
			data[path] = string(body)
			fmt.Fprint(w, "true")
		case http.MethodDelete:
			if failDelete[path] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			delete(data, path)
			fmt.Fprint(w, "true")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newMoveRunner builds a runner with an ABSENT legacy ini — KV moves read from
// Consul KV itself and must run regardless of the ini — pointed at consulURL.
func newMoveRunner(t *testing.T, consulURL string) *Runner {
	t.Helper()
	dir := t.TempDir()
	runner, err := NewRunner(Paths{
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

// TestRunApplicationKVMoves_IdentityMove runs a moves-only migration through the
// full Run() path (registered set, absent ini): the old key's value must be
// copied verbatim to the new path and the old key deleted.
func TestRunApplicationKVMoves_IdentityMove(t *testing.T) {
	withCleanRegistry(t, func() {
		data := map[string]string{"svc/pool/max-connections": "7"}
		srv := newStatefulConsul(t, data, nil, nil)

		if err := RegisterInSet("ce", Migration{
			Version: 2,
			Name:    "V2__MoveOnly",
			ApplicationKVMoves: []KVMove{
				{OldPath: "svc/pool/max-connections", NewPath: "svc/db-pool-max-size"},
			},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner := newMoveRunner(t, srv.URL)
		runner.Run()

		if got := data["svc/db-pool-max-size"]; got != "7" {
			t.Errorf("new key = %q, want value copied verbatim (%q)", got, "7")
		}
		if _, ok := data["svc/pool/max-connections"]; ok {
			t.Error("old key must be deleted after a successful move")
		}
	})
}

// TestRunApplicationKVMoves_Transform verifies a value transform (seconds →
// milliseconds style, ×1000) is applied before the write.
func TestRunApplicationKVMoves_Transform(t *testing.T) {
	data := map[string]string{"svc/pool/lifetime-seconds": "1800"}
	srv := newStatefulConsul(t, data, nil, nil)
	runner := newMoveRunner(t, srv.URL)

	m := Migration{
		Version: 2,
		Name:    "V2__MoveTransform",
		ApplicationKVMoves: []KVMove{{
			OldPath: "svc/pool/lifetime-seconds",
			NewPath: "svc/db-pool-max-lifetime",
			Transform: func(old string) (string, error) {
				n, err := strconv.Atoi(old)
				if err != nil {
					return "", err
				}
				return strconv.Itoa(n * 1000), nil
			},
		}},
	}
	if n := runner.runApplicationKVMoves(m); n != 1 {
		t.Errorf("runApplicationKVMoves = %d, want 1", n)
	}
	if got := data["svc/db-pool-max-lifetime"]; got != "1800000" {
		t.Errorf("new key = %q, want transformed value %q", got, "1800000")
	}
	if _, ok := data["svc/pool/lifetime-seconds"]; ok {
		t.Error("old key must be deleted after a successful transformed move")
	}
}

// TestRunApplicationKVMoves_OldAbsent verifies the nothing-to-migrate arm: no
// writes and no deletes happen when the old key does not exist.
func TestRunApplicationKVMoves_OldAbsent(t *testing.T) {
	data := map[string]string{}
	srv := newStatefulConsul(t, data, nil, nil)
	runner := newMoveRunner(t, srv.URL)

	m := Migration{
		Version:            2,
		Name:               "V2__MoveAbsent",
		ApplicationKVMoves: []KVMove{{OldPath: "svc/old", NewPath: "svc/new"}},
	}
	if n := runner.runApplicationKVMoves(m); n != 0 {
		t.Errorf("runApplicationKVMoves = %d, want 0 when old key is absent", n)
	}
	if len(data) != 0 {
		t.Errorf("no KV writes must happen when the old key is absent, got %v", data)
	}
}

// TestRunApplicationKVMoves_NewAlreadyPresent verifies the never-clobber arm:
// when the new path already holds an operator value, the move is skipped
// entirely — the new value is untouched and the old key survives (harmless
// leftover).
func TestRunApplicationKVMoves_NewAlreadyPresent(t *testing.T) {
	data := map[string]string{
		"svc/old": "operator-old",
		"svc/new": "operator-new",
	}
	srv := newStatefulConsul(t, data, nil, nil)
	runner := newMoveRunner(t, srv.URL)

	m := Migration{
		Version:            2,
		Name:               "V2__MoveNoClobber",
		ApplicationKVMoves: []KVMove{{OldPath: "svc/old", NewPath: "svc/new"}},
	}
	if n := runner.runApplicationKVMoves(m); n != 0 {
		t.Errorf("runApplicationKVMoves = %d, want 0 when new key already exists", n)
	}
	if got := data["svc/new"]; got != "operator-new" {
		t.Errorf("new key = %q, must never be clobbered (want %q)", got, "operator-new")
	}
	if got := data["svc/old"]; got != "operator-old" {
		t.Errorf("old key = %q, must survive untouched when the move is skipped (want %q)", got, "operator-old")
	}
}

// TestRunApplicationKVMoves_TransformErrorContinues verifies the warn-and-
// continue arm: a Transform error (e.g. a non-numeric operator value) must not
// write, not delete, and not stop the remaining moves.
func TestRunApplicationKVMoves_TransformErrorContinues(t *testing.T) {
	data := map[string]string{
		"svc/bad-old":  "not-a-number",
		"svc/good-old": "5",
	}
	srv := newStatefulConsul(t, data, nil, nil)
	runner := newMoveRunner(t, srv.URL)

	m := Migration{
		Version: 2,
		Name:    "V2__MoveTransformError",
		ApplicationKVMoves: []KVMove{
			{
				OldPath: "svc/bad-old",
				NewPath: "svc/bad-new",
				Transform: func(old string) (string, error) {
					n, err := strconv.Atoi(old)
					if err != nil {
						return "", err
					}
					return strconv.Itoa(n * 1000), nil
				},
			},
			{OldPath: "svc/good-old", NewPath: "svc/good-new"},
		},
	}
	if n := runner.runApplicationKVMoves(m); n != 1 {
		t.Errorf("runApplicationKVMoves = %d, want 1 (bad move skipped, good move done)", n)
	}
	if got := data["svc/bad-old"]; got != "not-a-number" {
		t.Errorf("bad old key = %q, must survive when transform fails (want %q)", got, "not-a-number")
	}
	if _, ok := data["svc/bad-new"]; ok {
		t.Error("bad new key must NOT be written when transform fails")
	}
	if got := data["svc/good-new"]; got != "5" {
		t.Errorf("good new key = %q, the run must continue to the next move (want %q)", got, "5")
	}
	if _, ok := data["svc/good-old"]; ok {
		t.Error("good old key must be deleted (subsequent move completed normally)")
	}
}

// TestRunApplicationKVMoves_SetFails verifies the retryable arm: when the PUT
// to the new path fails, the old key is untouched (retry on next run) and the
// move does not count as migrated.
func TestRunApplicationKVMoves_SetFails(t *testing.T) {
	data := map[string]string{"svc/old": "42"}
	srv := newStatefulConsul(t, data, map[string]bool{"svc/new": true}, nil)
	runner := newMoveRunner(t, srv.URL)

	m := Migration{
		Version:            2,
		Name:               "V2__MoveSetFails",
		ApplicationKVMoves: []KVMove{{OldPath: "svc/old", NewPath: "svc/new"}},
	}
	if n := runner.runApplicationKVMoves(m); n != 0 {
		t.Errorf("runApplicationKVMoves = %d, want 0 when Set fails", n)
	}
	if got := data["svc/old"]; got != "42" {
		t.Errorf("old key = %q, must survive when Set fails (retryable, want %q)", got, "42")
	}
	if _, ok := data["svc/new"]; ok {
		t.Error("new key must be absent when Set failed")
	}
}

// TestRunApplicationKVMoves_DeleteFails verifies the no-rollback arm: when Set
// succeeds but the DELETE of the old key fails, the new value stays in place,
// the stale old key is left behind (warned), and no failure propagates.
func TestRunApplicationKVMoves_DeleteFails(t *testing.T) {
	data := map[string]string{"svc/old": "42"}
	srv := newStatefulConsul(t, data, nil, map[string]bool{"svc/old": true})
	runner := newMoveRunner(t, srv.URL)

	m := Migration{
		Version:            2,
		Name:               "V2__MoveDeleteFails",
		ApplicationKVMoves: []KVMove{{OldPath: "svc/old", NewPath: "svc/new"}},
	}
	if n := runner.runApplicationKVMoves(m); n != 1 {
		t.Errorf("runApplicationKVMoves = %d, want 1 (move done, delete warned)", n)
	}
	if got := data["svc/new"]; got != "42" {
		t.Errorf("new key = %q, must be in place even when the old-key delete fails (want %q)", got, "42")
	}
	if got := data["svc/old"]; got != "42" {
		t.Errorf("old key = %q, expected the stale leftover to survive the failed delete (want %q)", got, "42")
	}
}

// TestHasApplicationWork_TrueWithOnlyKVMoves verifies the token gate: a
// migration whose only content is ApplicationKVMoves counts as application
// work even when the legacy ini is absent (moves talk to Consul KV directly).
func TestHasApplicationWork_TrueWithOnlyKVMoves(t *testing.T) {
	withCleanRegistry(t, func() {
		if err := RegisterInSet("ce", Migration{
			Version:            2,
			Name:               "V2__MovesOnly",
			ApplicationKVMoves: []KVMove{{OldPath: "svc/old", NewPath: "svc/new"}},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
		// No Consul contact happens in HasApplicationWork — a dead URL is fine.
		runner := newMoveRunner(t, "http://127.0.0.1:1")
		if !runner.HasApplicationWork() {
			t.Error("a migration with only KV moves must count as application work even with an absent ini")
		}
	})
}

// TestRunSetup_KVMovesRequireToken verifies the RunSetup token gate end to end:
// with a moves-only migration and no SETUP_CONSUL_TOKEN, setup must fail before
// touching Consul.
func TestRunSetup_KVMovesRequireToken(t *testing.T) {
	withCleanRegistry(t, func() {
		t.Setenv("SETUP_CONSUL_TOKEN", "")
		if err := RegisterInSet("ce", Migration{
			Version:            2,
			Name:               "V2__MovesOnly",
			ApplicationKVMoves: []KVMove{{OldPath: "svc/old", NewPath: "svc/new"}},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
		dir := t.TempDir()
		paths := Paths{
			IniPath:      filepath.Join(dir, "no.ini"),
			PropsPath:    filepath.Join(dir, "config.properties"),
			DropInPath:   filepath.Join(dir, "log-level.conf"),
			MigrationSet: "ce",
		}
		// The gate must trip before any Consul contact — a dead URL is fine.
		err := RunSetup("http://127.0.0.1:1", paths, "DOCS-TXT")
		if err == nil {
			t.Fatal("want SETUP_CONSUL_TOKEN error for a moves-only migration, got nil")
		}
		if !strings.Contains(err.Error(), "SETUP_CONSUL_TOKEN") {
			t.Errorf("error = %v, want it to mention SETUP_CONSUL_TOKEN", err)
		}
	})
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
