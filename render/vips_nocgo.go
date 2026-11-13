// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !cgo

package render

import (
	"errors"
	"image"
)

var errVipsUnavailable = errors.New("libvips unavailable: built without cgo")

func InitVips(string) error { return errVipsUnavailable }

func DisableVipsCache() {}

func SetVipsConcurrency(int) {}

func ImageThumbnail([]byte, int, int, string, string, string, string) ([]byte, error) {
	return nil, errVipsUnavailable
}

func coverCropVipsRGBA(*image.RGBA, int, int, string, int) ([]byte, error) {
	return nil, errVipsUnavailable
}

var roundedMaskFunc = func([]byte, int, int) ([]byte, error) { return nil, errVipsUnavailable }
