// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// registerImageRoutes registers all /preview/image/* endpoints onto mux.
// The prefix is "/{service_name}/{image_name}".
func registerImageRoutes(
	mux *http.ServeMux,
	cfg *config.Config,
	store storage.Client,
	c *cache.Cache,
	sem chan struct{},
) {
	base := "/" + cfg.ServiceName + "/" + cfg.ServiceImageName

	// We register a catch-all under the image base and dispatch manually.
	// This avoids the lack of path-parameter support in net/http's ServeMux.
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		routeImage(w, r, base, cfg, store, c, sem)
	})
}

// routeImage dispatches image requests by inspecting the URL path.
//
// Supported patterns (relative to base):
//
//	GET  /{id}/{version}/{area}/             → preview
//	GET  /{id}/{version}/{area}/thumbnail/   → thumbnail
//	POST /{area}/                            → preview upload
//	POST /{area}/thumbnail/                  → thumbnail upload
func routeImage(
	w http.ResponseWriter,
	r *http.Request,
	base string,
	cfg *config.Config,
	store storage.Client,
	c *cache.Cache,
	sem chan struct{},
) {
	tail := strings.TrimPrefix(r.URL.Path, base)
	tail = strings.TrimPrefix(tail, "/")
	// Remove trailing slash for consistent segment counting.
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")

	switch r.Method {
	case http.MethodGet:
		// Expected: {id}/{version}/{area}  OR  {id}/{version}/{area}/thumbnail
		if len(parts) == 4 && parts[3] == "thumbnail" {
			imageGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, c, sem)
			return
		}
		if len(parts) == 3 {
			imageGetPreview(w, r, parts[0], parts[1], parts[2], cfg, store, c, sem)
			return
		}
		errNotFound(w, "Not Found")

	case http.MethodPost:
		// Expected: {area}  OR  {area}/thumbnail
		if len(parts) == 2 && parts[1] == "thumbnail" {
			imagePostThumbnail(w, r, parts[0], cfg, sem)
			return
		}
		if len(parts) == 1 {
			imagePostPreview(w, r, parts[0], cfg, sem)
			return
		}
		errNotFound(w, "Not Found")

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// imageGetPreview handles GET /{id}/{version}/{area}/
func imageGetPreview(
	w http.ResponseWriter,
	r *http.Request,
	rawID, rawVersion, rawArea string,
	cfg *config.Config,
	store storage.Client,
	c *cache.Cache,
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
	crop, err := parseCrop(r)
	if err != nil {
		errValidation(w, "query", "crop", err.Error())
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

	key := cacheKey("img-preview", id, version, serviceType, width, height, quality, outputFormat, crop, "rectangular", 1, 0, "en-US", ownerID(r))
	if e, ok := c.Get(key); ok {
		w.Header().Set("Content-Type", e.ContentType)
		w.WriteHeader(http.StatusOK)
		if _, werr := w.Write(e.Body); werr != nil {
			log.Printf("imageGetPreview cache write: %v", werr)
		}
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

	// For preview, crop=true means cover crop (center), crop=false means scale-to-fit.
	cropMode := "center"
	if !crop {
		cropMode = "none"
	}

	out, err := imageThumbnailFunc(sem, data, width, height, outputFormat, quality, "rectangular", cropMode)
	if err != nil {
		log.Printf("imageGetPreview render error: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return
	}

	ct := contentTypeForFormat(outputFormat)
	c.Put(key, cache.Entry{Body: out, ContentType: ct})
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("imageGetPreview write: %v", werr)
	}
}

// imageGetThumbnail handles GET /{id}/{version}/{area}/thumbnail/
func imageGetThumbnail(
	w http.ResponseWriter,
	r *http.Request,
	rawID, rawVersion, rawArea string,
	cfg *config.Config,
	store storage.Client,
	c *cache.Cache,
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

	// crop=true constant: image thumbnails always center-crop.
	key := cacheKey("img-thumb", id, version, serviceType, width, height, quality, outputFormat, true, shape, 1, 0, "en-US", ownerID(r))
	if e, ok := c.Get(key); ok {
		w.Header().Set("Content-Type", e.ContentType)
		w.WriteHeader(http.StatusOK)
		if _, werr := w.Write(e.Body); werr != nil {
			log.Printf("imageGetThumbnail cache write: %v", werr)
		}
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

	// Image thumbnails use CENTER crop (per Python spec).
	out, err := imageThumbnailFunc(sem, data, width, height, outputFormat, quality, shape, "center")
	if err != nil {
		log.Printf("imageGetThumbnail render error: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return
	}

	ct := contentTypeForFormat(outputFormat)
	c.Put(key, cache.Entry{Body: out, ContentType: ct})
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("imageGetThumbnail write: %v", werr)
	}
}

// imagePostPreview handles POST /{area}/
func imagePostPreview(
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
	crop, err := parseCrop(r)
	if err != nil {
		errValidation(w, "query", "crop", err.Error())
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

	cropMode := "center"
	if !crop {
		cropMode = "none"
	}

	out, err := imageThumbnailFunc(sem, data, width, height, outputFormat, quality, "rectangular", cropMode)
	if err != nil {
		log.Printf("imagePostPreview render error: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return
	}

	w.Header().Set("Content-Type", contentTypeForFormat(outputFormat))
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("imagePostPreview write: %v", werr)
	}
}

// imagePostThumbnail handles POST /{area}/thumbnail/
func imagePostThumbnail(
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

	// Image thumbnails use CENTER crop (per Python spec).
	out, err := imageThumbnailFunc(sem, data, width, height, outputFormat, quality, shape, "center")
	if err != nil {
		log.Printf("imagePostThumbnail render error: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return
	}

	w.Header().Set("Content-Type", contentTypeForFormat(outputFormat))
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("imagePostThumbnail write: %v", werr)
	}
}

// readMultipartFile reads the "file" field from a multipart/form-data request.
// Returns the file bytes or an error if the field is missing or empty.
func readMultipartFile(r *http.Request) ([]byte, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, fmt.Errorf("parse multipart: %w", err)
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("form file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty file")
	}
	return data, nil
}

// fetchFromStorage is a convenience wrapper used by pdf.go and document.go
// to retrieve blobs with uniform error handling.
func fetchFromStorage(
	ctx context.Context,
	store storage.Client,
	id string,
	version int,
	serviceType string,
	owner string,
) ([]byte, error) {
	return store.RetrieveData(ctx, id, version, serviceType, owner)
}
