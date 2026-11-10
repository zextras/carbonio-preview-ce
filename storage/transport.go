// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	dialTimeout     = 5 * time.Second
	idleConnTimeout = 90 * time.Second
)

var errInactive = errors.New("inactivity timeout")

func NewTransport() *http.Transport {
	return &http.Transport{
		DialContext:     (&net.Dialer{Timeout: dialTimeout}).DialContext,
		IdleConnTimeout: idleConnTimeout,
	}
}

// DoWithInactivity sends req and cancels it when no request or response bytes move for idle; close the returned body.
func DoWithInactivity(client *http.Client, req *http.Request, idle time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(idle, func() { cancel(errInactive) })
	fail := func(err error) error {
		timer.Stop()
		inactive := errors.Is(context.Cause(ctx), errInactive)
		cancel(nil)
		if inactive {
			return fmt.Errorf("%w: no data for %s", ErrUnavailable, idle)
		}
		return err
	}

	req = req.WithContext(ctx)
	if req.Body != nil && req.Body != http.NoBody {
		req.Body = &progressBody{ReadCloser: req.Body, timer: timer, idle: idle}
		if gb := req.GetBody; gb != nil {
			req.GetBody = func() (io.ReadCloser, error) {
				b, err := gb()
				if err != nil {
					return nil, err
				}
				return &progressBody{ReadCloser: b, timer: timer, idle: idle}, nil
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fail(err)
	}
	timer.Reset(idle)
	resp.Body = &inactivityBody{ReadCloser: resp.Body, timer: timer, idle: idle, ctx: ctx, cancel: cancel}
	return resp, nil
}

type progressBody struct {
	io.ReadCloser
	timer *time.Timer
	idle  time.Duration
}

func (b *progressBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}

type inactivityBody struct {
	io.ReadCloser
	timer  *time.Timer
	idle   time.Duration
	ctx    context.Context
	cancel context.CancelCauseFunc
	once   sync.Once
}

func (b *inactivityBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	if err != nil && err != io.EOF && errors.Is(context.Cause(b.ctx), errInactive) {
		return n, fmt.Errorf("%w: no data for %s", ErrUnavailable, b.idle)
	}
	return n, err
}

func (b *inactivityBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		b.timer.Stop()
		b.cancel(nil)
	})
	return err
}
