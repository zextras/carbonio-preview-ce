// Package config loads the carbonio-preview-ce configuration from:
//  1. hard-coded defaults
//  2. /etc/carbonio/preview/config.ini (if present)
//  3. environment variable overrides
//
// The package exposes a single package-level *Config value, App, that is
// populated once at program start via Load().
package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"

	"gopkg.in/ini.v1"
)

// Config holds every configuration value consumed by the service.
// Field names follow the Python "flat key" naming used in AppConfig.
type Config struct {
	// carbonio.preview.*
	ServiceName                 string
	ServiceIP                   string
	ServicePort                 string
	ServiceTimeoutInSeconds     int
	ServiceDocsTimeout          int
	ServiceWorkers              int
	ServiceImageName            string
	ServiceHealthName           string
	ServicePDFName              string
	ServiceDocumentName         string
	ServiceEnableDocumentPreview    bool
	ServiceEnableDocumentThumbnail  bool

	// log.*
	LogFormat string
	LogLevel  string
	LogPath   string

	// image_constants.*
	ImageMinimumResolution int

	// carbonio.storages.*
	StorageName        string
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

	// Process-level knobs (env only)
	PDFWorkers      int
	PDFInternalPort int
	Role            string
	Hybrid          bool
	VIPSConcurrency int

	// Derived addresses (computed once in Load)
	StorageFullAddress                    string
	DocumentConversionBaseAddress         string
	DocumentConversionFullServiceAddress  string
	DocumentConversionFullConvertAddress  string

	// Derived feature flag
	AreDocsEnabled bool
}

// App is the package-level, process-wide configuration instance.
// It is populated by Load() and safe to read after that call returns.
var App Config

// Load initialises App from defaults → config.ini → environment variables.
// It is idempotent: calling it more than once re-reads everything.
func Load() error {
	c := defaults()

	// Attempt to read config.ini from the first available path.
	cfg, err := loadINI()
	if err == nil {
		applyINI(cfg, &c)
	}
	// err != nil means no config.ini was found — that is fine; defaults stand.

	// Environment variable overrides (always applied last).
	applyEnvOverrides(&c)

	// Derived addresses.
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
	return nil
}

// defaults returns a Config pre-populated with every hard-coded default value
// described in the Python source (config.ini defaults section).
func defaults() Config {
	return Config{
		ServiceName:                    "preview",
		ServiceIP:                      "127.78.0.6",
		ServicePort:                    "10000",
		ServiceTimeoutInSeconds:        30,
		ServiceDocsTimeout:             15,
		ServiceWorkers:                 2,
		ServiceImageName:               "image",
		ServiceHealthName:              "health",
		ServicePDFName:                 "pdf",
		ServiceDocumentName:            "document",
		ServiceEnableDocumentPreview:   true,
		ServiceEnableDocumentThumbnail: false,

		LogFormat: "[%(asctime)s] %(levelname)s [%(name)s.%(funcName)s:%(lineno)d] %(message)s",
		LogLevel:  "info",
		LogPath:   "/var/log/carbonio/preview/",

		ImageMinimumResolution: 80,

		StorageName:        "slimstore",
		StorageDownloadAPI: "download",
		StorageHealthCheck: "health/live",
		StorageProtocol:    "http",
		StorageIP:          "127.78.0.6",
		StoragePort:        "20000",

		DocumentConversionProtocol:        "http",
		DocumentConversionIP:              "127.78.0.6",
		DocumentConversionPort:            "20001",
		DocumentConversionServiceEndpoint: "services/docs/editor",
		DocumentConversionConvertAPI:      "cool/convert-to",

		// Process-level knobs — sensible defaults; env overrides applied below.
		PDFWorkers:      runtime.NumCPU(),
		PDFInternalPort: 10104,
		Role:            "",
		Hybrid:          false,
		VIPSConcurrency: 1,
	}
}

// configSearchPaths returns the ordered list of config.ini locations to try.
// The first file that exists is used.
func configSearchPaths() []string {
	return []string{
		"/etc/carbonio/preview/config.ini",
	}
}

func loadINI() (*ini.File, error) {
	for _, p := range configSearchPaths() {
		if _, err := os.Stat(p); err == nil {
			return ini.Load(p)
		}
	}
	return nil, fmt.Errorf("no config.ini found in any search path")
}

// iniLoad loads an ini file from an explicit path.
// It is unexported so that only same-package tests can call it.
func iniLoad(path string) (*ini.File, error) {
	return ini.Load(path)
}

// iniStr is a helper that returns the string value of a dotted key
// (section "carbonio.preview", key "default_host") or the fallback if
// the key is absent.
func iniStr(cfg *ini.File, section, key, fallback string) string {
	s, err := cfg.GetSection(section)
	if err != nil {
		return fallback
	}
	k, err := s.GetKey(key)
	if err != nil {
		return fallback
	}
	v := k.String()
	if v == "" {
		return fallback
	}
	return v
}

func iniBool(cfg *ini.File, section, key string, fallback bool) bool {
	s, err := cfg.GetSection(section)
	if err != nil {
		return fallback
	}
	k, err := s.GetKey(key)
	if err != nil {
		return fallback
	}
	b, err := k.Bool()
	if err != nil {
		return fallback
	}
	return b
}

