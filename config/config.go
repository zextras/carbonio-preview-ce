// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads the carbonio-preview-ce configuration from the
// extensions-equivalent chain:
//
//  1. Registry defaults (registry.go)
//  2. /etc/carbonio/preview/config.properties (networking layer)
//     Consul KV carbonio-preview/?recurse (application layer)
//  3. Environment variables (NETWORKING_CONFIG_* / APPLICATION_CONFIG_*)
//
// The package exposes a single package-level *Config value, App, that is
// populated once at program start via Load().
package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
)

// Config holds every configuration value consumed by the service.
// Field names follow the Python "flat key" naming used in AppConfig.
type Config struct {
	// carbonio.preview.*
	// ServiceName, ServiceImageName, ServicePDFName, ServiceDocumentName, and
	// ServiceHealthName are hardcoded by design: routes are not operator-configurable.
	ServiceName                    string
	ServiceIP                      string
	ServicePort                    string
	ServiceTimeoutInSeconds        int
	ServiceDocsTimeout             int
	ServiceWorkers                 int
	ServiceImageName               string
	ServiceHealthName              string
	ServicePDFName                 string
	ServiceDocumentName            string
	ServiceEnableDocumentPreview   bool
	ServiceEnableDocumentThumbnail bool

	// image_constants.*
	ImageMinimumResolution int

	// carbonio.storages.*
	StorageDownloadAPI string
	StorageHealthCheck string
	StorageProtocol    string
	StorageIP          string
	StoragePort        string

	// carbonio.docs-editor.*
	DocumentConversionProtocol        string
	DocumentConversionIP              string
	DocumentConversionPort            string
	DocumentConversionServiceEndpoint string
	DocumentConversionConvertAPI      string

	// Process-level knobs (raw env only — parent↔pdfworker IPC)
	PDFWorkers      int
	PDFInternalPort int
	Role            string

	// Application-layer knob
	VIPSConcurrency int

	// Derived addresses (computed once in Load)
	StorageFullAddress                   string
	DocumentConversionBaseAddress        string
	DocumentConversionFullServiceAddress string
	DocumentConversionFullConvertAddress string

	// Derived feature flag
	AreDocsEnabled bool
}

// App is the package-level, process-wide configuration instance.
// It is populated by Load() and safe to read after that call returns.
var App Config

// resolvedNetworking and resolvedApplication hold the two FrozenMap snapshots
// produced by Resolve during Load(). They are the zero-value FrozenMap (Get
// always returns "", false) until a successful Load() call.
var (
	resolvedNetworking  FrozenMap
	resolvedApplication FrozenMap

	// loaded is set to true by a successful Load() call.
	// Networking() and Application() panic when it is false.
	loaded bool
)

// Networking returns a snapshot of the resolved networking-layer key→value map.
// Valid after a successful Load().
// Panics with "config: accessor called before Load()" if Load() has not been
// called successfully yet.
// The advanced module reads its extra registered networking keys through this.
func Networking() FrozenMap {
	if !loaded {
		panic("config: accessor called before Load()")
	}
	return resolvedNetworking
}

// Application returns a snapshot of the resolved application-layer key→value map.
// Valid after a successful Load().
// Panics with "config: accessor called before Load()" if Load() has not been
// called successfully yet.
// The advanced module reads its extra registered application keys through this.
func Application() FrozenMap {
	if !loaded {
		panic("config: accessor called before Load()")
	}
	return resolvedApplication
}

