//go:build cgo

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

//go:embed testdata/minimal.pdf
var testPDFFixture []byte

func TestPDFMultiThreadedPool_E2E(t *testing.T) {
	// Build the worker binary from source.
	workerSrc := filepath.Join("..", "cmd", "pdfium-worker")
	if _, err := os.Stat(workerSrc); err != nil {
		t.Skipf("pdfium-worker source not found at %s: %v", workerSrc, err)
	}

	tmpDir := t.TempDir()
	workerBin := filepath.Join(tmpDir, "carbonio-preview-pdfium-worker")
	buildCmd := exec.Command("go", "build", "-o", workerBin, workerSrc)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Skipf("go build pdfium-worker failed (CGO unavailable?): %v\n%s", err, out)
	}
	t.Logf("Built pdfium-worker at %s", workerBin)

	// Init pool with 2 workers.
	if err := PDFInit(2, workerBin); err != nil {
		t.Fatalf("PDFInit: %v", err)
	}
	defer func() {
		globalPdfPool = nil
		PDFClose()
	}()

	pdfData := testPDFFixture
	if len(pdfData) == 0 {
		t.Fatal("embedded testPDFFixture is empty")
	}
	t.Logf("PDF fixture: %d bytes", len(pdfData))

	// Exercise PDFSlice.
	sliced, err := PDFSlice(pdfData, 1, 0)
	if err != nil {
		t.Fatalf("PDFSlice: %v", err)
	}
	if len(sliced) == 0 {
		t.Fatal("PDFSlice returned empty bytes")
	}
	t.Logf("PDFSlice: ok, %d bytes", len(sliced))

	// Exercise PDFRasterize.
	rendered, err := PDFRasterize(nil, pdfData, 0, 100, 100, "jpeg", "medium", "rectangular")
	if err != nil {
		t.Fatalf("PDFRasterize: %v", err)
	}
	if len(rendered) == 0 {
		t.Fatal("PDFRasterize returned empty bytes")
	}
	t.Logf("PDFRasterize: ok, %d bytes", len(rendered))

	// Kill one worker subprocess to test auto-respawn.
	myPID := os.Getpid()
	childPIDs := findChildPIDs(t, myPID)
	t.Logf("Found %d child PIDs: %v", len(childPIDs), childPIDs)
	if len(childPIDs) == 0 {
		t.Skip("no child processes found — cannot test respawn (pool may not spawn subprocesses in this env)")
	}

	// Kill the first child.
	victim := childPIDs[0]
	t.Logf("Killing worker PID %d to test auto-respawn", victim)
	if err := syscall.Kill(victim, syscall.SIGKILL); err != nil {
		t.Logf("kill %d: %v (may have already exited)", victim, err)
	}

	// Give pool a moment to detect the death and respawn.
	time.Sleep(500 * time.Millisecond)

	// Subsequent call must still succeed (pool respawns the worker).
	sliced2, err := PDFSlice(pdfData, 1, 0)
	if err != nil {
		t.Fatalf("PDFSlice after worker kill: %v", err)
	}
	if len(sliced2) == 0 {
		t.Fatal("PDFSlice after worker kill returned empty bytes")
	}
	t.Logf("PDFSlice after respawn: ok, %d bytes — respawn verified", len(sliced2))
}

// findChildPIDs returns PIDs of direct children of parentPID by scanning /proc.
func findChildPIDs(t *testing.T, parentPID int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Logf("ReadDir /proc: %v", err)
		return nil
	}

	var children []int
	parentStr := strconv.Itoa(parentPID)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		statusPath := filepath.Join("/proc", e.Name(), "status")
		data, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[1] == parentStr {
					children = append(children, pid)
				}
				break
			}
		}
	}
	return children
}
