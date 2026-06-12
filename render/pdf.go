// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/multi_threaded"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/single_threaded"
)

// ErrRenderUnavailable is returned when the PDFium subprocess pool cannot
// provide a worker — e.g. all workers are busy (pool exhaustion), the startup
// timeout has elapsed, or a worker failed to start.
//
// NOTE: this is a deliberate divergence from the old Python service, which had
// no pool-timeout concept. Callers should map this to HTTP 503 (Service
// Unavailable) to distinguish transient capacity problems from permanent
// document-level errors (HTTP 400).
var ErrRenderUnavailable = errors.New("PDF rendering temporarily unavailable")

// globalPdfPool is the PDFium instance pool. Initialised by PDFInit or
// PDFInitSingleThreadedForTests. Must not be used before initialisation.
var globalPdfPool pdfium.Pool

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

// PDFInitSingleThreadedForTests initialises the in-process single_threaded PDFium
// backend. For use in unit tests only — do NOT call in production code.
// Uses the CGO backend (links against libpdfium.so).
func PDFInitSingleThreadedForTests() {
	globalPdfPool = single_threaded.Init(single_threaded.Config{})
}

// PDFRasterize renders page `page` (0-indexed) of a PDF document to an encoded
// image and returns the encoded bytes.
//
// The pipeline:
//  1. PDFium renders the page at 150 DPI → *image.RGBA (no disk I/O).
//  2. The raw RGBA pixels are fed directly into libvips via
//     vips_image_new_from_memory — no PNG encode/decode round-trip.
//  3. libvips applies COVER resize + center-crop and encodes the result.
//
// Concurrency model:
//   - semaphore (N_http / workers) gates handler-level processing; pass nil
//     to skip gating (not recommended in production).
//   - The PDFium subprocess pool (N_pdf / pdf-workers) is the second gate:
//     GetInstance blocks until a subprocess worker is free or the timeout
//     fires. The http gate sits in front so the pool is never flooded.
//
// outputFormat: "jpeg" or "png".
// quality: "lowest", "low", "medium", "high", "highest".
// shape: "rounded" or "rectangular" (rounded forces PNG output).
// Returns ErrRenderUnavailable when the pool cannot supply a worker (timeout,
// exhaustion, or worker start failure) — callers should map this to HTTP 503.
func PDFRasterize(
	semaphore chan struct{},
	data []byte,
	page, width, height int,
	outputFormat, quality, shape string,
) ([]byte, error) {
	// N_http gate: bound handler concurrency before touching the subprocess pool.
	if semaphore != nil {
		semaphore <- struct{}{}
		defer func() { <-semaphore }()
	}

	if globalPdfPool == nil {
		return nil, fmt.Errorf("PDFium pool not initialised: call PDFInit first")
	}

	inst, err := globalPdfPool.GetInstance(30 * time.Second)
	if err != nil {
		// Pool exhaustion / timeout / worker start failure → transient, not a
		// document error; signal caller to return HTTP 503.
		return nil, fmt.Errorf("%w: %w", ErrRenderUnavailable, err)
	}
	defer inst.Close()

	docResp, err := inst.OpenDocument(&requests.OpenDocument{
		File: &data,
	})
	if err != nil {
		return nil, fmt.Errorf("pdfium OpenDocument: %w", err)
	}

	renderResp, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: docResp.Document,
				Index:    page,
			},
		},
		DPI: 150, // ~1240×1754 px for an A4 page
	})
	if err != nil {
		return nil, fmt.Errorf("pdfium RenderPageInDPI page %d: %w", page, err)
	}
	defer renderResp.Cleanup()

	rgba := renderResp.Result.Image
	if rgba == nil {
		return nil, fmt.Errorf("pdfium returned nil image for page %d", page)
	}

	// Apply the same size-normalisation as the image path: 0 means "keep the
	// rasterised page size"; sub-minimum values are clamped to ImageMinRes.
	origW := rgba.Bounds().Dx()
	origH := rgba.Bounds().Dy()
	width, height = ConvertRequestedSize(width, height, origW, origH, ImageMinRes)

	jpegQuality := QualityToInt(quality)
	fmtStr := outputFormat
	if shape == "rounded" {
		fmtStr = "png"
	}

	// Feed raw RGBA directly to libvips — eliminates the PNG round-trip.
	out, err := coverCropVipsRGBA(rgba, width, height, fmtStr, jpegQuality)
	if err != nil {
		return nil, err
	}

	if shape == "rounded" {
		out, err = applyRoundedMaskVips(out, width, height)
		if err != nil {
			return out, nil // non-fatal: return untransformed
		}
	}

	return out, nil
}

