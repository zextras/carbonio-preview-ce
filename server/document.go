// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// registerDocumentRoutes registers all /preview/document/* endpoints onto mux.
func registerDocumentRoutes(
	mux *http.ServeMux,
	cfg *config.Config,
	store storage.Client,
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string,
) {
	base := "/" + cfg.ServiceName + "/" + cfg.ServiceDocumentName

	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		routeDocument(w, r, base, cfg, store, sem, relayClient, pdfInternalAddr)
	})
}

// routeDocument dispatches document requests by path inspection.
//
// Patterns (relative to base):
//
//	GET  /{id}/{version}/                    → document preview (→ PDF)  [gated by EnableDocumentPreview]
//	GET  /{id}/{version}/{area}/thumbnail/   → document thumbnail        [gated by EnableDocumentThumbnail]
//	POST /                                   → document preview upload
//	POST /{area}/thumbnail/                  → document thumbnail upload
func routeDocument(
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
	if len(parts) == 1 && parts[0] == "" {
		parts = []string{}
	}

	switch r.Method {
	case http.MethodGet:
		switch len(parts) {
		case 2:
			// GET /{id}/{version}/
			if !cfg.ServiceEnableDocumentPreview {
				errBadRequest(w, config.Msg.DocumentPreviewDisabled)
				return
			}
			docGetPreview(w, r, parts[0], parts[1], cfg, store, relayClient, pdfInternalAddr)
		case 4:
			// GET /{id}/{version}/{area}/thumbnail/
			if parts[3] != "thumbnail" {
				errNotFound(w, "Not Found")
				return
			}
			if !cfg.ServiceEnableDocumentThumbnail {
				errBadRequest(w, config.Msg.DocumentThumbnailDisabled)
				return
			}
			docGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, sem, relayClient, pdfInternalAddr)
		default:
			errNotFound(w, "Not Found")
		}

	case http.MethodPost:
		switch len(parts) {
		case 0:
			// POST /
			if !cfg.ServiceEnableDocumentPreview {
				errBadRequest(w, config.Msg.DocumentPreviewDisabled)
				return
			}
			docPostPreview(w, r, cfg, relayClient, pdfInternalAddr)
		case 2:
			// POST /{area}/thumbnail/
			if parts[1] != "thumbnail" {
				errNotFound(w, "Not Found")
				return
			}
			if !cfg.ServiceEnableDocumentThumbnail {
				errBadRequest(w, config.Msg.DocumentThumbnailDisabled)
				return
			}
			docPostThumbnail(w, r, parts[0], cfg, sem, relayClient, pdfInternalAddr)
		default:
			errNotFound(w, "Not Found")
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// docGetPreview handles GET /{id}/{version}/
// Converts the document to PDF via Collabora, slices to page range, returns PDF bytes.
func docGetPreview(
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
	langTag := parseLangTag(r)

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w, config.Msg.ItemNotFound)
			return
		}
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		slog.Error("docGetPreview: convert", "err", err)
		errDetail(w, http.StatusBadGateway, config.Msg.StorageUnavailable)
		return
	}

	sliced, err := pdfSliceRelayFunc(r.Context(), pdfBytes, firstPage, lastPage, relayClient, pdfInternalAddr)
	if err != nil {
		slog.Error("docGetPreview: relayPDFSlice", "err", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		slog.Warn("docGetPreview: write", "err", werr)
	}
}

// docGetThumbnail handles GET /{id}/{version}/{area}/thumbnail/
func docGetThumbnail(
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
	langTag := parseLangTag(r)

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w, config.Msg.ItemNotFound)
			return
		}
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		slog.Error("docGetThumbnail: convert", "err", err)
		errDetail(w, http.StatusBadGateway, config.Msg.StorageUnavailable)
		return
	}

	renderPDFThumbnail(w, r, pdfBytes, width, height, outputFormat, quality, shape, sem, relayClient, pdfInternalAddr)
}

// docPostPreview handles POST /
func docPostPreview(
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
	langTag := parseLangTag(r)

	data, err := readMultipartFile(r)
	if err != nil {
		errValidation(w, "body", "file", config.Msg.FileNotValid)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		slog.Error("docPostPreview: convert", "err", err)
		errDetail(w, http.StatusBadGateway, config.Msg.StorageUnavailable)
		return
	}

	sliced, err := pdfSliceRelayFunc(r.Context(), pdfBytes, firstPage, lastPage, relayClient, pdfInternalAddr)
	if err != nil {
		slog.Error("docPostPreview: relayPDFSlice", "err", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		slog.Warn("docPostPreview: write", "err", werr)
	}
}

// docPostThumbnail handles POST /{area}/thumbnail/
func docPostThumbnail(
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
	langTag := parseLangTag(r)

	data, err := readMultipartFile(r)
	if err != nil {
		errValidation(w, "body", "file", config.Msg.FileNotValid)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		slog.Error("docPostThumbnail: convert", "err", err)
		errDetail(w, http.StatusBadGateway, config.Msg.StorageUnavailable)
		return
	}

	renderPDFThumbnail(w, r, pdfBytes, width, height, outputFormat, quality, shape, sem, relayClient, pdfInternalAddr)
}

// convertDocToPDF calls CollaboraConvert to turn the document bytes into PDF.
// The output extension is always "pdf" for document preview.
func convertDocToPDF(r *http.Request, data []byte, langTag string, cfg *config.Config) ([]byte, error) {
	docsTimeout := time.Duration(cfg.ServiceDocsTimeout) * time.Second
	// docsEditorURL = full convert-to URL + "/pdf"
	docsURL := cfg.DocumentConversionFullConvertAddress + "/pdf"
	return collaboraConvertFunc(r.Context(), data, langTag, docsURL, docsTimeout)
}
