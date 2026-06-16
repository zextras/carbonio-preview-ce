// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"log/slog"
	"net/http"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
)

// GetImagePreview implements PreviewService.GetImagePreview.
func (s *PreviewServer) GetImagePreview(req *pb.GetRequest, stream pb.PreviewService_GetImagePreviewServer) error {
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
	// crop=true → center/cover, crop=false → scale-to-fit; mirrors REST parseCrop.
	cropMode := parseCropMode(p.GetCrop())

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return storageErr(err)
	}

	out, err := s.imageThumbnail(s.sem, blob, width, height, outputFormat, quality, "rectangular", cropMode)
	if err != nil {
		slog.Error("GetImagePreview: render", "err", err)
		return toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}

	return streamBlob(stream, contentTypeForFormat(outputFormat), out)
}

// GetImageThumbnail implements PreviewService.GetImageThumbnail.
func (s *PreviewServer) GetImageThumbnail(req *pb.GetRequest, stream pb.PreviewService_GetImageThumbnailServer) error {
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

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return storageErr(err)
	}

	// Image thumbnails always use CENTER crop (per Python spec).
	out, err := s.imageThumbnail(s.sem, blob, width, height, outputFormat, quality, shape, "center")
	if err != nil {
		slog.Error("GetImageThumbnail: render", "err", err)
		return toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}

	return streamBlob(stream, contentTypeForFormat(outputFormat), out)
}

// PostImagePreview implements PreviewService.PostImagePreview.
func (s *PreviewServer) PostImagePreview(stream pb.PreviewService_PostImagePreviewServer) error {
	params, blob, err := recvUpload(stream)
	if err != nil {
		return err
	}
	width, height, err := parseGRPCArea(params.GetArea())
	if err != nil {
		return err
	}
	outputFormat, err := parseOutputFormat(params.GetOutputFormat())
	if err != nil {
		return err
	}
	quality, err := parseGRPCQuality(params.GetQuality())
	if err != nil {
		return err
	}
	// crop=true → center/cover, crop=false → scale-to-fit; mirrors REST parseCrop.
	cropMode := parseCropMode(params.GetCrop())

	out, err := s.imageThumbnail(s.sem, blob, width, height, outputFormat, quality, "rectangular", cropMode)
	if err != nil {
		slog.Error("PostImagePreview: render", "err", err)
		return toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}

	return streamBlob(stream, contentTypeForFormat(outputFormat), out)
}

// PostImageThumbnail implements PreviewService.PostImageThumbnail.
func (s *PreviewServer) PostImageThumbnail(stream pb.PreviewService_PostImageThumbnailServer) error {
	params, blob, err := recvUpload(stream)
	if err != nil {
		return err
	}
	width, height, err := parseGRPCArea(params.GetArea())
	if err != nil {
		return err
	}
	outputFormat, err := parseOutputFormat(params.GetOutputFormat())
	if err != nil {
		return err
	}
	quality, err := parseGRPCQuality(params.GetQuality())
	if err != nil {
		return err
	}
	shape, err := parseShape(params.GetShape())
	if err != nil {
		return err
	}

	// Image thumbnails always use CENTER crop.
	out, err := s.imageThumbnail(s.sem, blob, width, height, outputFormat, quality, shape, "center")
	if err != nil {
		slog.Error("PostImageThumbnail: render", "err", err)
		return toStatus(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}

	return streamBlob(stream, contentTypeForFormat(outputFormat), out)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// contentTypeForFormat mirrors server.contentTypeForFormat for use in the grpc package.
func contentTypeForFormat(f string) string {
	switch f {
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}
