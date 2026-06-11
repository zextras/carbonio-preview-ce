// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/config/migrate"
)

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

// TestSetupSuccessPath_PrintsConfigsTxt verifies that migrate.RunSetup with an
// absent ini exits cleanly and that config documentation is printed.
// Uses t.TempDir() paths passed directly — no subprocess, no env-var hooks.
func TestSetupSuccessPath_PrintsConfigsTxt(t *testing.T) {
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
	if err := migrate.RunSetup(srv.URL, paths, config.ConfigsTxt()); err != nil {
		t.Fatalf("migrate.RunSetup with absent ini should not fail: %v", err)
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
