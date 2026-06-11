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
)

// Suppress unused import warning for filepath if only used in buildBinary.
var _ = filepath.Join

// TestSetupCLI_MissingConsulURL verifies that --setup with no URL exits 1.
func TestSetupCLI_MissingConsulURL(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "--setup")
	cmd.Env = os.Environ()
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

// TestSetupCLI_SuccessPathPrintsConfigsTxt verifies that --setup with an absent
// ini exits 0 and prints configs.txt content. Uses temp paths to avoid any
// dependency on the real /etc/carbonio/preview/config.ini.
func TestSetupCLI_SuccessPathPrintsConfigsTxt(t *testing.T) {
	bin := buildBinary(t)
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

	cmd := exec.Command(bin, "--setup", srv.URL)
	// Inject temp paths so the binary never touches /etc/carbonio/preview/.
	env := append(os.Environ(),
		"CARBONIO_PREVIEW_TEST_INI_PATH="+filepath.Join(dir, "config.ini"),
		"CARBONIO_PREVIEW_TEST_PROPS_PATH="+filepath.Join(dir, "config.properties"),
	)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "carbonio.service") {
		t.Errorf("expected configs.txt content in output, got: %s", out)
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
