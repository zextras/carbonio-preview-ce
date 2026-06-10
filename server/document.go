package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
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
			docGetPreview(w, r, parts[0], parts[1], cfg, store)
		case 4:
			// GET /{id}/{version}/{area}/thumbnail/
			if parts[3] != "thumbnail" {
				errBadRequest(w, config.Msg.InputError)
				return
			}
			if !cfg.ServiceEnableDocumentThumbnail {
				errBadRequest(w, config.Msg.DocumentThumbnailDisabled)
				return
			}
			docGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, sem, relayClient, pdfInternalAddr)
		default:
			errBadRequest(w, config.Msg.InputError)
		}

	case http.MethodPost:
		switch len(parts) {
		case 0:
			// POST /
			if !cfg.ServiceEnableDocumentPreview {
				errBadRequest(w, config.Msg.DocumentPreviewDisabled)
				return
			}
			docPostPreview(w, r, cfg)
		case 2:
			// POST /{area}/thumbnail/
			if parts[1] != "thumbnail" {
				errBadRequest(w, config.Msg.InputError)
				return
			}
			if !cfg.ServiceEnableDocumentThumbnail {
				errBadRequest(w, config.Msg.DocumentThumbnailDisabled)
				return
			}
			docPostThumbnail(w, r, parts[0], cfg, sem, relayClient, pdfInternalAddr)
		default:
			errBadRequest(w, config.Msg.InputError)
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
) {
	id, err := parseID(rawID)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	version, err := parseVersion(rawVersion)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	serviceType, err := parseServiceType(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	firstPage, lastPage, err := parsePages(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	langTag := parseLangTag(r)

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w)
			return
		}
		errStorageUnavailable(w)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		log.Printf("docGetPreview convert error: %v", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, config.Msg.StorageUnavailable)
		return
	}

	sliced, err := render.PDFSlice(pdfBytes, firstPage, lastPage)
	if err != nil {
		log.Printf("docGetPreview PDFSlice error: %v", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		log.Printf("docGetPreview write: %v", werr)
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
		errBadRequest(w, err.Error())
		return
	}
	version, err := parseVersion(rawVersion)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	width, height, err := parseArea(rawArea)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	serviceType, err := parseServiceType(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	shape, err := parseShape(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	quality, err := parseQuality(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	outputFormat, err := parseOutputFormat(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	langTag := parseLangTag(r)

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w)
			return
		}
		errStorageUnavailable(w)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		log.Printf("docGetThumbnail convert error: %v", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, config.Msg.StorageUnavailable)
		return
	}

	renderPDFThumbnail(w, r, pdfBytes, width, height, outputFormat, quality, shape, sem, relayClient, pdfInternalAddr)
}

// docPostPreview handles POST /
func docPostPreview(
	w http.ResponseWriter,
	r *http.Request,
	cfg *config.Config,
) {
	firstPage, lastPage, err := parsePages(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	langTag := parseLangTag(r)

	data, err := readMultipartFile(r)
	if err != nil {
		errBadRequest(w, config.Msg.FileNotValid)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		log.Printf("docPostPreview convert error: %v", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, config.Msg.StorageUnavailable)
		return
	}

	sliced, err := render.PDFSlice(pdfBytes, firstPage, lastPage)
	if err != nil {
		log.Printf("docPostPreview PDFSlice error: %v", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		log.Printf("docPostPreview write: %v", werr)
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
		errBadRequest(w, err.Error())
		return
	}
	shape, err := parseShape(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	quality, err := parseQuality(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	outputFormat, err := parseOutputFormat(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}
	langTag := parseLangTag(r)

	data, err := readMultipartFile(r)
	if err != nil {
		errBadRequest(w, config.Msg.FileNotValid)
		return
	}

	pdfBytes, err := convertDocToPDF(r, data, langTag, cfg)
	if err != nil {
		log.Printf("docPostThumbnail convert error: %v", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, config.Msg.StorageUnavailable)
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
	return render.CollaboraConvert(r.Context(), data, langTag, docsURL, docsTimeout)
}
