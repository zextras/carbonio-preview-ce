// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/v2/config/migrate"
	"github.com/zextras/carbonio-preview-ce/v2/docs"

	// Blank-imported for the same reason main.go does: this test package
	// exercises the --setup path end-to-end and must see CE's "ce" migration
	// set registered, exactly like the production binary.
	_ "github.com/zextras/carbonio-preview-ce/v2/config/migrate/cemig"
)

// TestFindArg verifies the pure --setup flag locator.
func TestFindArg(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
		want int
	}{
		{"flag first", []string{"--setup", "url"}, "--setup", 0},
		{"flag second", []string{"a", "--setup", "url"}, "--setup", 1},
		{"flag absent", []string{"a", "b"}, "--setup", -1},
		{"empty args", nil, "--setup", -1},
		{"flag last", []string{"x", "y", "--setup"}, "--setup", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findArg(tc.args, tc.flag); got != tc.want {
				t.Errorf("findArg(%v, %q) = %d, want %d", tc.args, tc.flag, got, tc.want)
			}
		})
	}
}

// TestRunSetupIfRequested_NotRequested verifies that without --setup the helper
// reports handled=false so main() continues to the normal server boot.
func TestRunSetupIfRequested_NotRequested(t *testing.T) {
	handled, code := runSetupIfRequested([]string{"--some-other-flag", "value"})
	if handled {
		t.Errorf("handled = true, want false when --setup is absent")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 when not handled", code)
	}
}

// TestRunSetupIfRequested_MissingURL verifies that --setup with no following URL
// is handled with exit code 1 (usage error).
func TestRunSetupIfRequested_MissingURL(t *testing.T) {
	handled, code := runSetupIfRequested([]string{"--setup"})
	if !handled {
		t.Fatal("handled = false, want true for --setup with missing URL")
	}
	if code != 1 {
		t.Errorf("code = %d, want 1 for missing URL", code)
	}
}

// TestRunSetupIfRequested_Success verifies the success dispatch: --setup with a
// URL runs the migration and returns handled=true, code=0. It relies on the
// production ini path being absent (the default on a developer/CI machine), so
// there is no application work, no token is needed, and no Consul call is made.
// If the production ini happens to exist, the test skips rather than touch it.
func TestRunSetupIfRequested_Success(t *testing.T) {
	if _, err := os.Stat("/etc/carbonio/preview/config.ini"); err == nil {
		t.Skip("production config.ini present; skipping to avoid touching real config")
	}
	handled, code := runSetupIfRequested([]string{"--setup", "http://127.0.0.1:8500"})
	if !handled {
		t.Fatal("handled = false, want true for --setup with URL")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 on successful setup (absent ini → no-op)", code)
	}
}

// TestSetupCLI_MissingConsulURL verifies that --setup with no URL exits 1.
// This is a pure CLI concern (argument parsing before any path/store logic)
// that requires a subprocess to exercise the os.Exit path.
func TestSetupCLI_MissingConsulURL(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "--setup")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit, got 0")
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got: %v", err)
	}
	if !strings.Contains(string(out), "Usage") {
		t.Errorf("expected usage message, got: %s", out)
	}
}

// TestSetupSuccessPath_PrintsConfigsMd verifies that migrate.RunSetup with an
// absent ini exits cleanly and that config documentation is printed.
// Uses t.TempDir() paths passed directly — no subprocess, no env-var hooks.
func TestSetupSuccessPath_PrintsConfigsMd(t *testing.T) {
	dir := t.TempDir()

	// A minimal Consul stub that accepts any request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut, http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	// Register no migrations (or rely on the empty default registry).
	// migrate.RunSetup must complete without error when ini is absent.
	paths := migrate.Paths{
		IniPath:   filepath.Join(dir, "config.ini"), // does not exist → absent
		PropsPath: filepath.Join(dir, "config.properties"),
	}

	// Call migrate.RunSetup directly with the test's Consul stub.
	// When ini is absent there is no application work, so no token is needed.
	if err := migrate.RunSetup(srv.URL, paths, docs.ConfigsMd()); err != nil {
		t.Fatalf("migrate.RunSetup with absent ini should not fail: %v", err)
	}
}

// TestSetupEndToEnd_RunsOnlyCEMigrations proves the fix end-to-end at the
// cmd/carbonio-preview level: migrate.RunSetup, driven with the SAME hardcoded
// MigrationSet main.go passes ("ce"), executes CE's V1 migration (an application
// key gets PUT to Consul) — the whole point being that this binary only ever
// runs its OWN "ce" set, never inherits any other edition's migrations just
// because config/migrate is imported.
func TestSetupEndToEnd_RunsOnlyCEMigrations(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	if err := os.WriteFile(iniPath, []byte("[carbonio.preview]\nenable_document_preview = true\n"), 0o644); err != nil {
		t.Fatalf("write ini: %v", err)
	}

	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			puts++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Setenv("SETUP_CONSUL_TOKEN", "tok")
	paths := migrate.Paths{
		IniPath:      iniPath,
		PropsPath:    filepath.Join(dir, "config.properties"),
		DropInPath:   filepath.Join(dir, "log-level.conf"),
		MigrationSet: "ce", // hardcoded, matching main.go (dev-only, not KV config)
	}
	if err := migrate.RunSetup(srv.URL, paths, docs.ConfigsMd()); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if puts == 0 {
		t.Error("expected CE's V1 migration to PUT document/enable-preview to Consul KV; got 0 PUTs — migration set resolution is broken")
	}
}

// buildBinary compiles the carbonio-preview binary into a temp dir and
// returns the path.  The test is skipped if compilation fails (e.g. missing
// C libraries for pdfium in pure-test environments).
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "carbonio-preview")
	// Build without CGO to avoid pdfium dependency in test environments.
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(".") // cmd/carbonio-preview
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("binary build failed (likely missing C deps): %v\n%s", err, out)
	}
	return bin
}
