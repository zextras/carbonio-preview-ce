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

// TestRunSetup_NewRunnerError covers the first arm of RunSetup: NewRunner fails
// (here because the ini file exists but is unparseable), RunSetup prints docs
// and returns a wrapped "setup failed" error.
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

// TestRunSetup_TokenRequiredWhenAppWork covers the token-gate arm: the ini holds
// an application-layer key (enable_document_preview), HasApplicationWork() is
// true, and SETUP_CONSUL_TOKEN is empty → RunSetup returns the token error
// WITHOUT touching Consul.
func TestRunSetup_TokenRequiredWhenAppWork(t *testing.T) {
	withCleanRegistry(t, func() {
		t.Setenv("SETUP_CONSUL_TOKEN", "")
		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register V1: %v", err)
		}
		dir := t.TempDir()
		iniPath := writeFile(t, dir, "config.ini",
			"[carbonio.preview]\nenable_document_preview = true\n")

		consulHits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			consulHits++
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		paths := Paths{
			IniPath:    iniPath,
			PropsPath:  filepath.Join(dir, "config.properties"),
			DropInPath: filepath.Join(dir, "log-level.conf"),
		}
		err := RunSetup(srv.URL, paths, "DOCS-TXT")
		if err == nil {
			t.Fatal("want SETUP_CONSUL_TOKEN error, got nil")
		}
		if !strings.Contains(err.Error(), "SETUP_CONSUL_TOKEN") {
			t.Errorf("error = %v, want it to mention SETUP_CONSUL_TOKEN", err)
		}
		if consulHits != 0 {
			t.Errorf("token gate must run before any Consul request; consulHits=%d, want 0", consulHits)
		}
	})
}

// TestRunSetup_SuccessWithToken covers the success arm: an ini with an
// application key plus a token present → Run() executes and PUTs to the Consul
// stub, RunSetup returns nil.
func TestRunSetup_SuccessWithToken(t *testing.T) {
	withCleanRegistry(t, func() {
		t.Setenv("SETUP_CONSUL_TOKEN", "tok123")
		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register V1: %v", err)
		}
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

		paths := Paths{
			IniPath:    iniPath,
			PropsPath:  filepath.Join(dir, "config.properties"),
			DropInPath: filepath.Join(dir, "log-level.conf"),
		}
		if err := RunSetup(srv.URL, paths, "DOCS-TXT"); err != nil {
			t.Fatalf("RunSetup returned error: %v", err)
		}
		if puts == 0 {
			t.Error("expected at least one Consul PUT during a successful run")
		}
		if gotToken != "tok123" {
			t.Errorf("X-Consul-Token = %q, want %q", gotToken, "tok123")
		}
	})
}

// TestRunSetup_SuccessAbsentIni covers the success arm with an absent ini:
// HasApplicationWork() is false (ini missing), so no token is needed and Run()
// is a no-op that completes cleanly.
func TestRunSetup_SuccessAbsentIni(t *testing.T) {
	withCleanRegistry(t, func() {
		t.Setenv("SETUP_CONSUL_TOKEN", "")
		if err := Register(V1MigrateFromPythonIni()); err != nil {
			t.Fatalf("register V1: %v", err)
		}
		dir := t.TempDir()
		paths := Paths{
			IniPath:    filepath.Join(dir, "does-not-exist.ini"),
			PropsPath:  filepath.Join(dir, "config.properties"),
			DropInPath: filepath.Join(dir, "log-level.conf"),
		}
		if err := RunSetup("http://127.0.0.1:8500", paths, "DOCS-TXT"); err != nil {
			t.Fatalf("RunSetup with absent ini should not fail: %v", err)
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
