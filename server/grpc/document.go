// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/render"
)

// GetDocumentPreview implements PreviewService.GetDocumentPreview.
// Config-gated by ServiceEnableDocumentPreview (mirrors REST behaviour).
func (s *PreviewServer) GetDocumentPreview(req *pb.GetRequest, stream pb.PreviewService_GetDocumentPreviewServer) error {
	if !s.cfg.ServiceEnableDocumentPreview {
		return status.Error(codes.FailedPrecondition, config.Msg.DocumentPreviewDisabled)
	}

	p := req.GetParams()
	id, version, serviceType, err := parseGetParams(p)
	if err != nil {
		return err
	}

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, "")
	if err != nil {
		return storageErr(err)
	}

	pdfBytes, err := s.docToPDF(stream.Context(), blob)
	if err != nil {
		slog.Error("GetDocumentPreview: convert", "err", err)
		return toStatus(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}

	sliced, err := s.pdfSlice(s.sem, pdfBytes, 1, 0)
	if err != nil {
		slog.Error("GetDocumentPreview: PDFSlice", "err", err)
		if errors.Is(err, render.ErrRenderUnavailable) {
			return status.Errorf(codes.Unavailable, "PDF rendering temporarily unavailable")
		}
		return toStatus(http.StatusBadRequest, config.Msg.InputError)
	}

	return streamBlob(stream, "application/pdf", sliced)
}

// GetDocumentThumbnail implements PreviewService.GetDocumentThumbnail.
// Config-gated by ServiceEnableDocumentThumbnail (mirrors REST behaviour).
func (s *PreviewServer) GetDocumentThumbnail(req *pb.GetRequest, stream pb.PreviewService_GetDocumentThumbnailServer) error {
	if !s.cfg.ServiceEnableDocumentThumbnail {
		return status.Error(codes.FailedPrecondition, config.Msg.DocumentThumbnailDisabled)
	}

	p := req.GetParams()
	id, version, serviceType, err := parseGetParams(p)
	if err != nil {
		return err
	}
	width, height, err := parseGRPCArea(p.GetArea())
	if err != nil {
		return err
	}
	outputFormat := defaultOutputFormat(p.GetOutputFormat())
	quality := defaultQuality(p.GetQuality())
	shape := defaultShape(p.GetShape())

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, "")
	if err != nil {
		return storageErr(err)
	}

	pdfBytes, err := s.docToPDF(stream.Context(), blob)
	if err != nil {
		slog.Error("GetDocumentThumbnail: convert", "err", err)
		return toStatus(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}

	out, ct, err := s.renderPDFThumbnail(pdfBytes, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		return err
	}
	return streamBlob(stream, ct, out)
}

// PostDocumentPreview implements PreviewService.PostDocumentPreview.
// Config-gated by ServiceEnableDocumentPreview.
func (s *PreviewServer) PostDocumentPreview(stream pb.PreviewService_PostDocumentPreviewServer) error {
	if !s.cfg.ServiceEnableDocumentPreview {
		return status.Error(codes.FailedPrecondition, config.Msg.DocumentPreviewDisabled)
	}

	_, blob, err := recvUpload(stream)
	if err != nil {
		return err
	}

	pdfBytes, err := s.docToPDF(stream.Context(), blob)
	if err != nil {
		slog.Error("PostDocumentPreview: convert", "err", err)
		return toStatus(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}

	sliced, err := s.pdfSlice(s.sem, pdfBytes, 1, 0)
	if err != nil {
		slog.Error("PostDocumentPreview: PDFSlice", "err", err)
		if errors.Is(err, render.ErrRenderUnavailable) {
			return status.Errorf(codes.Unavailable, "PDF rendering temporarily unavailable")
		}
		return toStatus(http.StatusBadRequest, config.Msg.InputError)
	}

	return streamBlob(stream, "application/pdf", sliced)
}

// PostDocumentThumbnail implements PreviewService.PostDocumentThumbnail.
// Config-gated by ServiceEnableDocumentThumbnail.
func (s *PreviewServer) PostDocumentThumbnail(stream pb.PreviewService_PostDocumentThumbnailServer) error {
	if !s.cfg.ServiceEnableDocumentThumbnail {
		return status.Error(codes.FailedPrecondition, config.Msg.DocumentThumbnailDisabled)
	}

	params, blob, err := recvUpload(stream)
	if err != nil {
		return err
	}
	width, height, err := parseGRPCArea(params.GetArea())
	if err != nil {
		return err
	}
	outputFormat := defaultOutputFormat(params.GetOutputFormat())
	quality := defaultQuality(params.GetQuality())
	shape := defaultShape(params.GetShape())

	pdfBytes, err := s.docToPDF(stream.Context(), blob)
	if err != nil {
		slog.Error("PostDocumentThumbnail: convert", "err", err)
		return toStatus(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}

	out, ct, err := s.renderPDFThumbnail(pdfBytes, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		return err
	}
	return streamBlob(stream, ct, out)
}

// docToPDF calls collaboraConvert to turn document bytes into PDF.
// Mirrors convertDocToPDF in the REST server package.
func (s *PreviewServer) docToPDF(ctx context.Context, data []byte) ([]byte, error) {
	docsTimeout := time.Duration(s.cfg.ServiceDocsTimeout) * time.Second
	docsURL := s.cfg.DocumentConversionFullConvertAddress + "/pdf"
	return s.collaboraConvert(ctx, data, "en-US", docsURL, docsTimeout)
}
