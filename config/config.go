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
	"log/slog"
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
	ServiceImageName               string
	ServiceHealthName              string
	ServicePDFName                 string
	ServiceDocumentName            string
	ServiceEnableDocumentPreview   bool
	ServiceEnableDocumentThumbnail bool

	// image_constants.* — hardcoded constant; not operator-configurable.
	ImageMinimumResolution int

	// carbonio.storages.* — endpoint paths are hardcoded constants.
	StorageDownloadAPI string
	StorageHealthCheck string
	StorageProtocol    string
	StorageIP          string
	StoragePort        string

	// carbonio.docs-editor.* — endpoint paths are hardcoded constants.
	DocumentConversionProtocol        string
	DocumentConversionIP              string
	DocumentConversionPort            string
	DocumentConversionServiceEndpoint string
	DocumentConversionConvertAPI      string

	// RenderConcurrency is the maximum number of concurrent image-render operations.
	// Application-layer key "render-concurrency" (Consul KV carbonio-preview/render-concurrency
	// / env APPLICATION_CONFIG_RENDER_CONCURRENCY). Defaults to runtime.NumCPU() when absent.
	RenderConcurrency int

	// PDFWorkers is the size of the PDFium subprocess worker pool.
	// Application-layer key "pdf-workers" (Consul KV carbonio-preview/pdf-workers
	// / env APPLICATION_CONFIG_PDF_WORKERS). Defaults to runtime.NumCPU() when absent.
	PDFWorkers int

	// VideoConcurrency is the maximum number of concurrent video first-frame
	// generate operations. Application-layer key "video-concurrency" (Consul KV
	// carbonio-preview/video-concurrency / env APPLICATION_CONFIG_VIDEO_CONCURRENCY).
	// Defaults to runtime.NumCPU() when absent.
	VideoConcurrency int

	// VIPSConcurrency is the libvips internal-threads level. It is a hardcoded
	// internal constant (= 1), not an operator knob — no registry key, no env var,
	// no KV path.
	VIPSConcurrency int

	// CacheMaxBytes is the byte budget of the in-process rendered-output cache,
	// derived from the "cache-max-mb" application key (MiB → bytes). 0 disables
	// the cache. Application key ⇒ env override APPLICATION_CONFIG_CACHE_MAX_MB
	// is accepted automatically by the resolver chain.
	CacheMaxBytes int64

	// Derived addresses (computed once in Load)
	StorageFullAddress                   string
	DocumentConversionBaseAddress        string
	DocumentConversionFullServiceAddress string
	DocumentConversionFullConvertAddress string

	// Derived feature flag
	AreDocsEnabled bool

	// LogLevel is the effective slog level, read from PREVIEW_LOG_LEVEL env var.
	// This is a per-instance, framework-level knob (equivalent to QUARKUS_LOG_LEVEL)
	// and is intentionally outside the extensions config chain (no registry key, no KV).
	LogLevel slog.Level
}

// Hardcoded endpoint-path constants. These values were formerly stored in Consul
// KV but are not operator-configurable in practice. Baking them in as constants
// shrinks the KV surface and eliminates Consul round-trips for immutable data.
const (
	storageDownloadAPI           = "download"
	storageHealthCheck           = "health/live"
	documentConversionEndpoint   = "services/docs/editor"
	documentConversionConvertAPI = "cool/convert-to"
	imageMinimumResolution       = 80

	// vipsConcurrency is the libvips internal-threads setting. It is a plain
	// internal constant (=1), not an operator knob: no registry key, no env var,
	// no KV path. libvips concurrency above 1 hurts throughput for the
	// per-request render pattern (concurrency is bounded at the render semaphore).
	vipsConcurrency = 1
)

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

	c.ServiceEnableDocumentPreview, parseErr = appBool(r, "enable-document-preview", parseErr)
	c.ServiceEnableDocumentThumbnail, parseErr = appBool(r, "enable-document-thumbnail", parseErr)
	c.ServiceTimeoutInSeconds, parseErr = appPositiveInt(r, "timeout-in-seconds", parseErr)
	c.ServiceDocsTimeout, parseErr = appPositiveInt(r, "docs-timeout-in-seconds", parseErr)

	var cacheMaxMB int
	cacheMaxMB, parseErr = appNonNegativeInt(r, "cache-max-mb", parseErr)

	// Concurrency knobs (APPLICATION layer). Absent → 0 here → runtime.NumCPU()
	// fallback below. A present-but-invalid value (non-integer or < 1) fails fast
	// via appPositiveInt.
	c.RenderConcurrency, parseErr = appPositiveInt(r, "render-concurrency", parseErr)
	c.PDFWorkers, parseErr = appPositiveInt(r, "pdf-workers", parseErr)
	c.VideoConcurrency, parseErr = appPositiveInt(r, "video-concurrency", parseErr)

	if parseErr != nil {
		return parseErr
	}

	c.CacheMaxBytes = int64(cacheMaxMB) * 1024 * 1024

	if c.RenderConcurrency == 0 {
		c.RenderConcurrency = runtime.NumCPU()
	}
	if c.PDFWorkers == 0 {
		c.PDFWorkers = runtime.NumCPU()
	}
	if c.VideoConcurrency == 0 {
		c.VideoConcurrency = runtime.NumCPU()
	}

	// ── Hardcoded endpoint constants (not operator-configurable) ──────────────
	c.StorageDownloadAPI = storageDownloadAPI
	c.StorageHealthCheck = storageHealthCheck
	c.DocumentConversionServiceEndpoint = documentConversionEndpoint
	c.DocumentConversionConvertAPI = documentConversionConvertAPI
	c.ImageMinimumResolution = imageMinimumResolution

	// ── libvips internal-threads (plain internal constant; not a knob) ────────
	c.VIPSConcurrency = vipsConcurrency

	// ── Log level (PREVIEW_LOG_LEVEL — outside the extensions chain) ─────────
	// Read directly via os.Getenv; absent/empty → info.  Invalid → fail-fast.
	logLevel, err := loadLogLevel()
	if err != nil {
		return err
	}
	c.LogLevel = logLevel
	// Apply the level to the package-level slog handler immediately so all
	// subsequent log calls (in server startup, etc.) honour the configured level.
	logLevelVar.Set(logLevel)

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

// appPositiveInt reads an application-layer key as a positive integer (>= 1).
// If the key is absent (no KV value, no env override, registry default empty),
// it returns 0 and no error — callers should ensure the registry provides a
// non-empty default so the key is always present.
// If the value is present but not a valid positive integer, an error naming the
// key is returned (fail-fast at Load time).
func appPositiveInt(r Resolved, key string, prior error) (int, error) {
	if prior != nil {
		return 0, prior
	}
	raw, ok := r.Application.Get(key)
	if !ok || raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("config: key %q has invalid value %q: must be a positive integer", key, raw)
	}
	return n, nil
}

// appNonNegativeInt reads an application-layer key as a non-negative integer
// (>= 0). Absent/empty → returns 0 (callers should give the key a default).
// A present-but-invalid value (non-integer or < 0) is a fail-fast error.
func appNonNegativeInt(r Resolved, key string, prior error) (int, error) {
	if prior != nil {
		return 0, prior
	}
	raw, ok := r.Application.Get(key)
	if !ok || raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("config: key %q has invalid value %q: must be a non-negative integer", key, raw)
	}
	return n, nil
}
