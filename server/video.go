// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/storage"
	"github.com/zextras/carbonio-preview-ce/video"
)

// registerVideoRoutes wires GET /{ServiceName}/video/... endpoints.
func registerVideoRoutes(mux *http.ServeMux, cfg *config.Config, store storage.Client, c *cache.Cache, sem chan struct{}) {
	base := "/" + cfg.ServiceName + "/video"
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		routeVideo(w, r, base, cfg, store, c, sem)
	})
}

// routeVideo mirrors routeImage: GET .../{id}/{version}/{area}/ (preview),
// .../{id}/{version}/{area}/thumbnail/ (thumbnail).
func routeVideo(w http.ResponseWriter, r *http.Request, base string, cfg *config.Config, store storage.Client, c *cache.Cache, sem chan struct{}) {
	tail := strings.TrimPrefix(r.URL.Path, base)
	tail = strings.TrimPrefix(tail, "/")
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if len(parts) == 4 && parts[3] == "thumbnail" {
		videoGetThumbnail(w, r, parts[0], parts[1], parts[2], cfg, store, c, sem)
		return
	}
	if len(parts) == 3 {
		videoGetPreview(w, r, parts[0], parts[1], parts[2], cfg, store, c, sem)
		return
	}
	errNotFound(w, "Not Found")
}

// retrieveFirstFrame streams the blob and extracts frame 0 as PNG. Shared by
// both handlers. Maps storage + extraction errors to HTTP responses; returns
// false if it already wrote the response.
func retrieveFirstFrame(w http.ResponseWriter, r *http.Request, id string, version int, serviceType string, store storage.Client) ([]byte, bool) {
	rc, err := store.RetrieveDataStreaming(r.Context(), id, version, serviceType, ownerID(r))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			errNotFound(w, config.Msg.ItemNotFound)
			return nil, false
		}
		errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		return nil, false
	}
	defer rc.Close()

	frame, err := videoFirstFrameFunc(r.Context(), rc, video.MaxBytes)
	if err != nil {
		if errors.Is(err, video.ErrTooLarge) {
			errDetail(w, http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
			return nil, false
		}
		log.Printf("video first-frame extraction: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return nil, false
	}
	return frame, true
}

func videoGetThumbnail(w http.ResponseWriter, r *http.Request, rawID, rawVersion, rawArea string, cfg *config.Config, store storage.Client, c *cache.Cache, sem chan struct{}) {
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

	key := cacheKey("video-thumb", id, version, serviceType, width, height, quality, outputFormat, true, shape, 1, 0, "en-US", ownerID(r))
	if e, ok := c.Get(key); ok {
		w.Header().Set("Content-Type", e.ContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(e.Body)
		return
	}

	frame, ok := retrieveFirstFrame(w, r, id, version, serviceType, store)
	if !ok {
		return
	}

	out, err := imageThumbnailFunc(sem, frame, width, height, outputFormat, quality, shape, "center")
	if err != nil {
		log.Printf("videoGetThumbnail render error: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return
	}

	ct := contentTypeForFormat(outputFormat)
	c.Put(key, cache.Entry{Body: out, ContentType: ct})
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("videoGetThumbnail write: %v", werr)
	}
}

func videoGetPreview(w http.ResponseWriter, r *http.Request, rawID, rawVersion, rawArea string, cfg *config.Config, store storage.Client, c *cache.Cache, sem chan struct{}) {
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

	key := cacheKey("video-preview", id, version, serviceType, width, height, quality, outputFormat, crop, "rectangular", 1, 0, "en-US", ownerID(r))
	if e, ok := c.Get(key); ok {
		w.Header().Set("Content-Type", e.ContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(e.Body)
		return
	}

	frame, ok := retrieveFirstFrame(w, r, id, version, serviceType, store)
	if !ok {
		return
	}

	cropMode := "center"
	if !crop {
		cropMode = "none"
	}
	out, err := imageThumbnailFunc(sem, frame, width, height, outputFormat, quality, "rectangular", cropMode)
	if err != nil {
		log.Printf("videoGetPreview render error: %v", err)
		errBadRequest(w, config.Msg.FormatNotSupported)
		return
	}

	ct := contentTypeForFormat(outputFormat)
	c.Put(key, cache.Entry{Body: out, ContentType: ct})
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("videoGetPreview write: %v", werr)
	}
}
