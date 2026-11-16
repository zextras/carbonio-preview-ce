//go:build cgo

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

	pair, err := PDFPage1Pair(context.Background(), pdfData)
	if err != nil {
		t.Fatalf("PDFPage1Pair: %v", err)
	}
	if len(pair.M.Body) == 0 || len(pair.S.Body) == 0 {
		t.Fatal("PDFPage1Pair returned an empty rendition")
	}
	t.Logf("PDFPage1Pair: ok, M %d bytes, S %d bytes", len(pair.M.Body), len(pair.S.Body))

	// Correctness-after-many-calls: run 20 sequential calls and assert each
	// produces valid non-empty output. This verifies that closing documents
	// via defer does not break handle reuse across repeated pool borrows.
	const repeatN = 20
	for i := range repeatN {
		got, err := PDFPage1Pair(context.Background(), pdfData)
		if err != nil {
			t.Fatalf("PDFPage1Pair repeat %d/%d: %v", i+1, repeatN, err)
		}
		if len(got.M.Body) == 0 {
			t.Fatalf("PDFPage1Pair repeat %d/%d: empty result", i+1, repeatN)
		}
	}
	t.Logf("PDFPage1Pair x%d sequential calls: all ok (handle-close reuse verified)", repeatN)

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

	// go-pdfium's multi_threaded pool respawns LAZILY: it does not run an idle
	// evictor, so MinIdle is not maintained proactively. A dead worker is
	// replaced only when a BorrowObject needs it — ValidateObject detects the
	// dead plugin (Exited()==true), destroys it, and MakeObject starts a fresh
	// subprocess. Capacity is therefore never permanently lost (the bug the
	// hand-rolled relay had), but it self-heals on demand rather than eagerly.
	//
	// To prove BOTH guarantees we drive SUSTAINED concurrent load: more
	// goroutines than poolSize hammering PDFPage1Pair keeps >= poolSize borrows
	// in flight at once, which forces the pool to instantiate up to MaxTotal
	// live workers again. We assert (a) every call succeeds (resilience) and
	// (b) the live worker count climbs back to poolSize (on-demand respawn).
	concurrency := poolSize * 3
	if concurrency < 6 {
		concurrency = 6
	}
	stop := make(chan struct{})
	errCh := make(chan error, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := PDFPage1Pair(context.Background(), pdfData)
				if err != nil {
					errCh <- err
					return
				}
				if len(got.M.Body) == 0 {
					errCh <- fmt.Errorf("empty PDFPage1Pair result")
					return
				}
			}
		}()
	}

	// Poll for full recovery while the load runs.
	const respawnTimeout = 25 * time.Second
	const pollInterval = 200 * time.Millisecond
	deadline := time.Now().Add(respawnTimeout)
	recovered := false
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			close(stop)
			wg.Wait()
			t.Fatalf("PDFPage1Pair failed under post-kill load: %v", err)
		default:
		}
		if len(findChildPIDs(t, myPID)) >= poolSize {
			recovered = true
			break
		}
		time.Sleep(pollInterval)
	}
	close(stop)
	wg.Wait()
	// drain any late error
	select {
	case err := <-errCh:
		t.Fatalf("PDFPage1Pair failed under post-kill load: %v", err)
	default:
	}
	current := findChildPIDs(t, myPID)
	if !recovered {
		t.Fatalf("pool did not recover to %d workers under sustained load within %v: only %d alive",
			poolSize, respawnTimeout, len(current))
	}
	t.Logf("Resilience verified: all calls succeeded after kill; pool recovered to %d workers (PIDs: %v)",
		len(current), current)
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

func TestPDFPage1Pair_E2E(t *testing.T) {
	workerSrc := filepath.Join("..", "cmd", "pdfium-worker")
	if _, err := os.Stat(workerSrc); err != nil {
		t.Skipf("pdfium-worker source not found at %s: %v", workerSrc, err)
	}
	workerBin := filepath.Join(t.TempDir(), "carbonio-preview-pdfium-worker")
	buildCmd := exec.Command("go", "build", "-o", workerBin, workerSrc)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Skipf("go build pdfium-worker failed (CGO unavailable?): %v\n%s", err, out)
	}
	if err := PDFInit(1, workerBin); err != nil {
		t.Fatalf("PDFInit: %v", err)
	}
	defer func() {
		PDFClose()
		globalPdfPool = nil
	}()

	p, err := PDFPage1Pair(context.Background(), testPDFFixture)
	if err != nil {
		t.Fatalf("PDFPage1Pair: %v", err)
	}
	long := func(r Rendition) int { return max(r.Width, r.Height) }
	if long(p.M) != SideM || long(p.S) != SideS {
		t.Fatalf("M=%dx%d S=%dx%d, want long sides %d and %d", p.M.Width, p.M.Height, p.S.Width, p.S.Height, SideM, SideS)
	}
	for _, r := range []Rendition{p.M, p.S} {
		if r.ContentType != "image/jpeg" || !isJPEGBytes(r.Body) {
			t.Fatalf("rendition is %s, want JPEG", r.ContentType)
		}
	}

	if _, err := PDFPage1Pair(context.Background(), []byte("not a pdf")); err == nil {
		t.Fatal("want error for invalid PDF")
	}
}