func iniInt(cfg *ini.File, section, key string, fallback int) int {
	raw := iniStr(cfg, section, key, "")
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

// applyINI overlays values from a parsed config.ini onto c.
// Uses the exact section and key names from the Python source.
func applyINI(cfg *ini.File, c *Config) {
	const (
		secPreview    = "carbonio.preview"
		secLog        = "log"
		secImageConst = "image_constants"
		secStorages   = "carbonio.storages"
		secDocs       = "carbonio.docs-editor"
	)

	c.ServiceName = iniStr(cfg, secPreview, "name", c.ServiceName)
	c.ServiceIP = iniStr(cfg, secPreview, "default_host", c.ServiceIP)
	c.ServicePort = iniStr(cfg, secPreview, "default_port", c.ServicePort)
	c.ServiceTimeoutInSeconds = iniInt(cfg, secPreview, "timeout_in_seconds", c.ServiceTimeoutInSeconds)
	c.ServiceDocsTimeout = iniInt(cfg, secPreview, "docs-timeout", c.ServiceDocsTimeout)
	c.ServiceWorkers = iniInt(cfg, secPreview, "workers", c.ServiceWorkers)
	c.ServiceImageName = iniStr(cfg, secPreview, "image_name", c.ServiceImageName)
	c.ServiceHealthName = iniStr(cfg, secPreview, "health_name", c.ServiceHealthName)
	c.ServicePDFName = iniStr(cfg, secPreview, "pdf_name", c.ServicePDFName)
	c.ServiceDocumentName = iniStr(cfg, secPreview, "document_name", c.ServiceDocumentName)
	c.ServiceEnableDocumentPreview = iniBool(cfg, secPreview, "enable_document_preview", c.ServiceEnableDocumentPreview)
	c.ServiceEnableDocumentThumbnail = iniBool(cfg, secPreview, "enable_document_thumbnail", c.ServiceEnableDocumentThumbnail)

	c.LogFormat = iniStr(cfg, secLog, "format", c.LogFormat)
	c.LogLevel = iniStr(cfg, secLog, "level", c.LogLevel)
	c.LogPath = iniStr(cfg, secLog, "path", c.LogPath)

	c.ImageMinimumResolution = iniInt(cfg, secImageConst, "minimum_resolution", c.ImageMinimumResolution)

	c.StorageName = iniStr(cfg, secStorages, "name", c.StorageName)
	c.StorageDownloadAPI = iniStr(cfg, secStorages, "download_api", c.StorageDownloadAPI)
	c.StorageHealthCheck = iniStr(cfg, secStorages, "health_check", c.StorageHealthCheck)
	c.StorageProtocol = iniStr(cfg, secStorages, "default_protocol", c.StorageProtocol)
	c.StorageIP = iniStr(cfg, secStorages, "default_host", c.StorageIP)
	c.StoragePort = iniStr(cfg, secStorages, "default_port", c.StoragePort)

	c.DocumentConversionProtocol = iniStr(cfg, secDocs, "default_protocol", c.DocumentConversionProtocol)
	c.DocumentConversionIP = iniStr(cfg, secDocs, "default_host", c.DocumentConversionIP)
	c.DocumentConversionPort = iniStr(cfg, secDocs, "default_port", c.DocumentConversionPort)
	c.DocumentConversionServiceEndpoint = iniStr(cfg, secDocs, "service_endpoint", c.DocumentConversionServiceEndpoint)
	c.DocumentConversionConvertAPI = iniStr(cfg, secDocs, "convert_api", c.DocumentConversionConvertAPI)
}

// applyEnvOverrides overwrites config fields from environment variables.
// The env vars are checked last so they win over config.ini.
func applyEnvOverrides(c *Config) {
	if v := os.Getenv("PREVIEW_HOST"); v != "" {
		c.ServiceIP = v
	}
	if v := os.Getenv("PREVIEW_PORT"); v != "" {
		c.ServicePort = v
	}
	if v := os.Getenv("STORAGES_HOST"); v != "" {
		c.StorageIP = v
	}
	if v := os.Getenv("STORAGES_PORT"); v != "" {
		c.StoragePort = v
	}
	if v := os.Getenv("DOCS_EDITOR_HOST"); v != "" {
		c.DocumentConversionIP = v
	}
	if v := os.Getenv("DOCS_EDITOR_PORT"); v != "" {
		c.DocumentConversionPort = v
	}

	// Process-level knobs (env only, no config.ini equivalent).
	if v := os.Getenv("PDF_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.PDFWorkers = n
		}
	} else {
		// Fall back to carbonio.preview.workers (already set from INI or default).
		c.PDFWorkers = c.ServiceWorkers
	}

	if v := os.Getenv("PDF_INTERNAL_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.PDFInternalPort = n
		}
	}

	if v := os.Getenv("ROLE"); v != "" {
		c.Role = v
	}

	if v := os.Getenv("HYBRID"); v == "1" || v == "true" {
		c.Hybrid = true
	}

	if v := os.Getenv("VIPS_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.VIPSConcurrency = n
		}
	}
}
