// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command carbonio-preview-pdfium-worker is the PDFium subprocess worker binary
// used by the carbonio-preview-ce service.
//
// It is managed by the klippa go-pdfium multi_threaded pool: the pool spawns and
// supervises N copies of this binary, each owning a single PDFium instance and
// communicating with the parent process over stdio via the hashicorp/go-plugin
// RPC protocol. If a worker crashes, the pool automatically respawns a replacement.
//
// Build (requires CGO_ENABLED=1 and libpdfium.so reachable via pkg-config):
//
//	CGO_ENABLED=1 PKG_CONFIG_PATH=/usr/local/lib/pkgconfig \
//	  go build -o carbonio-preview-pdfium-worker ./cmd/pdfium-worker
package main

import "github.com/klippa-app/go-pdfium/multi_threaded/worker"

func main() {
	worker.StartWorker(nil)
}
