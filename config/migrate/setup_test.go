// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunSetup_NewRunnerError covers the first arm of RunSetup: NewRunner fails
// (here because the ini file exists but is unparseable), RunSetup prints docs
// and returns a wrapped "setup failed" error.
//
// This is a pure framework test (no concrete migration set needed — NewRunner
// fails before any set is consulted), so it stays here rather than moving to
// config/migrate/cemig alongside the CE-specific RunSetup tests.
func TestRunSetup_NewRunnerError(t *testing.T) {
	withCleanRegistry(t, func() {
		dir := t.TempDir()
		// An ini that exists but cannot be parsed by gopkg.in/ini.v1: a line with
		// an unterminated section header makes ini.Load fail, so newIniStore
		// returns an error and NewRunner fails.
		iniPath := writeFile(t, dir, "config.ini", "[unterminated\nkey = value\n")
		paths := Paths{
			IniPath:    iniPath,
			PropsPath:  filepath.Join(dir, "config.properties"),
			DropInPath: filepath.Join(dir, "log-level.conf"),
		}
		err := RunSetup("http://127.0.0.1:8500", paths, "DOCS-TXT")
		if err == nil {
			t.Fatal("want NewRunner error, got nil")
		}
		if !strings.Contains(err.Error(), "setup failed") {
			t.Errorf("error = %v, want it to wrap %q", err, "setup failed")
		}
	})
}

// TestNewRunner_PropertiesError covers the NewRunner arm where newPropertiesStore
// fails. We make config.properties an unreadable file (chmod 0000) so the
// load() Open succeeds-as-error path returns a non-ErrNotExist error.
func TestNewRunner_PropertiesError(t *testing.T) {
	dir := t.TempDir()
	propsPath := writeFile(t, dir, "config.properties", "k=v\n")
	if err := os.Chmod(propsPath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(propsPath, 0o644) })

	_, err := NewRunner(Paths{
		IniPath:   filepath.Join(dir, "no.ini"), // absent → iniStore ok
		PropsPath: propsPath,
	})
	if err == nil {
		t.Fatal("want newPropertiesStore error from unreadable properties file, got nil")
	}
}