// PDFSlice extracts pages [firstPage, lastPage] from a PDF and returns the
// sliced PDF bytes. firstPage and lastPage are 1-indexed (matching the Python
// service spec). lastPage == 0 means "last page of the document".
//
// Concurrency model:
//   - semaphore (N_http / workers) gates handler-level processing; pass nil
//     to skip gating (not recommended in production).
//   - The PDFium subprocess pool (N_pdf / pdf-workers) is the second gate.
//     The http gate sits in front so the pool is never flooded.
//
// Invalid PDFs: returns (nil, error). Callers should map this to HTTP 400.
// Pool unavailability: returns (nil, ErrRenderUnavailable). Callers should
// map this to HTTP 503.
func PDFSlice(semaphore chan struct{}, data []byte, firstPage, lastPage int) ([]byte, error) {
	// N_http gate: bound handler concurrency before touching the subprocess pool.
	if semaphore != nil {
		semaphore <- struct{}{}
		defer func() { <-semaphore }()
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty PDF data")
	}

	if globalPdfPool == nil {
		return nil, fmt.Errorf("PDFium pool not initialised: call PDFInit first")
	}

	inst, err := globalPdfPool.GetInstance(30 * time.Second)
	if err != nil {
		// Pool exhaustion / timeout / worker start failure → transient, not a
		// document error; signal caller to return HTTP 503.
		return nil, fmt.Errorf("%w: %w", ErrRenderUnavailable, err)
	}
	defer inst.Close()

	docResp, err := inst.OpenDocument(&requests.OpenDocument{
		File: &data,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid PDF: %w", err)
	}

	pageCountResp, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: docResp.Document,
	})
	if err != nil {
		return nil, fmt.Errorf("pdfium FPDF_GetPageCount: %w", err)
	}
	totalPages := pageCountResp.PageCount

	// Convert 1-indexed firstPage/lastPage to 0-indexed start/end.
	start := firstPage - 1
	if start < 0 {
		start = 0
	}
	end := lastPage
	if end == 0 || end > totalPages {
		end = totalPages
	}
	if start >= end {
		return nil, fmt.Errorf("invalid page range: first=%d last=%d (total=%d)", firstPage, lastPage, totalPages)
	}

	// If the whole document is requested, return the original bytes.
	if start == 0 && end == totalPages {
		return data, nil
	}

	// Create a new empty document and import the page range via
	// FPDF_ImportPagesByIndex (0-indexed page list, no string parsing needed).
	newDocResp, err := inst.FPDF_CreateNewDocument(&requests.FPDF_CreateNewDocument{})
	if err != nil {
		return nil, fmt.Errorf("pdfium FPDF_CreateNewDocument: %w", err)
	}

	// Build 0-indexed page list.
	pageIndices := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		pageIndices = append(pageIndices, i)
	}

	if _, err := inst.FPDF_ImportPagesByIndex(&requests.FPDF_ImportPagesByIndex{
		Source:      docResp.Document,
		Destination: newDocResp.Document,
		PageIndices: pageIndices,
		Index:       0,
	}); err != nil {
		return nil, fmt.Errorf("pdfium FPDF_ImportPagesByIndex: %w", err)
	}

	saveResp, err := inst.FPDF_SaveAsCopy(&requests.FPDF_SaveAsCopy{
		Document: newDocResp.Document,
	})
	if err != nil {
		return nil, fmt.Errorf("pdfium FPDF_SaveAsCopy: %w", err)
	}

	if saveResp.FileBytes == nil {
		return nil, fmt.Errorf("pdfium FPDF_SaveAsCopy: nil output")
	}

	// Return a copy so the pdfium-managed buffer can be freed.
	out := bytes.Clone(*saveResp.FileBytes)
	return out, nil
}
