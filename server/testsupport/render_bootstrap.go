//go:build cgo

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zextras/carbonio-preview-ce/render"
)

// InitRealRender builds the pdfium worker from source, initialises the libvips
// and PDFium pools for REAL rendering, and registers teardown via t.Cleanup.
//
// It mirrors render/pdf_e2e_test.go's skip pattern: if cgo / libpdfium / the
// worker binary cannot be built, the test SKIPs cleanly rather than failing,
// so the full-flow suite degrades to SKIP on machines without the native libs.
//
// Call it ONCE per test (the pool is process-global). It is safe across
// sequential tests because Cleanup closes the pool and clears the pointer.
func InitRealRender(t *testing.T) {
	t.Helper()

	// Locate cmd/pdfium-worker relative to THIS file so the helper works no
	// matter which package's test directory is the CWD.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("testsupport: cannot resolve caller path to find pdfium-worker source")
	}
	// thisFile = .../server/testsupport/render_bootstrap.go → repo root is two up.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	workerSrc := filepath.Join(repoRoot, "cmd", "pdfium-worker")
	if _, err := os.Stat(workerSrc); err != nil {
		t.Skipf("pdfium-worker source not found at %s: %v", workerSrc, err)
	}

	tmpDir := t.TempDir()
	workerBin := filepath.Join(tmpDir, "carbonio-preview-pdfium-worker")
	buildCmd := exec.Command("go", "build", "-o", workerBin, workerSrc)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Skipf("go build pdfium-worker failed (CGO/libpdfium unavailable?): %v\n%s", err, out)
	}

	if err := render.InitVips("carbonio-preview-fullflow-test"); err != nil {
		t.Skipf("render.InitVips failed (libvips unavailable?): %v", err)
	}

	const poolSize = 2
	if err := render.PDFInit(poolSize, workerBin); err != nil {
		t.Skipf("render.PDFInit failed: %v", err)
	}
	t.Cleanup(func() {
		render.PDFClose()
	})
}
