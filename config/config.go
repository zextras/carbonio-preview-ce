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

	// RenderConcurrency is the maximum number of concurrent render operations
	// (image, PDF, document; does not apply to video).
	// Application-layer key "image-document.max-concurrent-operations" (Consul KV
	// carbonio-preview/image-document/max-concurrent-operations / env
	// APPLICATION_CONFIG_IMAGE_DOCUMENT_MAX_CONCURRENT_OPERATIONS). Defaults to
	// runtime.NumCPU() when absent.
	RenderConcurrency int

	// PDFWorkers is the size of the PDFium subprocess worker pool.
	// Application-layer key "document.subprocess-pool-size" (Consul KV
	// carbonio-preview/document/subprocess-pool-size / env
	// APPLICATION_CONFIG_DOCUMENT_SUBPROCESS_POOL_SIZE). Defaults to
	// runtime.NumCPU() when absent.
	PDFWorkers int

	// VideoConcurrency is the maximum number of concurrent video first-frame
	// extraction jobs. Application-layer key "video.max-concurrent-extractions"
	// (Consul KV carbonio-preview/video/max-concurrent-extractions / env
	// APPLICATION_CONFIG_VIDEO_MAX_CONCURRENT_EXTRACTIONS).
	// Defaults to runtime.NumCPU() when absent.
	VideoConcurrency int

	// VIPSConcurrency is the libvips internal-threads level. It is a hardcoded
	// internal constant (= 1), not an operator knob — no registry key, no env var,
	// no KV path.
	VIPSConcurrency int

	// CacheMaxBytes is the byte budget of the in-process rendered-output cache,
	// derived from the "cache-max-mb" application key (MiB → bytes). 0
	// disables the cache. Application key ⇒ env override
	// APPLICATION_CONFIG_CACHE_MAX_MB is accepted automatically by the
	// resolver chain.
	CacheMaxBytes int64

	// Derived addresses (computed once in Load)
	StorageFullAddress                   string
	DocumentConversionBaseAddress        string
	DocumentConversionFullServiceAddress string
	DocumentConversionFullConvertAddress string

	// Derived feature flag
	AreDocsEnabled bool

	// OpenAPIEnabled reports whether the huma-served OpenAPI spec endpoints
	// (/openapi.json, /openapi.yaml, /openapi-3.0.json, /openapi-3.0.yaml) and
	// the Swagger UI at /docs are exposed. Application-layer key
	// "openapi.enabled" (Consul KV carbonio-preview/openapi/enabled / env
	// APPLICATION_CONFIG_OPENAPI_ENABLED).
	//
	// DEFAULT-OFF (registry default "false"; an absent key also yields false),
	// mirroring carbonio-quarkus-extensions-rest's
	// %prod.quarkus.smallrye-openapi.enabled=false. Note this is unrelated to
	// AreDocsEnabled above, which is about DOCUMENT (Collabora) previews.
	OpenAPIEnabled bool

	// LogLevel is the effective slog level, read from PREVIEW_LOG_LEVEL env var.
	// This is a per-instance, framework-level knob (equivalent to QUARKUS_LOG_LEVEL)
	// and is intentionally outside the extensions config chain (no registry key, no KV).
	LogLevel slog.Level

	// ── Database (PostgreSQL via Consul mesh) ──────────────────────────────
	// PostgreSQL host/port come from the networking layer (config.properties).
	// Credentials (db-name, db-username, db-password) come from Consul KV at
	// carbonio-preview/database/credentials/* (written by carbonio-preview-db-bootstrap).
	// DSN and PoolConfig are derived once in Load() for use by cmd/.../main.go.
	// If credentials are absent the DSN is empty and the DB layer will fail-fast
	// at pool-open time (not at config parse time, so unit tests without a DB pass).
	DBDSN               string
	DBPoolMaxConns      int32
	DBPoolMinConns      int32
	DBConnMaxLifetimeMs int

	// ── Video worker ─────────────────────────────────────────────────────────
	// Application-layer keys video.poll-interval-seconds /
	// video.stuck-generation-timeout-seconds / video.max-attempts. Zero values are
	// resolved to defaults in the worker itself (matching WSC Java constants).
	// Total ffmpeg concurrency is governed by VideoConcurrency
	// (video.max-concurrent-extractions key) via the shared VideoSem.
	VideoSweepIntervalSeconds int
	VideoStaleTTLSeconds      int
	VideoMaxAttempts          int
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

	c.ServiceEnableDocumentPreview, parseErr = appBool(r, "document.enable-preview", parseErr)
	c.ServiceEnableDocumentThumbnail, parseErr = appBool(r, "document.enable-thumbnail", parseErr)
	// Absent/unset → appBool returns false, so the spec endpoints stay off even
	// if the registry default were ever dropped.
	c.OpenAPIEnabled, parseErr = appBool(r, "openapi.enabled", parseErr)
	c.ServiceTimeoutInSeconds, parseErr = appPositiveInt(r, "image-document.fetch-timeout-seconds", parseErr)
	c.ServiceDocsTimeout, parseErr = appPositiveInt(r, "document.conversion-timeout-seconds", parseErr)

	var cacheMaxMB int
	cacheMaxMB, parseErr = appNonNegativeInt(r, "cache-max-mb", parseErr)

	// Concurrency knobs (APPLICATION layer). Absent → 0 here → runtime.NumCPU()
	// fallback below. A present-but-invalid value (non-integer or < 1) fails fast
	// via appPositiveInt.
	c.RenderConcurrency, parseErr = appPositiveInt(r, "image-document.max-concurrent-operations", parseErr)
	c.PDFWorkers, parseErr = appPositiveInt(r, "document.subprocess-pool-size", parseErr)
	c.VideoConcurrency, parseErr = appPositiveInt(r, "video.max-concurrent-extractions", parseErr)

	var dbPoolMaxConns, dbPoolMinConns, dbConnMaxLifetimeMs int
	dbPoolMaxConns, parseErr = appPositiveInt(r, "database.db-pool-max-size", parseErr)
	dbPoolMinConns, parseErr = appPositiveInt(r, "database.db-pool-min-size", parseErr)
	dbConnMaxLifetimeMs, parseErr = appPositiveInt(r, "database.db-pool-max-lifetime", parseErr)

	var videoSweepInterval, videoStaleTTL, videoMaxAttempts int
	videoSweepInterval, parseErr = appPositiveInt(r, "video.poll-interval-seconds", parseErr)
	videoStaleTTL, parseErr = appPositiveInt(r, "video.stuck-generation-timeout-seconds", parseErr)
	videoMaxAttempts, parseErr = appPositiveInt(r, "video.max-attempts", parseErr)

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

	// ── Database pool tuning ──────────────────────────────────────────────────
	// Defaults are baked into registry.go but appPositiveInt returns 0 for empty;
	// guard against that here (should not happen since registry provides defaults).
	if dbPoolMaxConns > 0 {
		c.DBPoolMaxConns = int32(dbPoolMaxConns)
	} else {
		c.DBPoolMaxConns = 10
	}
	if dbPoolMinConns > 0 {
		c.DBPoolMinConns = int32(dbPoolMinConns)
	} else {
		c.DBPoolMinConns = 2
	}
	if dbConnMaxLifetimeMs > 0 {
		c.DBConnMaxLifetimeMs = dbConnMaxLifetimeMs
	} else {
		c.DBConnMaxLifetimeMs = 600000
	}

	// ── Video worker tuning ───────────────────────────────────────────────────
	// Zero → the worker applies its own defaults (matching WSC Java constants).
	c.VideoSweepIntervalSeconds = videoSweepInterval
	c.VideoStaleTTLSeconds = videoStaleTTL
	c.VideoMaxAttempts = videoMaxAttempts

	// ── Database DSN ───────────────────────────────────────────────────────────
	// Credentials are written by carbonio-preview-db-bootstrap into Consul KV
	// under carbonio-preview/database/credentials/{db-name,db-username,db-password}.
	// The fetchConsulKV routine strips the "carbonio-preview/" prefix and converts
	// '/' to '.' so these resolve as application keys:
	//   database.credentials.db-name
	//   database.credentials.db-username
	//   database.credentials.db-password
	//
	// When credentials are absent (e.g. during tests or before db-bootstrap is run)
	// DSN is left empty. The service will fail-fast at pool-open time in main(),
	// not here, so unit tests that don't touch the DB can still Load() safely.
	pgHost := netStr(r, "carbonio.postgresql.host")
	pgPort := netStr(r, "carbonio.postgresql.port")
	dbName, _ := r.Application.Get("database.credentials.db-name")
	dbUser, _ := r.Application.Get("database.credentials.db-username")
	dbPass, _ := r.Application.Get("database.credentials.db-password")

	if dbName != "" && dbUser != "" {
		c.DBDSN = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s",
			dbUser, dbPass, pgHost, pgPort, dbName,
		)
	} else {
		// Credentials absent: service will fail-fast at db.New() in main().
		// Leave DBDSN empty so callers can detect the absent-creds case and
		// emit a clear error message rather than a parse error.
		c.DBDSN = ""
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
