package server

// This file provides injectable function variables that wrap the render
// package calls.  Production code uses the real implementations; tests
// override them with stubs so that the CGO render layer (libvips / PDFium)
// is never exercised during unit tests.
//
// All vars are package-level (not per-request) because the handlers are
// also package-level closures — a per-request override would require a
// context value or a handler struct, both of which are larger changes.
// Resetting to defaults in test teardown is enough for isolated tests.

import (
	"context"
	"net/http"
	"time"

	"github.com/zextras/carbonio-preview-ce/render"
)

// imageThumbnailFunc is the seam for render.ImageThumbnail.
var imageThumbnailFunc = func(
	sem chan struct{},
	data []byte,
	width, height int,
	outputFormat, quality, shape, cropMode string,
) ([]byte, error) {
	return render.ImageThumbnail(sem, data, width, height, outputFormat, quality, shape, cropMode)
}

// pdfSliceFunc is the seam for render.PDFSlice.
var pdfSliceFunc = func(data []byte, firstPage, lastPage int) ([]byte, error) {
	return render.PDFSlice(data, firstPage, lastPage)
}

// pdfSliceRelayFunc is the seam for relay-based PDF slicing in the main process.
// Production code relays to the worker pool; tests stub it without a live worker.
var pdfSliceRelayFunc = func(
	ctx context.Context,
	data []byte,
	firstPage, lastPage int,
	relayClient *http.Client,
	pdfInternalAddr string,
) ([]byte, error) {
	return relayPDFSlice(ctx, data, firstPage, lastPage, relayClient, pdfInternalAddr)
}

// pdfRasterizeFunc is the seam for render.PDFRasterize.
var pdfRasterizeFunc = func(
	sem chan struct{},
	data []byte,
	page, width, height int,
	outputFormat, quality, shape string,
) ([]byte, error) {
	return render.PDFRasterize(sem, data, page, width, height, outputFormat, quality, shape)
}

// collaboraConvertFunc is the seam for render.CollaboraConvert.
var collaboraConvertFunc = func(
	ctx context.Context,
	data []byte,
	langTag string,
	docsEditorURL string,
	timeout time.Duration,
) ([]byte, error) {
	return render.CollaboraConvert(ctx, data, langTag, docsEditorURL, timeout)
}
