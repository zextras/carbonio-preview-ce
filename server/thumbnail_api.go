// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

const (
	retryAfterSeconds = 3
	cacheTagThumbnail = "t"
	cacheTagInterim   = "g"
)

var touchEvery = 24 * time.Hour

type acceptedError struct {
	body        []byte
	contentType string
}

func (acceptedError) Error() string { return "thumbnail generating" }

type thumbRequest struct {
	p           pipeline
	id          string
	version     int
	size        string
	digest      string
	lang        string
	serviceType string
	owner       string
}

func thumbKey(tag, serviceType, owner, id string, version int, size, digest string) string {
	return tag + "|" + serviceType + "|" + owner + "|" + id + "|" + strconv.Itoa(version) + "|" + size + "|" + digest
}

func (q thumbRequest) key(tag string) string {
	return q.keyFor(tag, q.size)
}

func (q thumbRequest) keyFor(tag, size string) string {
	return thumbKey(tag, q.serviceType, q.owner, q.id, q.version, size, q.digest)
}

func errFormatNotSupported() error {
	return huma.NewError(http.StatusUnsupportedMediaType, config.Msg.FormatNotSupported)
}

func errDependencyDown() error {
	return huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
}

func asyncKind(k kind) bool {
	return k == kindOffice || k == kindVideo
}

func buildGetThumbnail(deps Deps, worker *AsyncWorker) func(context.Context, *apispec.ThumbnailGetInput) (*apispec.ThumbnailOutput, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, in *apispec.ThumbnailGetInput) (*apispec.ThumbnailOutput, error) {
		p, ok := pipelineFor(in.Mime)
		if !ok || !p.thumbnail {
			return nil, errFormatNotSupported()
		}
		id, err := validateUUID(in.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: in.ID})
		}
		if p.kind == kindOffice && deps.Cfg != nil && !deps.Cfg.ServiceEnableDocumentThumbnail {
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.DocumentThumbnailDisabled)
		}
		q := thumbRequest{
			p: p, id: id, version: in.Version, size: string(in.Size), digest: in.Digest,
			lang: in.Lang, serviceType: string(in.ServiceType), owner: in.FileOwnerID,
		}
		key := q.key(cacheTagThumbnail)
		if e, hit := deps.Cache.Get(key); hit {
			deps.touchOnHit(key, q)
			return thumbnailOK(e), nil
		}
		e, err := lim.serve(ctx, deps.Cache, "getThumbnail", key, func(step runner) (cache.Entry, error) {
			return resolveThumbnail(step, deps, worker, q)
		})
		var acc acceptedError
		if errors.As(err, &acc) {
			out := thumbnailBytes(http.StatusAccepted, acc.contentType, acc.body)
			out.CacheControl, out.RetryAfter = "no-store", strconv.Itoa(retryAfterSeconds)
			return out, nil
		}
		var orig originalAnswer
		if errors.As(err, &orig) {
			return streamOriginal(ctx, deps, q, orig.contentType)
		}
		if err != nil {
			return nil, err
		}
		return thumbnailOK(e), nil
	}
}

func thumbnailOK(e cache.Entry) *apispec.ThumbnailOutput {
	return thumbnailBytes(http.StatusOK, e.ContentType, e.Body)
}

func thumbnailBytes(status int, contentType string, body []byte) *apispec.ThumbnailOutput {
	out := &apispec.ThumbnailOutput{Status: status, ContentType: contentType}
	if len(body) > 0 {
		out.ContentLength = strconv.Itoa(len(body))
	}
	out.Body = func(c huma.Context) {
		c.SetStatus(status)
		if len(body) > 0 {
			_, _ = c.BodyWriter().Write(body)
		}
	}
	return out
}

// originalAnswer asks the handler to stream the source itself, so originals never sit in memory or in the cache.
type originalAnswer struct{ contentType string }

func (originalAnswer) Error() string { return "serve the original source" }

func streamOriginal(ctx context.Context, deps Deps, q thumbRequest, contentType string) (*apispec.ThumbnailOutput, error) {
	src, size, err := openSource(ctx, deps.Store, q.id, q.version, q.serviceType, q.owner, fetchIdle(deps.Cfg))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
		}
		return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
	}
	out := &apispec.ThumbnailOutput{Status: http.StatusOK, ContentType: contentType}
	if size >= 0 {
		out.ContentLength = strconv.FormatInt(size, 10)
	}
	out.Body = func(c huma.Context) {
		defer src.Close()
		c.SetStatus(http.StatusOK)
		if _, err := io.Copy(c.BodyWriter(), src); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	return out, nil
}

func (d Deps) touchOnHit(key string, q thumbRequest) {
	store := d.dbStore()
	if store == nil || !d.Cache.TouchDue(key, touchEvery) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		defer cancel()
		if err := store.TouchReady(ctx, q.id, q.version, q.size); err != nil {
			slog.Warn("getThumbnail: TouchReady failed", "file_id", q.id, "version", q.version, "err", err)
		}
	}()
}

func buildDeleteThumbnails(deps Deps) func(context.Context, *apispec.ThumbnailDeleteInput) (*struct{}, error) {
	return func(ctx context.Context, in *apispec.ThumbnailDeleteInput) (*struct{}, error) {
		store := deps.dbStore()
		if store == nil {
			slog.Warn("deleteThumbnails: thumbnail DB not ready, no-op success")
			return nil, nil
		}
		id, err := validateUUID(in.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: in.ID})
		}
		rows, err := store.FindByFile(ctx, id)
		if err != nil {
			if isDBConnError(err) {
				slog.Warn("deleteThumbnails: DB connection down, no-op success", "err", err)
				return nil, nil
			}
			slog.Warn("deleteThumbnails: FindByFile failed", "err", err)
			return nil, errDependencyDown()
		}
		for i := range rows {
			if !deleteRowBlob(ctx, deps.Store, &rows[i], "deleteThumbnails") {
				continue
			}
			if _, err := store.DeleteRow(ctx, rows[i]); err != nil {
				if isDBConnError(err) {
					slog.Warn("deleteThumbnails: DB connection down on delete, no-op success", "err", err)
					return nil, nil
				}
				slog.Warn("deleteThumbnails: DeleteRow failed", "err", err)
				return nil, errDependencyDown()
			}
		}
		return nil, nil
	}
}
