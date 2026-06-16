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

// documentDisabledErr returns 400 INVALID_ARGUMENT when a document endpoint is
// disabled via config. Mirrors REST errBadRequest (HTTP 400).
func documentDisabledErr(msg string) error {
	return toStatus(http.StatusBadRequest, msg)
}

// GetDocumentPreview implements PreviewService.GetDocumentPreview.
// Config-gated by ServiceEnableDocumentPreview (mirrors REST behaviour).
func (s *PreviewServer) GetDocumentPreview(req *pb.GetRequest, stream pb.PreviewService_GetDocumentPreviewServer) error {
	if !s.cfg.ServiceEnableDocumentPreview {
		return documentDisabledErr(config.Msg.DocumentPreviewDisabled)
	}

	p := req.GetParams()
	id, version, serviceType, err := parseGetParams(p)
	if err != nil {
		return err
	}

	firstPage, lastPage, err := parseGRPCPages(p.GetFirstPage(), p.GetLastPage())
	if err != nil {
		return err
	}
	langTag := parseGRPCLangTag(p.GetLangTag())

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return storageErr(err)
	}

	pdfBytes, err := s.docToPDFWithLang(stream.Context(), blob, langTag)
	if err != nil {
		slog.Error("GetDocumentPreview: convert", "err", err)
		return toStatus(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}

	sliced, err := s.pdfSlice(s.sem, pdfBytes, firstPage, lastPage)
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
		return documentDisabledErr(config.Msg.DocumentThumbnailDisabled)
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
	langTag := parseGRPCLangTag(p.GetLangTag())

	blob, err := s.store.RetrieveData(stream.Context(), id, version, serviceType, p.GetOwnerId())
	if err != nil {
		return storageErr(err)
	}

	pdfBytes, err := s.docToPDFWithLang(stream.Context(), blob, langTag)
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
		return documentDisabledErr(config.Msg.DocumentPreviewDisabled)
	}

	result, params, err := recvUpload(stream, s.cfg.UploadMemoryThresholdBytes, s.cfg.UploadMaxBytes)
	if err != nil {
		return err
	}
	defer result.Cleanup()
	blob, err := result.Bytes()
	if err != nil {
		return toStatus(http.StatusInternalServerError, "read upload: internal error")
	}
	firstPage, lastPage, err := parseGRPCPages(params.GetFirstPage(), params.GetLastPage())
	if err != nil {
		return err
	}
	langTag := parseGRPCLangTag(params.GetLangTag())

	pdfBytes, err := s.docToPDFWithLang(stream.Context(), blob, langTag)
	if err != nil {
		slog.Error("PostDocumentPreview: convert", "err", err)
		return toStatus(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}

	sliced, err := s.pdfSlice(s.sem, pdfBytes, firstPage, lastPage)
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
		return documentDisabledErr(config.Msg.DocumentThumbnailDisabled)
	}

	result, params, err := recvUpload(stream, s.cfg.UploadMemoryThresholdBytes, s.cfg.UploadMaxBytes)
	if err != nil {
		return err
	}
	defer result.Cleanup()
	blob, err := result.Bytes()
	if err != nil {
		return toStatus(http.StatusInternalServerError, "read upload: internal error")
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
	langTag := parseGRPCLangTag(params.GetLangTag())

	pdfBytes, err := s.docToPDFWithLang(stream.Context(), blob, langTag)
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

// docToPDFWithLang calls collaboraConvert to turn document bytes into PDF,
// using the provided langTag (mirrors REST convertDocToPDF which receives
// langTag from parseLangTag). Empty langTag is normalised to "en-US" by the
// caller via parseGRPCLangTag before this function is called.
func (s *PreviewServer) docToPDFWithLang(ctx context.Context, data []byte, langTag string) ([]byte, error) {
	docsTimeout := time.Duration(s.cfg.ServiceDocsTimeout) * time.Second
	docsURL := s.cfg.DocumentConversionFullConvertAddress + "/pdf"
	return s.collaboraConvert(ctx, data, langTag, docsURL, docsTimeout)
}
