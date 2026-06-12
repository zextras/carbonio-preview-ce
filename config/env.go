// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
)

// EnvKnobs holds per-instance performance tuning values read from PREVIEW_*
// environment variables. These are framework-level knobs (equivalent to
// QUARKUS_* in Quarkus services) and are intentionally outside the extensions
// networking/application config chain.
type EnvKnobs struct {
	// StoragesTimeout is the HTTP timeout in seconds for carbonio-storages requests.
	// Controlled by PREVIEW_STORAGES_TIMEOUT_SECONDS (default: 30).
	StoragesTimeout int

	// DocsTimeout is the HTTP timeout in seconds for carbonio-docs-editor conversion requests.
	// Controlled by PREVIEW_DOCS_TIMEOUT_SECONDS (default: 15).
	DocsTimeout int

	// RenderConcurrency is the maximum number of concurrent image-render operations.
	// Controlled by PREVIEW_RENDER_CONCURRENCY (default: runtime.NumCPU()).
	RenderConcurrency int

	// PDFWorkers is the PDFium subprocess pool size.
	// Controlled by PREVIEW_PDF_WORKERS (default: runtime.NumCPU()).
	PDFWorkers int

	// VipsConcurrency is the number of libvips threads per operation.
	// Controlled by PREVIEW_VIPS_CONCURRENCY (default: 1).
	VipsConcurrency int
}

// loadEnvKnobs reads the five PREVIEW_* performance-tuning environment
// variables and returns an EnvKnobs struct. Absent or empty variables fall
// back to their documented defaults. An invalid value (non-integer or < 1)
// causes a descriptive error naming the variable so the service fails fast at
// startup.
func loadEnvKnobs() (EnvKnobs, error) {
	var k EnvKnobs
	var err error

	k.StoragesTimeout, err = envPositiveInt("PREVIEW_STORAGES_TIMEOUT_SECONDS", 30)
	if err != nil {
		return EnvKnobs{}, err
	}

	k.DocsTimeout, err = envPositiveInt("PREVIEW_DOCS_TIMEOUT_SECONDS", 15)
	if err != nil {
		return EnvKnobs{}, err
	}

	k.RenderConcurrency, err = envPositiveInt("PREVIEW_RENDER_CONCURRENCY", runtime.NumCPU())
	if err != nil {
		return EnvKnobs{}, err
	}

	k.PDFWorkers, err = envPositiveInt("PREVIEW_PDF_WORKERS", runtime.NumCPU())
	if err != nil {
		return EnvKnobs{}, err
	}

	k.VipsConcurrency, err = envPositiveInt("PREVIEW_VIPS_CONCURRENCY", 1)
	if err != nil {
		return EnvKnobs{}, err
	}

	return k, nil
}

// envPositiveInt reads an environment variable as a positive integer (>= 1).
// If the variable is absent or empty, defaultVal is returned.
// If the variable is set to a non-integer or a value < 1, an error naming the
// variable is returned.
func envPositiveInt(name string, defaultVal int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("config: %s has invalid value %q: must be a positive integer", name, raw)
	}
	return n, nil
}
