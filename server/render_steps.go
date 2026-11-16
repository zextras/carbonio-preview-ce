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

	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/render"
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

func pdfRenderError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errServerBusy()
	case errors.Is(err, render.ErrRenderUnavailable):
		return huma.NewError(http.StatusServiceUnavailable, "PDF rendering temporarily unavailable")
	}
	return huma.NewError(http.StatusBadRequest, config.Msg.InputError)
}

func (r runner) convert(cfg *config.Config, data []byte, langTag, ext string) ([]byte, error) {
	docsIdle := time.Duration(cfg.ServiceDocsTimeout) * time.Second
	docsURL := cfg.DocumentConversionFullConvertAddress + "/" + ext
	converted, err := collaboraConvertFunc(r.work, data, langTag, docsURL, docsIdle)
	if err != nil {
		log.Printf("%s: convert: %v", r.op, err)
		return nil, huma.NewError(http.StatusBadGateway, config.Msg.StorageUnavailable)
	}
	return converted, nil
}
