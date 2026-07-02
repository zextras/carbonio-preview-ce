// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package video extracts the first frame of a video as a still image (PNG),
// so it can be fed to the existing libvips image pipeline (render.ImageThumbnail)
// exactly like a photo. Extraction shells out to ffmpeg; the input is always
// written to a SEEKABLE temp file first, because moov-at-end MP4 (the common
// phone/screen-recording layout) cannot be decoded from a non-seekable pipe.
package video

var (
	// FFmpegPath is the ffmpeg executable. It is a fixed packaging invariant,
	// NOT a config key: the Zextras-published "carbonio-ffmpeg" package always
	// installs the binary at /opt/zextras/common/bin/ffmpeg, mirroring the
	// convention used by carbonio-videoserver / carbonio-videorecorder. It is
	// not exposed as a networking- or application-config key on purpose — a
	// path to a vendored binary fits neither category. If packaging ever moves
	// the binary, change this constant (and the carbonio-ffmpeg dependency).
	//
	// Concurrency is bounded by the caller (the generate handler's dedicated
	// video-semaphore middleware), not here — see ExtractFirstFramePNG.
	FFmpegPath = "/opt/zextras/common/bin/ffmpeg"
)
