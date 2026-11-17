// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v4/config"
	"github.com/zextras/carbonio-preview-ce/v4/render"
	"github.com/zextras/carbonio-preview-ce/v4/server/apispec"
	"github.com/zextras/carbonio-preview-ce/v4/video"
)

func errFileNotValid() error {
	return huma.NewError(http.StatusUnprocessableEntity, config.Msg.FileNotValid,
		&huma.ErrorDetail{Message: config.Msg.FileNotValid, Location: "body.file"})
}

func uploadPart(data *apispec.UploadFiles) (*huma.FormFile, error) {
	if data == nil || !data.File.IsSet || data.File.Size == 0 {
		return nil, errFileNotValid()
	}
	return &data.File, nil
}

func buildPostThumbnail(deps Deps) func(context.Context, *apispec.ThumbnailPostInput) (*apispec.ThumbnailOutput, error) {
	lim := deps.slotLimits()
	return func(ctx context.Context, in *apispec.ThumbnailPostInput) (*apispec.ThumbnailOutput, error) {
		p, ok := pipelineFor(in.Mime)
		if !ok || !p.thumbnail {
			return nil, errFormatNotSupported()
		}
		if p.kind == kindOffice && deps.Cfg != nil && !deps.Cfg.ServiceEnableDocumentThumbnail {
			return nil, huma.NewError(http.StatusBadRequest, config.Msg.DocumentThumbnailDisabled)
		}
		part, err := uploadPart(in.RawBody.Data())
		if err != nil {
			return nil, err
		}
		defer part.Close()

		step := lim.forCaller(ctx, "postThumbnail")
		size := string(in.Size)
		var out produced
		if p.kind == kindVideo {
			out, err = postVideo(step, deps, part, size)
		} else {
			var data []byte
			if data, err = io.ReadAll(part); err != nil {
				return nil, errFileNotValid()
			}
			out, err = postSource(step, deps, p, data, size, in.Lang)
		}
		if err != nil {
			return nil, err
		}
		return thumbnailBytes(http.StatusOK, out.contentType, out.body), nil
	}
}

func postSource(step runner, deps Deps, p pipeline, data []byte, size, lang string) (produced, error) {
	switch p.kind {
	case kindPDF:
		return step.pdfThumb(data, size)
	case kindOffice:
		png, err := step.convert(deps.Cfg, data, lang, "png")
		if err != nil {
			return produced{}, err
		}
		return step.imageThumb(png, size)
	}
	info, err := step.imageInfo(p, data)
	if err != nil {
		return produced{}, err
	}
	if isAnimatedGIF(p, info) {
		return postAnimatedGIF(step, deps, data, info, size)
	}
	return step.staticThumb(p, data, info, size)
}

func postAnimatedGIF(step runner, deps Deps, data []byte, info render.Info, size string) (produced, error) {
	if longSide(info) <= sideOf(size) {
		return produced{body: data, contentType: mimeGIF, original: true}, nil
	}
	out, err := resizeGIFUnderSlot(step, deps, data, info, size)
	switch {
	case err == nil:
		return produced{body: out, contentType: mimeGIF}, nil
	case step.wait.Err() != nil:
		return produced{}, errServerBusy()
	}
	slog.Warn("postThumbnail: animated GIF resize failed, serving the static frame", "err", err)
	return step.imageThumb(data, size)
}

func resizeGIFUnderSlot(step runner, deps Deps, data []byte, info render.Info, size string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "carbonio-preview-gif-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.gif")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return nil, err
	}
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		return nil, err
	}
	sem := slots(deps.AsyncSem)
	if err := step.slot(sem); err != nil {
		return nil, err
	}
	defer sem.release()
	ctx, cancel := context.WithTimeout(step.wait, extractCeiling)
	defer cancel()
	return animatedGIFFunc(ctx, src, info, sideOf(size), work)
}

func postVideo(step runner, deps Deps, part *huma.FormFile, size string) (produced, error) {
	tmp, err := os.CreateTemp("", "carbonio-preview-post-*.bin")
	if err != nil {
		return produced{}, huma.NewError(http.StatusInternalServerError, "internal error")
	}
	defer os.Remove(tmp.Name())
	_, copyErr := video.StreamToSparseTemp(tmp, part)
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return produced{}, errFileNotValid()
	}
	frame, err := postVideoFrame(step, deps, tmp.Name())
	if err != nil {
		return produced{}, err
	}
	return step.imageThumb(frame, size)
}

func postVideoFrame(step runner, deps Deps, path string) ([]byte, error) {
	sem := slots(deps.AsyncSem)
	if err := step.slot(sem); err != nil {
		return nil, err
	}
	defer sem.release()
	ctx, cancel := context.WithTimeout(step.wait, extractCeiling)
	defer cancel()
	fail := func(err error) error {
		if step.wait.Err() != nil {
			return errServerBusy()
		}
		slog.Warn("postThumbnail: video frame failed", "err", err)
		return huma.NewError(http.StatusBadRequest, config.Msg.FormatNotSupported)
	}
	codec, err := videoDetectCodecFromFileFunc(ctx, path)
	if err != nil {
		return nil, fail(err)
	}
	if !isSupportedVideoCodec(codec) {
		return nil, errFormatNotSupported()
	}
	frame, err := videoFirstFrameFromFileFunc(ctx, path)
	if err != nil {
		return nil, fail(err)
	}
	return frame, nil
}
