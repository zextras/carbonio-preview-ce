// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/cache"
	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type runner struct {
	l      *limits
	wait   context.Context
	work   context.Context
	flight bool
	op     string
}

func (l *limits) forCaller(ctx context.Context, op string) runner {
	return runner{l: l, wait: ctx, work: ctx, op: op}
}

func served(e cache.Entry, err error) (*apispec.BinOut, error) {
	if err != nil {
		return nil, err
	}
	return &apispec.BinOut{ContentType: e.ContentType, Body: e.Body}, nil
}

func (r runner) slot(s slots) error {
	start := time.Now()
	if err := r.l.acquire(r.wait, s, r.flight); err != nil {
		if !r.flight {
			slog.Debug("caller left while waiting for a slot",
				"operation", r.op, "waited_ms", time.Since(start).Milliseconds())
		}
		return errServerBusy()
	}
	return nil
}

func (r runner) fetch(store storage.Client, id string, version int, serviceType, ownerID string) ([]byte, error) {
	data, err := store.RetrieveData(r.work, id, version, serviceType, ownerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, huma.NewError(http.StatusNotFound, config.Msg.ItemNotFound)
		}
		return nil, huma.NewError(http.StatusUnprocessableEntity, config.Msg.GenericErrorStorage)
	}
	return data, nil
}

func (r runner) printfRenderErr(err error) {
	log.Printf("%s render: %v", r.op, err)
}

func (r runner) slogRenderErr(err error) {
	slog.Warn(r.op+" render", "err", err)
}

func (r runner) image(logErr func(error), data []byte, width, height int, outputFormat, quality, shape, cropMode string) ([]byte, error) {
	if err := r.slot(r.l.render); err != nil {
		return nil, err
	}
	defer r.l.render.release()
	out, err := imageThumbnailFunc(data, width, height, outputFormat, quality, shape, cropMode)
	if err != nil {
		logErr(err)
		return nil, huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	return out, nil
}

func (r runner) pdfSlice(data []byte, firstPage, lastPage int) ([]byte, error) {
	if err := r.slot(r.l.render); err != nil {
		return nil, err
	}
	defer r.l.render.release()
	out, err := pdfSliceFunc(r.wait, data, firstPage, lastPage)
	if err != nil {
		log.Printf("%s: PDFSlice: %v", r.op, err)
		return nil, pdfRenderError(err)
	}
	return out, nil
}

func (r runner) pdfRasterize(data []byte, width, height int, outputFormat, quality, shape string) ([]byte, error) {
	if err := r.slot(r.l.render); err != nil {
		return nil, err
	}
	defer r.l.render.release()
	out, err := pdfRasterizeFunc(r.wait, data, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		log.Printf("%s: PDFRasterize: %v", r.op, err)
		return nil, pdfRenderError(err)
	}
	return out, nil
}

func pdfRenderError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errServerBusy()
	case errors.Is(err, render.ErrRenderUnavailable):
		return huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
	}
	return huma.NewError(http.StatusBadRequest, config.Msg.InputError)
}

func (r runner) convert(cfg *config.Config, data []byte, langTag string) ([]byte, error) {
	docsTimeout := time.Duration(cfg.ServiceDocsTimeout) * time.Second
	docsURL := cfg.DocumentConversionFullConvertAddress + "/pdf"
	pdfBytes, err := collaboraConvertFunc(r.work, data, langTag, docsURL, docsTimeout)
	if err != nil {
		log.Printf("%s: convert: %v", r.op, err)
		return nil, huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}
	return pdfBytes, nil
}
