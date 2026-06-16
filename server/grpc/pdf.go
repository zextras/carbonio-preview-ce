// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"errors"
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/render"
)

// GetPdfPreview implements PreviewService.GetPdfPreview.
// Returns sliced PDF bytes (first_page..last_page) as a stream.
// first_page 0 → 1; last_page 0 → to end; mirrors REST parsePages defaults.
func (s *PreviewServer) GetPdfPreview(req *pb.GetRequest, stream pb.PreviewService_GetPdfPreviewServer) error {
	p := req.GetParams()
	id, version, serviceType, err := parseGetParams(p)
	if err != nil {
		return err
	}
	firstPage, lastPage, err := parseGRPCPages(p.GetFirstPage(), p.GetLastPage())
	if err != nil {
		return err
	}

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return storageErr(err)
	}

	sliced, err := s.pdfSlice(s.sem, blob, firstPage, lastPage)
	if err != nil {
		slog.Error("GetPdfPreview: PDFSlice", "err", err)
		if errors.Is(err, render.ErrRenderUnavailable) {
			return status.Errorf(codes.Unavailable, "PDF rendering temporarily unavailable")
		}
		return toStatus(http.StatusBadRequest, config.Msg.InputError)
	}

	return streamBlob(stream, "application/pdf", sliced)
}

// GetPdfThumbnail implements PreviewService.GetPdfThumbnail.
// Rasterizes page 0 (first page) of the PDF to an image.
func (s *PreviewServer) GetPdfThumbnail(req *pb.GetRequest, stream pb.PreviewService_GetPdfThumbnailServer) error {
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

	out, ct, err := s.renderPDFThumbnail(blob, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		return err
	}
	return streamBlob(stream, ct, out)
}

// PostPdfPreview implements PreviewService.PostPdfPreview.
func (s *PreviewServer) PostPdfPreview(stream pb.PreviewService_PostPdfPreviewServer) error {
	params, blob, err := recvUpload(stream)
	if err != nil {
		return err
	}
	firstPage, lastPage, err := parseGRPCPages(params.GetFirstPage(), params.GetLastPage())
	if err != nil {
		return err
	}

	sliced, err := s.pdfSlice(s.sem, blob, firstPage, lastPage)
	if err != nil {
		slog.Error("PostPdfPreview: PDFSlice", "err", err)
		if errors.Is(err, render.ErrRenderUnavailable) {
			return status.Errorf(codes.Unavailable, "PDF rendering temporarily unavailable")
		}
		return toStatus(http.StatusBadRequest, config.Msg.InputError)
	}

	return streamBlob(stream, "application/pdf", sliced)
}

// PostPdfThumbnail implements PreviewService.PostPdfThumbnail.
func (s *PreviewServer) PostPdfThumbnail(stream pb.PreviewService_PostPdfThumbnailServer) error {
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

	out, ct, err := s.renderPDFThumbnail(blob, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		return err
	}
	return streamBlob(stream, ct, out)
}

// renderPDFThumbnail is the shared PDF rasterization logic used by both the
// GET and POST thumbnail handlers. It mirrors renderPDFThumbnail in pdf.go
// (REST package) but returns (blob, contentType, error) instead of writing to
// http.ResponseWriter, making it transport-agnostic.
func (s *PreviewServer) renderPDFThumbnail(
	data []byte,
	page, width, height int,
	outputFormat, quality, shape string,
) ([]byte, string, error) {
	out, err := s.pdfRasterize(s.sem, data, page, width, height, outputFormat, quality, shape)
	if err != nil {
		slog.Error("renderPDFThumbnail: PDFRasterize", "err", err)
		if errors.Is(err, render.ErrRenderUnavailable) {
			return nil, "", status.Errorf(codes.Unavailable, "PDF rendering temporarily unavailable")
		}
		return nil, "", toStatus(http.StatusBadRequest, config.Msg.InputError)
	}

	// Rounded shape forces PNG output (mirrors REST renderPDFThumbnail).
	actualFormat := outputFormat
	if shape == "rounded" {
		actualFormat = "png"
	}
	ct := contentTypeForFormat(actualFormat)
	return out, ct, nil
}
