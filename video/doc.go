// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package video extracts the first frame of a video as a still image (PNG),
// so it can be fed to the existing libvips image pipeline (render.ImageThumbnail)
// exactly like a photo. Extraction shells out to ffmpeg; the input is always
// written to a SEEKABLE temp file first, because moov-at-end MP4 (the common
// phone/screen-recording layout) cannot be decoded from a non-seekable pipe.
package video

import (
	"runtime"
	"time"
)

// Tunables. main() may override these from config after config.Load(),
// mirroring render.ImageMinRes.
var (
	// FFmpegPath is the ffmpeg executable. Default mirrors the Carbonio
	// convention used by carbonio-videoserver / carbonio-videorecorder: the
	// Zextras-published "carbonio-ffmpeg" package installs the binary at
	// /opt/zextras/common/bin/ffmpeg. Overridable from config in main().
	FFmpegPath = "/opt/zextras/common/bin/ffmpeg"
	// MaxBytes caps how many bytes we stream to the temp file before rejecting.
	// Videos larger than this are not previewed (ErrTooLarge).
	MaxBytes int64 = 100 << 20 // 100 MiB
	// Timeout bounds a single ffmpeg invocation.
	Timeout = 20 * time.Second
	// MaxConcurrent bounds the number of ffmpeg subprocesses that may run
	// simultaneously. Each video request forks one ffmpeg process before the
	// libvips render semaphore is acquired, so without this cap a burst of
	// requests can exhaust file-descriptors and RAM. main() may override this
	// before the first call to ExtractFirstFramePNG.
	MaxConcurrent = runtime.NumCPU()
)
