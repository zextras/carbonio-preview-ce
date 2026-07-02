// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"strings"

	"github.com/zextras/carbonio-preview-ce/v2/video"
)

// supportedVideoCodecs is the hardcoded set of codecs that carbonio-ffmpeg can
// decode to produce a preview frame. Adding a codec here is a deliberate feature
// change that requires end-to-end testing with the actual binary — it is NOT an
// operator configuration option.
var supportedVideoCodecs = map[string]struct{}{
	"h264":  {},
	"hevc":  {},
	"h265":  {}, // alias for hevc, reported by some containers
	"vp8":   {},
	"vp9":   {},
	"mpeg4": {},
	"mpeg2video": {},
	"theora": {},
}

// isSupportedVideoCodec reports whether codec (lowercased) is in the supported set.
func isSupportedVideoCodec(codec string) bool {
	_, ok := supportedVideoCodecs[strings.ToLower(codec)]
	return ok
}

// videoDetectCodecFunc is the seam for video.DetectCodec. Tests override it to
// avoid spawning ffprobe/ffmpeg; production uses the real detector.
var videoDetectCodecFunc = func(ctx context.Context, r io.Reader) (string, error) {
	return video.DetectCodec(ctx, r)
}
