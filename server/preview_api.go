// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

func previewPipeline(deps Deps, mimeType string) (pipeline, error) {
	p, ok := pipelineFor(mimeType)
	if !ok {
		return pipeline{}, errFormatNotSupported()
	}
	if p.kind == kindOffice && !deps.Cfg.ServiceEnableDocumentPreview {
		return pipeline{}, huma.NewError(http.StatusBadRequest, config.Msg.DocumentPreviewDisabled)
	}
	return p, nil
}

func sourceStatusError(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
	}
	return huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
}

func buildGetPreview(deps Deps) func(context.Context, *apispec.PreviewGetInput) (*apispec.PreviewOutput, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, in *apispec.PreviewGetInput) (*apispec.PreviewOutput, error) {
		p, err := previewPipeline(deps, in.Mime)
		if err != nil {
			return nil, err
		}
		id, err := validateUUID(in.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnprocessableEntity, "Validation Error",
				&huma.ErrorDetail{Message: config.Msg.IDNotValid, Location: "path.id", Value: in.ID})
		}
		src, size, err := openSource(ctx, deps.Store, id, in.Version, string(in.ServiceType), in.FileOwnerID, time.Duration(deps.Cfg.ServiceTimeoutInSeconds)*time.Second)
		if err != nil {
			return nil, sourceStatusError(err)
		}
		if p.native {
			br := bufio.NewReader(src)
			if _, err := br.Peek(1); err != nil && !errors.Is(err, io.EOF) {
				_ = src.Close()
				return nil, sourceStatusError(err)
			}
			return passthrough(p.mime, struct {
				io.Reader
				io.Closer
			}{br, src}, size), nil
		}
		defer src.Close()
		data, err := io.ReadAll(src)
		if err != nil {
			return nil, sourceStatusError(err)
		}
		return convertPreview(lim.forCaller(ctx, "getPreview"), deps, p, data, in.Lang)
	}
}

func buildPostPreview(deps Deps) func(context.Context, *apispec.PreviewPostInput) (*apispec.PreviewOutput, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, in *apispec.PreviewPostInput) (*apispec.PreviewOutput, error) {
		p, err := previewPipeline(deps, in.Mime)
		if err != nil {
			return nil, err
		}
		part, err := uploadPart(in.RawBody.Data())
		if err != nil {
			return nil, err
		}
		if p.native {
			return passthrough(p.mime, part, part.Size), nil
		}
		defer part.Close()
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, errFileNotValid()
		}
		return convertPreview(lim.forCaller(ctx, "postPreview"), deps, p, data, in.Lang)
	}
}

func passthrough(contentType string, body io.ReadCloser, size int64) *apispec.PreviewOutput {
	out := &apispec.PreviewOutput{ContentType: contentType}
	if size >= 0 {
		out.ContentLength = strconv.FormatInt(size, 10)
	}
	out.Body = func(c huma.Context) {
		defer body.Close()
		c.SetStatus(http.StatusOK)
		if _, err := io.Copy(c.BodyWriter(), body); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	return out
}

func convertPreview(step runner, deps Deps, p pipeline, data []byte, lang string) (*apispec.PreviewOutput, error) {
	contentType, body := mimePDF, []byte(nil)
	if p.kind == kindOffice {
		pdf, err := step.convert(deps.Cfg, data, lang, "pdf")
		if err != nil {
			return nil, err
		}
		body = pdf
	} else {
		r, err := step.fullImage(data)
		if err != nil {
			return nil, err
		}
		contentType, body = r.ContentType, r.Body
	}
	return &apispec.PreviewOutput{
		ContentType:   contentType,
		ContentLength: strconv.Itoa(len(body)),
		Body: func(c huma.Context) {
			c.SetStatus(http.StatusOK)
			_, _ = c.BodyWriter().Write(body)
		},
	}, nil
}
