package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
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
			pdfGetPreview(w, r, parts[0], parts[1], cfg, store)
		case 4:
			// GET /{id}/{version}/{area}/thumbnail/
			if parts[3] == "thumbnail" {
				pdfGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, sem, relayClient, pdfInternalAddr)
				return
			}
			errBadRequest(w, config.Msg.InputError)
		default:
			errBadRequest(w, config.Msg.InputError)
		}

	case http.MethodPost:
		switch len(parts) {
		case 0:
			// POST /
			pdfPostPreview(w, r, cfg)
		case 2:
			// POST /{area}/thumbnail/
			if parts[1] == "thumbnail" {
				pdfPostThumbnail(w, r, parts[0], cfg, sem, relayClient, pdfInternalAddr)
				return
			}
			errBadRequest(w, config.Msg.InputError)
		default:
			errBadRequest(w, config.Msg.InputError)
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

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w)
			return
		}
		errStorageUnavailable(w)
		return
	}

	sliced, err := render.PDFSlice(data, firstPage, lastPage)
	if err != nil {
		log.Printf("pdfGetPreview PDFSlice error: %v", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		log.Printf("pdfGetPreview write: %v", werr)
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

	data, err := store.RetrieveData(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w)
			return
		}
		errStorageUnavailable(w)
		return
	}

	renderPDFThumbnail(w, r, data, width, height, outputFormat, quality, shape, sem, relayClient, pdfInternalAddr)
}

// pdfPostPreview handles POST /
func pdfPostPreview(
	w http.ResponseWriter,
	r *http.Request,
	cfg *config.Config,
) {
	firstPage, lastPage, err := parsePages(r)
	if err != nil {
		errBadRequest(w, err.Error())
		return
	}

	data, err := readMultipartFile(r)
	if err != nil {
		errBadRequest(w, config.Msg.FileNotValid)
		return
	}

	sliced, err := render.PDFSlice(data, firstPage, lastPage)
	if err != nil {
		log.Printf("pdfPostPreview PDFSlice error: %v", err)
		errBadRequest(w, config.Msg.InputError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(sliced); werr != nil {
		log.Printf("pdfPostPreview write: %v", werr)
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

	data, err := readMultipartFile(r)
	if err != nil {
		errBadRequest(w, config.Msg.FileNotValid)
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
	out, err := render.PDFRasterize(sem, data, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		log.Printf("renderPDFThumbnail PDFRasterize: %v", err)
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
		log.Printf("renderPDFThumbnail write: %v", werr)
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
		log.Printf("relayPDFRender: build request: %v", err)
		errInternal(w, "relay failed")
		return
	}
	req.Header.Set("Content-Type", "application/pdf")

	resp, err := relayClient.Do(req)
	if err != nil {
		log.Printf("relayPDFRender: relay request failed: %v", err)
		errInternal(w, "relay failed")
		return
	}
	defer resp.Body.Close()

	// Copy status, content-type, and body back to the caller.
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	if _, werr := io.Copy(w, resp.Body); werr != nil {
		log.Printf("relayPDFRender: copy response body: %v", werr)
	}
}

// internalPDFRenderPath is the endpoint that PDF worker processes expose.
const internalPDFRenderPath = "/render/pdf"
