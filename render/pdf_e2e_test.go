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
	const poolSize = 2
	if err := PDFInit(poolSize, workerBin); err != nil {
		t.Fatalf("PDFInit: %v", err)
	}
	// Close the pool before clearing the pointer so PDFClose can flush the pool
	// without racing on a nil reference.
	defer func() {
		PDFClose()
		globalPdfPool = nil
	}()

	pdfData := testPDFFixture
	if len(pdfData) == 0 {
		t.Fatal("embedded testPDFFixture is empty")
	}
	t.Logf("PDF fixture: %d bytes", len(pdfData))

	// Exercise PDFSlice (nil semaphore = no gating in test).
	sliced, err := PDFSlice(nil, pdfData, 1, 0)
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

	// Correctness-after-many-calls: run 20 sequential PDFSlice calls and assert
	// each produces valid non-empty output. This verifies that closing documents
	// via defer does not break handle reuse across repeated pool borrows.
	const repeatN = 20
	for i := range repeatN {
		got, err := PDFSlice(nil, pdfData, 1, 0)
		if err != nil {
			t.Fatalf("PDFSlice repeat %d/%d: %v", i+1, repeatN, err)
		}
		if len(got) == 0 {
			t.Fatalf("PDFSlice repeat %d/%d: empty result", i+1, repeatN)
		}
	}
	t.Logf("PDFSlice x%d sequential calls: all ok (handle-close reuse verified)", repeatN)

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

	// go-pdfium's multi_threaded pool respawns lazily: when BorrowObject is
	// called, ValidateObject detects the dead plugin (Exited()==true), destroys
	// the invalid object, and MakeObject starts a fresh subprocess. Respawns
	// are NOT proactive; each BorrowObject triggers at most one create cycle.
	// We force concurrent borrows equal to poolSize so ALL slots are exercised.
	type sliceResult struct {
		data []byte
		err  error
	}
	resultsCh := make(chan sliceResult, poolSize)
	for i := 0; i < poolSize; i++ {
		go func() {
			data, err := PDFSlice(nil, pdfData, 1, 0)
			resultsCh <- sliceResult{data, err}
		}()
	}
	for i := 0; i < poolSize; i++ {
		res := <-resultsCh
		if res.err != nil {
			t.Fatalf("PDFSlice (post-kill concurrent borrow %d): %v", i, res.err)
		}
		if len(res.data) == 0 {
			t.Fatalf("PDFSlice (post-kill concurrent borrow %d): empty result", i)
		}
	}
	t.Logf("PDFSlice after worker kill: all %d concurrent borrows succeeded", poolSize)

	// Poll until the live worker count is back to poolSize. Each concurrent
	// BorrowObject above spawns a fresh subprocess for any invalid slot, so
	// after both complete the pool holds poolSize healthy workers.
	const respawnTimeout = 10 * time.Second
	const pollInterval = 200 * time.Millisecond
	deadline := time.Now().Add(respawnTimeout)
	for {
		current := findChildPIDs(t, myPID)
		if len(current) >= poolSize {
			t.Logf("Respawn confirmed: %d/%d workers alive after kill (PIDs: %v)",
				len(current), poolSize, current)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Pool did not respawn within %v: only %d/%d workers alive",
				respawnTimeout, len(current), poolSize)
		}
		time.Sleep(pollInterval)
	}
	t.Logf("Pool fully restored to %d workers — respawn verified", poolSize)
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
