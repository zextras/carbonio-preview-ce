// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package render implements the image/PDF/document processing pipeline.
// It wraps libvips (via CGO) for image transformations and go-pdfium for PDF
// rendering. The benchmark reference is
// github.com/zextras/preview-bench-go-vips/main.go which this package is
// derived from.
package render

/*
#cgo pkg-config: vips
#include <vips/vips.h>
*/
import "C"
import "fmt"

// InitVips initialises the libvips library and disables the operation cache to
// prevent unbounded RAM growth under concurrent load. It also applies
// VIPS_CONCURRENCY if set. Call once at process startup, before any other
// render function.
//
// appName is used only for vips internal diagnostics (e.g. "carbonio-preview").
func InitVips(appName string) error {
	cName := C.CString(appName)
	// Note: C.CString allocates; libvips keeps it for the lifetime of the
	// process so we deliberately do not free it.
	if rc := C.vips_init(cName); rc != 0 {
		return fmt.Errorf("vips_init failed: %d", int(rc))
	}
	DisableVipsCache()
	return nil
}

// DisableVipsCache turns off the libvips operation cache entirely.
// Without this, libvips caches every decoded tile in RAM and the process
// grows without bound under sustained traffic. Safe to call multiple times.
func DisableVipsCache() {
	C.vips_cache_set_max(0)
	C.vips_cache_set_max_mem(0)
}

// SetVipsConcurrency overrides the number of threads each VipsOperation may
// use. Passing 0 or a negative value is a no-op.
func SetVipsConcurrency(n int) {
	if n > 0 {
		C.vips_concurrency_set(C.int(n))
	}
}
