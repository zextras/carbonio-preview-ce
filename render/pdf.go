// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/multi_threaded"
	"github.com/klippa-app/go-pdfium/requests"
)

// ErrRenderUnavailable is returned when the PDFium subprocess pool cannot
// provide a worker — e.g. a worker failed to start or the pool is closed.
//
// NOTE: this is a deliberate divergence from the old Python service, which had
// no pool-timeout concept. Callers should map this to HTTP 503 (Service
// Unavailable) to distinguish transient capacity problems from permanent
// document-level errors (HTTP 400).
var ErrRenderUnavailable = errors.New("PDF rendering temporarily unavailable")

// globalPdfPool is the PDFium instance pool. Initialised by PDFInit.
// Must not be used before initialisation.
var globalPdfPool pdfium.Pool

// pdfInstanceWait bounds one pool borrow attempt; getPdfInstance retries until ctx is done.
var pdfInstanceWait = 5 * time.Second

func getPdfInstance(ctx context.Context) (pdfium.Pdfium, error) {
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, pdfInstanceWait)
		inst, err := globalPdfPool.GetInstanceWithContext(attemptCtx)
		timedOut := errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
		cancel()
		switch {
		case err == nil:
			return inst, nil
		case ctx.Err() != nil:
			return nil, fmt.Errorf("pdfium worker wait: %w", ctx.Err())
		case !timedOut:
			return nil, fmt.Errorf("%w: %w", ErrRenderUnavailable, err)
		}
	}
}

// PDFInit initialises the multi_threaded PDFium subprocess pool.
// poolSize controls MinIdle/MaxIdle/MaxTotal (all set to the same value).
// workerBin is the absolute path to the carbonio-preview-pdfium-worker binary.
//
// Call PDFClose on graceful shutdown.
func PDFInit(poolSize int, workerBin string) error {
	pool := multi_threaded.Init(multi_threaded.Config{
		MinIdle:  poolSize,
		MaxIdle:  poolSize,
		MaxTotal: poolSize,
		Command: multi_threaded.Command{
			BinPath:      workerBin,
			StartTimeout: 30 * time.Second,
		},
	})
	globalPdfPool = pool
	return nil
}

// PDFClose shuts down the PDFium pool. Call on graceful shutdown.
func PDFClose() {
	if globalPdfPool != nil {
		globalPdfPool.Close() //nolint:errcheck
	}
}

func pdfPage1DPI(w, h float64) (int, error) {
	long := math.Max(w, h)
	if w <= 0 || h <= 0 {
		return 0, fmt.Errorf("invalid pdf page size %vx%v", w, h)
	}
	return int(math.Ceil(float64(SideM) * 72 / long)), nil
}

// PDFPage1Pair renders page 1 at the DPI that gives a SideM long side and
// returns the M and S renditions (always JPEG: the page is flattened on white).
func PDFPage1Pair(ctx context.Context, data []byte) (Pair, error) {
	if globalPdfPool == nil {
		return Pair{}, fmt.Errorf("PDFium pool not initialised: call PDFInit first")
	}

	inst, err := getPdfInstance(ctx)
	if err != nil {
		return Pair{}, err
	}
	defer inst.Close()

	docResp, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return Pair{}, fmt.Errorf("pdfium OpenDocument: %w", err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: docResp.Document}) //nolint:errcheck

	size, err := inst.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{Document: docResp.Document, Index: 0})
	if err != nil {
		return Pair{}, fmt.Errorf("pdfium FPDF_GetPageSizeByIndex: %w", err)
	}
	dpi, err := pdfPage1DPI(size.Width, size.Height)
	if err != nil {
		return Pair{}, err
	}

	renderResp, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: docResp.Document, Index: 0}},
		DPI:  dpi,
	})
	if err != nil {
		return Pair{}, fmt.Errorf("pdfium RenderPageInDPI page 0: %w", err)
	}
	defer renderResp.Cleanup()

	if renderResp.Result.Image == nil {
		return Pair{}, fmt.Errorf("pdfium returned nil image for page 0")
	}
	return rgbaPair(renderResp.Result.Image)
}
