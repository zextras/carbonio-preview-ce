// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// video_api.go implements the four video HTTP endpoints:
//   GET  /preview/video/{id}/{version}/{area}/
//   GET  /preview/video/{id}/{version}/{area}/thumbnail/
//   DELETE /preview/video/{id}/{version}/
//   POST   /preview/video/{id}/{version}/copy/
//
// All handlers use the resolve() state machine for GET paths, which is a port
// of WSC's VideoPreviewServiceImpl.resolve().

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/zextras/carbonio-preview-ce/v2/cache"
	"github.com/zextras/carbonio-preview-ce/v2/config"
	"github.com/zextras/carbonio-preview-ce/v2/db"
	"github.com/zextras/carbonio-preview-ce/v2/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v2/storage"
)

// ---------------------------------------------------------------------------
// resolve() state machine
// ---------------------------------------------------------------------------

// resolveResult is the outcome of resolve().
type resolveResult struct {
	// previewID is set only when Status == StatusReady.
	previewID string
	// httpStatus is one of: 200 (READY), 202 (not yet ready), 415 (UNSUPPORTED), 422 (FAILED).
	httpStatus int
}

// resolve runs the video-preview state machine for a GET request.
// It is a port of VideoPreviewServiceImpl.resolve() from WSC, adapted for
// preview's own DB + storage.
//
// State transitions:
//  1. Not found       → EnqueueIfAbsent + fire async attempt → 202
//  2. PENDING         → fire async attempt (non-blocking) → 202
//  3. GENERATING      → fire async attempt (non-blocking) → 202
//  4. READY           → DB-only check → 200 (previewID set)
//     Blob existence is verified lazily by the handler: ErrNotFound → ReenqueueReady → 202
//  5. UNSUPPORTED     → 415
//  6. FAILED          → 422
func resolve(
	ctx context.Context,
	deps Deps,
	worker *VideoWorker,
	fileID string,
	version int,
	serviceType string,
	ownerID string,
) resolveResult {
	store := deps.videoStore()
	if store == nil {
		// DB layer disabled / not ready — the dependency is down. 424 (Failed
		// Dependency): operational, NOT 415 (which is a genuinely-unsupported
		// codec, and requires a working DB to have been probed).
		return resolveResult{httpStatus: http.StatusFailedDependency}
	}

	row, err := store.Find(ctx, fileID, version)
	if err != nil {
		slog.Warn("resolve: DB.Find error", "file_id", fileID, "version", version, "err", err)
		// A connection-level error at runtime means the DB went down after boot:
		// degrade to 424 (dependency down), same as boot-time absence.
		return resolveResult{httpStatus: http.StatusFailedDependency}
	}

	if row == nil {
		// Not found: enqueue and fire an immediate attempt.
		if eerr := store.EnqueueIfAbsent(ctx, fileID, version, ownerID, serviceType); eerr != nil {
			slog.Warn("resolve: EnqueueIfAbsent error", "err", eerr)
		}
		fireAsyncAttempt(ctx, deps, worker, fileID, version)
		return resolveResult{httpStatus: http.StatusAccepted}
	}

	switch row.Status {
	case db.StatusReady:
		if row.PreviewID == nil || *row.PreviewID == "" {
			// Unexpected: READY without a previewID — move back to PENDING for regeneration.
			slog.Warn("resolve: READY row with nil previewID, re-enqueueing",
				"file_id", fileID, "version", version)
			if rerr := store.ReenqueueReady(ctx, fileID, version); rerr != nil {
				slog.Warn("resolve: ReenqueueReady (nil previewID) error", "err", rerr)
			}
			fireAsyncAttempt(ctx, deps, worker, fileID, version)
			return resolveResult{httpStatus: http.StatusAccepted}
		}
		pid := *row.PreviewID
		// DB says READY: return 200 with previewID. The handler will fetch the blob
		// and handle a missing blob lazily (ErrNotFound → ReenqueueReady → 202).
		return resolveResult{httpStatus: http.StatusOK, previewID: pid}

	case db.StatusUnsupported:
		// If the codec is now in the supported list (binary expanded), re-enqueue.
		if row.Codec != nil && *row.Codec != "" && isSupportedVideoCodec(*row.Codec) {
			slog.Info("resolve: codec now in supported list, re-enqueueing",
				"file_id", fileID, "version", version, "codec", *row.Codec)
			if rerr := store.ReenqueueUnsupported(ctx, fileID, version); rerr != nil {
				slog.Warn("resolve: ReenqueueUnsupported error", "err", rerr)
			}
			fireAsyncAttempt(ctx, deps, worker, fileID, version)
			return resolveResult{httpStatus: http.StatusAccepted}
		}
		return resolveResult{httpStatus: http.StatusUnsupportedMediaType}

	case db.StatusFailed:
		return resolveResult{httpStatus: http.StatusUnprocessableEntity}

	default: // PENDING or GENERATING
		fireAsyncAttempt(ctx, deps, worker, fileID, version)
		return resolveResult{httpStatus: http.StatusAccepted}
	}
}

