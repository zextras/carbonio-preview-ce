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

// ── V1 log.level → systemd drop-in migration tests ────────────────────────────

// TestV1LogLevel_WriteDropIn verifies that a V1 migration with [log] level=debug
// in the ini writes a correctly-formatted drop-in file.
func TestV1LogLevel_WriteDropIn(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		dropInPath := filepath.Join(dir, "log-level.conf")

		iniContent := "[log]\nlevel = debug\n"
		iniPath := writeFile(t, dir, "config.ini", iniContent)
		propsPath := filepath.Join(dir, "config.properties")

		// Consul stub (not used by this migration but required by NewRunner).
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		}))
		defer srv.Close()

		if err := Register(V1MigrateFromPythonIni(dropInPath)); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:    iniPath,
			PropsPath:  propsPath,
			ConsulURL:  srv.URL,
			DropInPath: dropInPath,
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
	})
}

// TestV1LogLevel_IdempotentSecondRun verifies that a second run (ini absent)
// does NOT re-write the drop-in file.
func TestV1LogLevel_IdempotentSecondRun(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		dropInPath := filepath.Join(dir, "log-level.conf")
		iniPath := filepath.Join(dir, "config.ini") // does not exist → absent

		propsPath := filepath.Join(dir, "config.properties")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "true")
		}))
		defer srv.Close()

		if err := Register(V1MigrateFromPythonIni(dropInPath)); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:    iniPath,
			PropsPath:  propsPath,
			ConsulURL:  srv.URL,
			DropInPath: dropInPath,
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		// Drop-in must NOT have been created (ini absent → entry skipped).
		if _, err := os.Stat(dropInPath); !os.IsNotExist(err) {
			t.Error("drop-in must not be created when ini is absent (idempotent second run)")
		}
	})
}

// TestV1LogLevel_AbsentIniNoDropIn verifies that when the ini has no [log] level
// key, the drop-in file is not created.
func TestV1LogLevel_AbsentIniNoDropIn(t *testing.T) {
	withCleanRegistry(t, func() {
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

		if err := Register(V1MigrateFromPythonIni(dropInPath)); err != nil {
			t.Fatalf("register: %v", err)
		}

		runner, err := NewRunner(Paths{
			IniPath:    iniPath,
			PropsPath:  propsPath,
			ConsulURL:  srv.URL,
			DropInPath: dropInPath,
		})
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		runner.Run()

		// Drop-in must NOT have been created.
		if _, err := os.Stat(dropInPath); !os.IsNotExist(err) {
			t.Error("drop-in must not be created when ini has no log.level key")
		}
	})
}
