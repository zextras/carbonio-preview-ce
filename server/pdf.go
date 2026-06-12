// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// registerPDFRoutes registers all /preview/pdf/* endpoints onto mux.
func registerPDFRoutes(
	mux *http.ServeMux,
	cfg *config.Config,
	store storage.Client,
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string, // "http://127.0.0.1:<pdfInternalPort>" — empty in worker
) {
	base := "/" + cfg.ServiceName + "/" + cfg.ServicePDFName

	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		routePDF(w, r, base, cfg, store, sem, relayClient, pdfInternalAddr)
	})
}

// routePDF dispatches PDF requests by path inspection.
//
// Patterns (relative to base):
//
//	GET  /{id}/{version}/                    → PDF preview (sliced)
//	GET  /{id}/{version}/{area}/thumbnail/   → PDF thumbnail (rasterize → image)
//	POST /                                   → PDF preview upload
//	POST /{area}/thumbnail/                  → PDF thumbnail upload
func routePDF(
	w http.ResponseWriter,
	r *http.Request,
	base string,
	cfg *config.Config,
	store storage.Client,
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	tail := strings.TrimPrefix(r.URL.Path, base)
	tail = strings.TrimPrefix(tail, "/")
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")
	// Empty path ("") means exactly base+"/"
	if len(parts) == 1 && parts[0] == "" {
		parts = []string{}
	}

	switch r.Method {
	case http.MethodGet:
		switch len(parts) {
		case 2:
			// GET /{id}/{version}/
			pdfGetPreview(w, r, parts[0], parts[1], cfg, store, relayClient, pdfInternalAddr)
		case 4:
			// GET /{id}/{version}/{area}/thumbnail/
			if parts[3] == "thumbnail" {
				pdfGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, sem, relayClient, pdfInternalAddr)
				return
			}
			errNotFound(w, "Not Found")
		default:
			errNotFound(w, "Not Found")
		}

	case http.MethodPost:
		switch len(parts) {
		case 0:
			// POST /
			pdfPostPreview(w, r, cfg, relayClient, pdfInternalAddr)
		case 2:
			// POST /{area}/thumbnail/
			if parts[1] == "thumbnail" {
				pdfPostThumbnail(w, r, parts[0], cfg, sem, relayClient, pdfInternalAddr)
				return
			}
			errNotFound(w, "Not Found")
		default:
			errNotFound(w, "Not Found")
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// pdfGetPreview handles GET /{id}/{version}/
func pdfGetPreview(
	w http.ResponseWriter,
	r *http.Request,
	rawID, rawVersion string,
	cfg *config.Config,
	store storage.Client,
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	id, err := parseID(rawID)
	if err != nil {
		errValidation(w, "path", "id", err.Error())
		return
	}
	version, err := parseVersion(rawVersion)
	if err != nil {
		errValidation(w, "path", "version", err.Error())
		return
	}
	serviceType, err := parseServiceType(r)
	if err != nil {
		errValidation(w, "query", "service_type", err.Error())
		return
	}
	firstPage, lastPage, err := parsePages(r)
	if err != nil {
		errDetail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w, config.Msg.ItemNotFound)
			return
		}
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return
	}

	sliced, err := pdfSliceRelayFunc(r.Context(), data, firstPage, lastPage, relayClient, pdfInternalAddr)
	if err != nil {
		slog.Error("pdfGetPreview: relayPDFSlice", "err", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		slog.Warn("pdfGetPreview: write", "err", werr)
	}
}

// pdfGetThumbnail handles GET /{id}/{version}/{area}/thumbnail/
func pdfGetThumbnail(
	w http.ResponseWriter,
	r *http.Request,
	rawID, rawVersion, rawArea string,
	cfg *config.Config,
	store storage.Client,
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	id, err := parseID(rawID)
	if err != nil {
		errValidation(w, "path", "id", err.Error())
		return
	}
	version, err := parseVersion(rawVersion)
	if err != nil {
		errValidation(w, "path", "version", err.Error())
		return
	}
	width, height, err := parseArea(rawArea)
	if err != nil {
		errValidation(w, "path", "area", err.Error())
		return
	}
	serviceType, err := parseServiceType(r)
	if err != nil {
		errValidation(w, "query", "service_type", err.Error())
		return
	}
	shape, err := parseShape(r)
	if err != nil {
		errValidation(w, "query", "shape", err.Error())
		return
	}
	quality, err := parseQuality(r)
	if err != nil {
		errValidation(w, "query", "quality", err.Error())
		return
	}
	outputFormat, err := parseOutputFormat(r)
	if err != nil {
		errValidation(w, "query", "output_format", err.Error())
		return
	}

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w, config.Msg.ItemNotFound)
			return
		}
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return
	}

	renderPDFThumbnail(w, r, data, width, height, outputFormat, quality, shape, sem, relayClient, pdfInternalAddr)
}