// fireAsyncAttempt attempts to claim the row and submit a generate job to the
// worker. This is a non-blocking best-effort fast-path: if the semaphore is
// busy or the claim is lost (race with another instance), the row simply waits
// for the next sweep tick. No error is returned — failures are logged only.
//
// The goroutine uses context.WithoutCancel(ctx) so that the HTTP handler
// returning 202 (and net/http cancelling the request context) does not abort
// an in-flight generateFirstFrameJPEG call. Values (trace ids etc.) are
// preserved; only the cancellation signal is dropped.
func fireAsyncAttempt(ctx context.Context, deps Deps, worker *VideoWorker, fileID string, version int) {
	store := deps.videoStore()
	if worker == nil || store == nil {
		return
	}
	// Detach from request lifetime: keep values but drop cancellation so that
	// the 202 response completing does not cancel the in-flight generation.
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		claimed, err := store.Claim(bgCtx, fileID, version, worker.instanceID)
		if err != nil || !claimed {
			return // lost the race or DB error — sweep will retry
		}
		// Acquire semaphore non-blocking.
		if !worker.tryAcquireSem() {
			// Busy — return the row without counting an attempt (back-pressure, not a failure).
			slog.Debug("fireAsyncAttempt: semaphore busy on immediate attempt, releasing without attempt increment",
				"file_id", fileID, "version", version)
			_ = store.Release(bgCtx, fileID, version, worker.instanceID)
			return
		}
		key := liveKey(fileID, version)
		worker.mu.Lock()
		worker.live[key] = struct{}{}
		worker.mu.Unlock()
		go func() {
			defer func() {
				worker.mu.Lock()
				delete(worker.live, key)
				worker.mu.Unlock()
			}()
			defer worker.releaseSem()
			// Re-read the row to get ownerID and serviceType (they may not be known here).
			row, rerr := store.Find(bgCtx, fileID, version)
			if rerr != nil || row == nil {
				if rerr != nil {
					slog.Warn("fireAsyncAttempt: Find error before attempt, releasing", "err", rerr,
						"file_id", fileID, "version", version)
				} else {
					slog.Debug("fireAsyncAttempt: row vanished before attempt, releasing",
						"file_id", fileID, "version", version)
				}
				_ = store.Release(bgCtx, fileID, version, worker.instanceID)
				return
			}
			worker.attempt(bgCtx, *row)
		}()
	}()
}

// ---------------------------------------------------------------------------
// Handler: GET /preview/video/{id}/{version}/{area}/
// ---------------------------------------------------------------------------

