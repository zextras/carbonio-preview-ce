// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"errors"
	"log/slog"
	"net/http"
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
) {
	base := "/" + cfg.ServiceName + "/" + cfg.ServicePDFName

	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		routePDF(w, r, base, cfg, store, sem)
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
			pdfGetPreview(w, r, parts[0], parts[1], cfg, store)
		case 4:
			// GET /{id}/{version}/{area}/thumbnail/
			if parts[3] == "thumbnail" {
				pdfGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, sem)
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
			pdfPostPreview(w, r, cfg)
		case 2:
			// POST /{area}/thumbnail/
			if parts[1] == "thumbnail" {
				pdfPostThumbnail(w, r, parts[0], cfg, sem)
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

	sliced, err := pdfSliceFunc(data, firstPage, lastPage)
	if err != nil {
		slog.Error("pdfGetPreview: PDFSlice", "err", err)
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

	renderPDFThumbnail(w, r, data, width, height, outputFormat, quality, shape, sem)
}

// pdfPostPreview handles POST /
func pdfPostPreview(
	w http.ResponseWriter,
	r *http.Request,
	cfg *config.Config,
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

	sliced, err := pdfSliceFunc(data, firstPage, lastPage)
	if err != nil {
		slog.Error("pdfPostPreview: PDFSlice", "err", err)
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

	renderPDFThumbnail(w, r, data, width, height, outputFormat, quality, shape, sem)
}

// renderPDFThumbnail renders a PDF page to an image and writes the response.
func renderPDFThumbnail(
	w http.ResponseWriter,
	r *http.Request,
	data []byte,
	width, height int,
	outputFormat, quality, shape string,
	sem chan struct{},
) {
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