// pdfPostPreview handles POST /
func pdfPostPreview(
	w http.ResponseWriter,
	r *http.Request,
	cfg *config.Config,
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	firstPage, lastPage, err := parsePages(r)
	if err != nil {
		errDetail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	data, err := readMultipartFile(r)
	if err != nil {
		errValidation(w, "body", "file", config.Msg.FileNotValid)
		return
	}

	sliced, err := pdfSliceRelayFunc(r.Context(), data, firstPage, lastPage, relayClient, pdfInternalAddr)
	if err != nil {
		slog.Error("pdfPostPreview: relayPDFSlice", "err", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		slog.Warn("pdfPostPreview: write", "err", werr)
	}
}

// pdfPostThumbnail handles POST /{area}/thumbnail/
func pdfPostThumbnail(
	w http.ResponseWriter,
	r *http.Request,
	rawArea string,
	cfg *config.Config,
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	width, height, err := parseArea(rawArea)
	if err != nil {
		errValidation(w, "path", "area", err.Error())
		return
	}
	shape, err := parseShape(r)
	if err != nil {
		errValidation(w, "query", "shape", err.Error())
		return
	}
	quality, err := parseQuality(r)
	if err != nil {
		errValidation(w, "query", "quality", err.Error())
		return
	}
	outputFormat, err := parseOutputFormat(r)
	if err != nil {
		errValidation(w, "query", "output_format", err.Error())
		return
	}

	data, err := readMultipartFile(r)
	if err != nil {
		errValidation(w, "body", "file", config.Msg.FileNotValid)
		return
	}

	renderPDFThumbnail(w, r, data, width, height, outputFormat, quality, shape, sem, relayClient, pdfInternalAddr)
}

// renderPDFThumbnail contains the shared logic for rendering a PDF thumbnail.
// If relayClient != nil and pdfInternalAddr != "", the request is relayed to the
// PDF worker pool; otherwise it is rendered in-process (PDF worker role).
func renderPDFThumbnail(
	w http.ResponseWriter,
	r *http.Request,
	data []byte,
	width, height int,
	outputFormat, quality, shape string,
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	if relayClient != nil && pdfInternalAddr != "" {
		relayPDFRender(w, r, data, width, height, outputFormat, quality, shape, relayClient, pdfInternalAddr)
		return
	}

	// PDF worker process: render in-process using PDFium + libvips.
	out, err := pdfRasterizeFunc(sem, data, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		slog.Error("renderPDFThumbnail: PDFRasterize", "err", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	// rounded shape forces PNG output.
	actualFormat := outputFormat
	if shape == "rounded" {
		actualFormat = "png"
	}

	w.Header().Set("Content-Type", contentTypeForFormat(actualFormat))
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		slog.Warn("renderPDFThumbnail: write", "err", werr)
	}
}

// relayPDFRender sends a PDF thumbnail render request to the internal PDF worker pool.
// PDF bytes are POSTed as application/pdf body; render parameters are query params.
func relayPDFRender(
	w http.ResponseWriter,
	r *http.Request,
	data []byte,
	width, height int,
	outputFormat, quality, shape string,
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	relayURL := fmt.Sprintf(
		"%s%s?w=%d&h=%d&fmt=%s&quality=%s&shape=%s",
		pdfInternalAddr, internalPDFRenderPath,
		width, height, outputFormat, quality, shape,
	)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, relayURL, bytes.NewReader(data))
	if err != nil {
		slog.Error("relayPDFRender: build request", "err", err)
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return
	}
	req.Header.Set("Content-Type", "application/pdf")

	resp, err := relayClient.Do(req)
	if err != nil {
		slog.Error("relayPDFRender: relay request failed", "err", err)
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return
	}
	defer resp.Body.Close()

	// Copy status, content-type, and body back to the caller.
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	if _, werr := io.Copy(w, resp.Body); werr != nil {
		slog.Warn("relayPDFRender: copy response body", "err", werr)
	}
}

// relayPDFSlice sends a PDF slice request to the internal PDF worker pool.
// first_page and lastPage are 1-indexed. Returns the sliced PDF bytes,
// or an error (caller maps to HTTP 400 InputError).
func relayPDFSlice(
	ctx context.Context,
	data []byte,
	firstPage, lastPage int,
	relayClient *http.Client,
	pdfInternalAddr string,
) ([]byte, error) {
	relayURL := fmt.Sprintf(
		"%s%s?first_page=%d&last_page=%d",
		pdfInternalAddr, internalPDFSlicePath,
		firstPage, lastPage,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("relayPDFSlice: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/pdf")

	resp, err := relayClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relayPDFSlice: relay failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("relayPDFSlice: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relayPDFSlice: worker returned %d", resp.StatusCode)
	}

	return body, nil
}

// internalPDFRenderPath is the endpoint that PDF worker processes expose.
const internalPDFRenderPath = "/render/pdf"

// internalPDFSlicePath is the endpoint that PDF workers expose for PDF slicing.
const internalPDFSlicePath = "/render/pdf/slice"

// handleInternalPDFSlice is the /render/pdf/slice endpoint served by PDF workers.
// Accepts a POST with raw PDF body and first_page / last_page query params (1-indexed).
// Returns the sliced PDF bytes with Content-Type application/pdf.
func handleInternalPDFSlice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	firstPage, _ := strconv.Atoi(q.Get("first_page"))
	lastPage, _ := strconv.Atoi(q.Get("last_page"))
	if firstPage == 0 {
		firstPage = 1
	}

	data, err := readBody(r)
	if err != nil || len(data) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	out, err := pdfSliceFunc(data, firstPage, lastPage)
	if err != nil {
		slog.Error("handleInternalPDFSlice: PDFSlice", "err", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		slog.Warn("handleInternalPDFSlice: write", "err", werr)
	}
}