func buildGetVideoPreview(deps Deps, worker *VideoWorker) func(context.Context, *apispec.VideoGetPreviewInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.VideoGetPreviewInput) (*apispec.BinOut, error) {
		// Top-level short-circuit: with no ready DB we cannot even probe the
		// codec, so return 424 (Failed Dependency) BEFORE the resolve() state
		// machine. Distinct from 415 (genuinely-unsupported codec).
		if deps.videoStore() == nil {
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		serviceType := string(input.ServiceType)
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		crop := input.Crop
		ownerHeader := input.FileOwnerID

		res := resolve(ctx, deps, worker, id, input.Version, serviceType, ownerHeader)
		switch res.httpStatus {
		case http.StatusAccepted:
			return nil, huma.NewError(http.StatusAccepted, "generating")
		case http.StatusUnsupportedMediaType:
			return nil, huma.NewError(http.StatusUnsupportedMediaType, "video format not supported")
		case http.StatusUnprocessableEntity:
			return nil, huma.NewError(http.StatusUnprocessableEntity, "video preview generation failed")
		case http.StatusFailedDependency:
			// DB went down between the top-level check and resolve() — degrade to 424.
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}
		// Status 200: serve the READY frame through the image pipeline.
		pid := res.previewID

		key := cacheKey("vid-preview", pid, input.Version, serviceType, width, height, quality, outputFormat, crop, "rectangular", 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, pid, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				// Blob disappeared between resolve and serve — move READY row back to PENDING + 202.
				slog.Warn("getVideoPreview: blob 404 after resolve, re-enqueueing",
					"file_id", id, "version", input.Version, "err", rerr)
				if store := deps.videoStore(); store != nil {
					_ = store.ReenqueueReady(ctx, id, input.Version)
				}
				return nil, huma.NewError(http.StatusAccepted, "generating")
			}
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
		}

		cropMode := "none"
		if crop {
			cropMode = "center"
		}
		out, rerr := imageThumbnailFunc(nil, data, width, height, outputFormat, quality, "rectangular", cropMode)
		if rerr != nil {
			slog.Warn("getVideoPreview render", "err", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		ct := contentTypeForFormat(outputFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &apispec.BinOut{ContentType: ct, Body: out}, nil
	}
}

// ---------------------------------------------------------------------------
// Handler: GET /preview/video/{id}/{version}/{area}/thumbnail/
// ---------------------------------------------------------------------------

func buildGetVideoThumbnail(deps Deps, worker *VideoWorker) func(context.Context, *apispec.VideoGetThumbnailInput) (*apispec.BinOut, error) {
	return func(ctx context.Context, input *apispec.VideoGetThumbnailInput) (*apispec.BinOut, error) {
		// Top-level short-circuit: no ready DB ⇒ 424 (Failed Dependency), BEFORE
		// resolve(). Distinct from 415 (genuinely-unsupported codec).
		if deps.videoStore() == nil {
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		width, height, err := parseArea(input.Area)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: err.Error(), Location: "path.area", Value: input.Area})
		}
		serviceType := string(input.ServiceType)
		quality := string(input.Quality)
		outputFormat := string(input.OutputFormat)
		shape := string(input.Shape)
		ownerHeader := input.FileOwnerID

		res := resolve(ctx, deps, worker, id, input.Version, serviceType, ownerHeader)
		switch res.httpStatus {
		case http.StatusAccepted:
			return nil, huma.NewError(http.StatusAccepted, "generating")
		case http.StatusUnsupportedMediaType:
			return nil, huma.NewError(http.StatusUnsupportedMediaType, "video format not supported")
		case http.StatusUnprocessableEntity:
			return nil, huma.NewError(http.StatusUnprocessableEntity, "video preview generation failed")
		case http.StatusFailedDependency:
			// DB went down between the top-level check and resolve() — degrade to 424.
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}
		pid := res.previewID

		key := cacheKey("vid-thumb", pid, input.Version, serviceType, width, height, quality, outputFormat, true, shape, 1, 0, "en-US", ownerHeader)
		if e, ok := deps.Cache.Get(key); ok {
			return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
		}

		data, rerr := deps.Store.RetrieveData(ctx, pid, input.Version, serviceType, ownerHeader)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				// Blob disappeared between resolve and serve — move READY row back to PENDING + 202.
				slog.Warn("getVideoThumbnail: blob 404 after resolve, re-enqueueing",
					"file_id", id, "version", input.Version, "err", rerr)
				if store := deps.videoStore(); store != nil {
					_ = store.ReenqueueReady(ctx, id, input.Version)
				}
				return nil, huma.NewError(http.StatusAccepted, "generating")
			}
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
		}

		out, rerr := imageThumbnailFunc(nil, data, width, height, outputFormat, quality, shape, "center")
		if rerr != nil {
			slog.Warn("getVideoThumbnail render", "err", rerr)
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
		}

		ct := contentTypeForFormat(outputFormat)
		deps.Cache.Put(key, cache.Entry{Body: out, ContentType: ct})
		return &apispec.BinOut{ContentType: ct, Body: out}, nil
	}
}

// ---------------------------------------------------------------------------
// Handler: DELETE /preview/video/{id}/{version}/
// ---------------------------------------------------------------------------

