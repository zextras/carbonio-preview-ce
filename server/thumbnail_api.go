// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
)

const (
	retryAfterSeconds = 3
	cacheTagThumbnail = "t"
)

var touchEvery = 24 * time.Hour

type acceptedError struct{}

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
	return thumbKey(tag, q.serviceType, q.owner, q.id, q.version, q.size, q.digest)
}

func errFormatNotSupported() error {
	return huma.NewError(http.StatusUnsupportedMediaType, config.Msg.FormatNotSupported)
}

func errDependencyDown() error {
	return huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
}

func asyncThumbnail(p pipeline) bool {
	return p.thumbnail && (p.kind == kindOffice || p.kind == kindVideo)
}

func buildGetThumbnail(deps Deps, worker *AsyncWorker) func(context.Context, *apispec.ThumbnailGetInput) (*apispec.ThumbnailOutput, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, in *apispec.ThumbnailGetInput) (*apispec.ThumbnailOutput, error) {
		p, ok := pipelineFor(in.Mime)
		if !ok || !asyncThumbnail(p) {
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
			return &apispec.ThumbnailOutput{
				Status:       http.StatusAccepted,
				CacheControl: "no-store",
				RetryAfter:   strconv.Itoa(retryAfterSeconds),
			}, nil
		}
		if err != nil {
			return nil, err
		}
		return thumbnailOK(e), nil
	}
}

func thumbnailOK(e cache.Entry) *apispec.ThumbnailOutput {
	return &apispec.ThumbnailOutput{Status: http.StatusOK, ContentType: e.ContentType, Body: e.Body}
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
			deleteRowBlob(ctx, deps.Store, &rows[i], "deleteThumbnails")
		}
		if err := store.DeleteByFile(ctx, id); err != nil {
			if isDBConnError(err) {
				slog.Warn("deleteThumbnails: DB connection down on delete, no-op success", "err", err)
				return nil, nil
			}
			slog.Warn("deleteThumbnails: DeleteByFile failed", "err", err)
			return nil, errDependencyDown()
		}
		return nil, nil
	}
}