// Load initialises App from the extensions-equivalent chain:
// registry defaults → config.properties / Consul KV → ENV.
//
// It is idempotent: calling it more than once re-reads everything.
// Consul unreachable → error (fail-fast).
func Load() error {
	r, err := Resolve()
	if err != nil {
		return fmt.Errorf("config.Load: resolve chain: %w", err)
	}

	var c Config

	// ── Hardcoded route constants (not operator-configurable by design) ────────
	c.ServiceName = "preview"
	c.ServiceImageName = "image"
	c.ServicePDFName = "pdf"
	c.ServiceDocumentName = "document"
	c.ServiceHealthName = "health"

	// ── Networking layer ───────────────────────────────────────────────────────
	c.ServiceIP = netStr(r, "carbonio.service.host")
	c.ServicePort = netStr(r, "carbonio.service.port")
	c.StorageIP = netStr(r, "carbonio.storages.host")
	c.StoragePort = netStr(r, "carbonio.storages.port")
	c.StorageProtocol = netStr(r, "carbonio.storages.protocol")
	c.DocumentConversionIP = netStr(r, "carbonio.docs-editor.host")
	c.DocumentConversionPort = netStr(r, "carbonio.docs-editor.port")
	c.DocumentConversionProtocol = netStr(r, "carbonio.docs-editor.protocol")

	// ── Application layer ──────────────────────────────────────────────────────
	var parseErr error

	c.ServiceTimeoutInSeconds, parseErr = appPositiveInt(r, "timeout-in-seconds", parseErr)
	c.ServiceDocsTimeout, parseErr = appPositiveInt(r, "docs-timeout-in-seconds", parseErr)
	c.VIPSConcurrency, parseErr = appInt(r, "vips-concurrency", parseErr)
	c.ImageMinimumResolution, parseErr = appPositiveInt(r, "image-minimum-resolution", parseErr)

	// workers: absent → runtime.NumCPU() (registry has no default for this key)
	if raw, ok := r.Application.Get("workers"); ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return fmt.Errorf("config: key %q has invalid value %q: must be a positive integer", "workers", raw)
		}
		c.ServiceWorkers = n
	} else {
		c.ServiceWorkers = runtime.NumCPU()
	}

	// pdf-workers: absent → runtime.NumCPU() (registry has no default for this key)
	if raw, ok := r.Application.Get("pdf-workers"); ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return fmt.Errorf("config: key %q has invalid value %q: must be a positive integer", "pdf-workers", raw)
		}
		c.PDFWorkers = n
	} else {
		c.PDFWorkers = runtime.NumCPU()
	}

	c.ServiceEnableDocumentPreview, parseErr = appBool(r, "enable-document-preview", parseErr)
	c.ServiceEnableDocumentThumbnail, parseErr = appBool(r, "enable-document-thumbnail", parseErr)

	c.StorageDownloadAPI = appStr(r, "storages.download-api")
	c.StorageHealthCheck = appStr(r, "storages.health-check")
	c.DocumentConversionServiceEndpoint = appStr(r, "docs-editor.service-endpoint")
	c.DocumentConversionConvertAPI = appStr(r, "docs-editor.convert-api")

	if parseErr != nil {
		return parseErr
	}

	// ── Process-level knobs (raw env; parent↔pdfworker IPC) ───────────────────
	if v := os.Getenv("ROLE"); v != "" {
		c.Role = v
	}

	c.PDFInternalPort = 10104 // default
	if v := os.Getenv("PDF_INTERNAL_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.PDFInternalPort = n
		}
	}

	// ── Derived addresses ──────────────────────────────────────────────────────
	c.StorageFullAddress = fmt.Sprintf(
		"%s://%s:%s",
		c.StorageProtocol, c.StorageIP, c.StoragePort,
	)
	c.DocumentConversionBaseAddress = fmt.Sprintf(
		"%s://%s:%s",
		c.DocumentConversionProtocol,
		c.DocumentConversionIP,
		c.DocumentConversionPort,
	)
	c.DocumentConversionFullServiceAddress = fmt.Sprintf(
		"%s/%s/",
		c.DocumentConversionBaseAddress,
		c.DocumentConversionServiceEndpoint,
	)
	c.DocumentConversionFullConvertAddress = fmt.Sprintf(
		"%s/%s/%s",
		c.DocumentConversionBaseAddress,
		c.DocumentConversionServiceEndpoint,
		c.DocumentConversionConvertAPI,
	)
	c.AreDocsEnabled = c.ServiceEnableDocumentPreview || c.ServiceEnableDocumentThumbnail

	App = c
	resolvedNetworking = r.Networking
	resolvedApplication = r.Application
	loaded = true
	return nil
}

// ── Internal helpers ───────────────────────────────────────────────────────────

// netStr reads a networking-layer string key. The registry always provides a
// default so the value will always be present; returns "" only if the registry
// has no default (which would be a registration bug).
func netStr(r Resolved, key string) string {
	v, _ := r.Networking.Get(key)
	return v
}

// appStr reads an application-layer string key.
func appStr(r Resolved, key string) string {
	v, _ := r.Application.Get(key)
	return v
}

// appInt reads an application-layer integer key. Returns (0, err) on parse
// failure, chaining with any prior error so the caller can surface the first one.
func appInt(r Resolved, key string, prior error) (int, error) {
	if prior != nil {
		return 0, prior
	}
	raw, ok := r.Application.Get(key)
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: key %q has invalid value %q: %w", key, raw, err)
	}
	return n, nil
}

// appPositiveInt reads an application-layer integer key and requires the value
// to be >= 1. Returns (0, err) on parse or range failure, chaining with any
// prior error. Used for keys that Python validated with validate_positive_int().
func appPositiveInt(r Resolved, key string, prior error) (int, error) {
	if prior != nil {
		return 0, prior
	}
	raw, ok := r.Application.Get(key)
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: key %q has invalid value %q: %w", key, raw, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("config: key %q has invalid value %q: must be a positive integer", key, raw)
	}
	return n, nil
}

// appBool reads an application-layer boolean key ("true"/"false").
func appBool(r Resolved, key string, prior error) (bool, error) {
	if prior != nil {
		return false, prior
	}
	raw, ok := r.Application.Get(key)
	if !ok {
		return false, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("config: key %q has invalid value %q: must be \"true\" or \"false\"", key, raw)
	}
}