func buildDeleteVideoPreview(deps Deps) func(context.Context, *apispec.VideoDeleteInput) (*struct{}, error) {
	return func(ctx context.Context, input *apispec.VideoDeleteInput) (*struct{}, error) {
		// DB down/absent ⇒ no-op success (204). Delete is called fire-and-forget
		// by WSC on node deletion; surfacing an error would make the caller retry
		// forever / log noise. There is no row to delete when the DB is down, and
		// the row (if any) will be swept out later — nothing is leaked.
		store := deps.videoStore()
		if store == nil {
			slog.Warn("deleteVideoPreview: video DB not ready — no-op success")
			return nil, nil
		}
		id, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		serviceType := string(input.ServiceType)
		ownerHeader := input.FileOwnerID

		row, derr := store.Find(ctx, id, input.Version)
		if derr != nil {
			// Runtime DB connection error: degrade to no-op success (same rationale
			// as the absent-DB path above).
			if isDBConnError(derr) {
				slog.Warn("deleteVideoPreview: DB connection down — no-op success", "err", derr)
				return nil, nil
			}
			slog.Warn("deleteVideoPreview: DB.Find error", "err", derr)
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}

		// Best-effort blob delete (if a preview frame exists).
		if row != nil && row.PreviewID != nil && *row.PreviewID != "" {
			if berr := deps.Store.Delete(ctx, *row.PreviewID, input.Version, serviceType, ownerHeader); berr != nil {
				slog.Warn("deleteVideoPreview: Store.Delete error (swallowed)",
					"preview_id", *row.PreviewID, "err", berr)
			}
		}

		// Delete the DB row unconditionally (idempotent).
		if dberr := store.DeleteByFileId(ctx, id, input.Version); dberr != nil {
			if isDBConnError(dberr) {
				slog.Warn("deleteVideoPreview: DB connection down on delete — no-op success", "err", dberr)
				return nil, nil
			}
			slog.Warn("deleteVideoPreview: DB.DeleteByFileId error", "err", dberr)
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}

		// 204 No Content — huma emits no body when the output struct is nil.
		return nil, nil
	}
}

// ---------------------------------------------------------------------------
// Handler: POST /preview/video/{id}/{version}/copy/
// ---------------------------------------------------------------------------

// VideoCopyOutput is the JSON body for a successful copy response.
// Defined here (not in apispec) because it is only used by the handler.
// It is also defined in apispec/types.go as the registered huma output type.

func buildCopyVideoPreview(deps Deps) func(context.Context, *apispec.VideoCopyInput) (*apispec.VideoCopyOutput, error) {
	return func(ctx context.Context, input *apispec.VideoCopyInput) (*apispec.VideoCopyOutput, error) {
		// DB down/absent ⇒ no-op success (empty body). Copy is called
		// fire-and-forget by WSC on node copy; surfacing an error would make the
		// caller retry/log. The destination simply has no video preview yet — it
		// will be regenerated lazily on first request once the DB is back.
		store := deps.videoStore()
		if store == nil {
			slog.Warn("copyVideoPreview: video DB not ready — no-op success")
			return &apispec.VideoCopyOutput{}, nil
		}
		srcID, err := validateUUID(input.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: input.ID})
		}
		targetFileID, err := validateUUID(input.Target)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "query.target", Value: input.Target})
		}
		serviceType := string(input.ServiceType)
		srcOwner := input.FileOwnerID
		dstOwner := input.TargetOwnerID

		row, ferr := store.Find(ctx, srcID, input.Version)
		if ferr != nil {
			if isDBConnError(ferr) {
				slog.Warn("copyVideoPreview: DB connection down — no-op success", "err", ferr)
				return &apispec.VideoCopyOutput{}, nil
			}
			slog.Warn("copyVideoPreview: DB.Find error", "err", ferr)
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}
		if row == nil || row.Status != db.StatusReady || row.PreviewID == nil || *row.PreviewID == "" {
			return nil, huma.NewError(http.StatusNotFound, "source video preview not ready")
		}

		// Read the source frame blob.
		srcPreviewID := *row.PreviewID
		bytes, rerr := deps.Store.RetrieveData(ctx, srcPreviewID, input.Version, serviceType, srcOwner)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrNotFound) {
				return nil, huma.NewError(http.StatusNotFound, "source preview blob not found")
			}
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
		}

		// Mint a new preview blob UUID for the copy.
		newPreviewID := uuid.New().String()

		// Store the copy under the destination owner.
		if _, serr := deps.Store.StoreData(ctx, newPreviewID, input.Version, serviceType, dstOwner, bytes); serr != nil {
			slog.Warn("copyVideoPreview: StoreData error", "err", serr)
			return nil, huma.NewError(http.StatusBadGateway, config.Msg.GenericErrorStorage)
		}

		// Insert a READY row for the target file ID (ON CONFLICT DO NOTHING — idempotent).
		if ierr := store.InsertReady(ctx, targetFileID, input.Version, dstOwner, serviceType, newPreviewID); ierr != nil {
			// Best-effort cleanup of the newly stored blob.
			go func() {
				_ = deps.Store.Delete(context.Background(), newPreviewID, input.Version, serviceType, dstOwner)
			}()
			slog.Warn("copyVideoPreview: InsertReady error", "err", ierr)
			return nil, huma.NewError(http.StatusFailedDependency, config.Msg.GenericErrorStorage)
		}

		out := &apispec.VideoCopyOutput{}
		out.Body.PreviewID = newPreviewID
		return out, nil
	}
}
