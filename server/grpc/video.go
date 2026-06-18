// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/video"
)

// firstFrame streams the blob from storage and extracts frame 0 as PNG bytes.
//
// When the caller's context is cancelled or its deadline passes, firstFrame
// returns ctx.Err() directly. gRPC maps context.Canceled → codes.Canceled and
// context.DeadlineExceeded → codes.DeadlineExceeded without logging — the
// client is already gone and the cancellation is not an extraction error.
func (s *PreviewServer) firstFrame(ctx context.Context, id string, version int, serviceType, ownerID string) ([]byte, error) {
	rc, err := s.store.RetrieveDataStreaming(ctx, id, version, serviceType, ownerID)
	if err != nil {
		// Silent path: client cancelled before or during storage fetch.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, storageErr(err)
	}
	defer rc.Close()
	frame, err := s.videoFirstFrame(ctx, rc, video.MaxBytes)
	if err != nil {
		// Silent path: client cancelled during the long download+extract operation.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, video.ErrTooLarge) {
			return nil, toStatus(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
		}
		slog.Error("video first-frame extraction", "err", err)
		return nil, toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return frame, nil
}

// GetVideoPreview implements PreviewService.GetVideoPreview.
func (s *PreviewServer) GetVideoPreview(req *pb.GetRequest, stream pb.PreviewService_GetVideoPreviewServer) error {
	p := req.GetParams()
	id, version, serviceType, err := parseGetParams(p)
	if err != nil {
		return err
	}
	width, height, err := parseGRPCArea(p.GetArea())
	if err != nil {
		return err
	}
	outputFormat, err := parseOutputFormat(p.GetOutputFormat())
	if err != nil {
		return err
	}
	quality, err := parseGRPCQuality(p.GetQuality())
	if err != nil {
		return err
	}
	cropMode := parseCropMode(p.GetCrop())

	frame, err := s.firstFrame(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return err
	}
	out, err := s.imageThumbnail(s.sem, frame, width, height, outputFormat, quality, "rectangular", cropMode)
	if err != nil {
		slog.Error("GetVideoPreview: render", "err", err)
		return toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return streamBlob(stream, contentTypeForFormat(outputFormat), out)
}

// GetVideoThumbnail implements PreviewService.GetVideoThumbnail.
func (s *PreviewServer) GetVideoThumbnail(req *pb.GetRequest, stream pb.PreviewService_GetVideoThumbnailServer) error {
	p := req.GetParams()
	id, version, serviceType, err := parseGetParams(p)
	if err != nil {
		return err
	}
	width, height, err := parseGRPCArea(p.GetArea())
	if err != nil {
		return err
	}
	outputFormat, err := parseOutputFormat(p.GetOutputFormat())
	if err != nil {
		return err
	}
	quality, err := parseGRPCQuality(p.GetQuality())
	if err != nil {
		return err
	}
	shape, err := parseShape(p.GetShape())
	if err != nil {
		return err
	}

	frame, err := s.firstFrame(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return err
	}
	out, err := s.imageThumbnail(s.sem, frame, width, height, outputFormat, quality, shape, "center")
	if err != nil {
		slog.Error("GetVideoThumbnail: render", "err", err)
		return toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return streamBlob(stream, contentTypeForFormat(outputFormat), out)
}
