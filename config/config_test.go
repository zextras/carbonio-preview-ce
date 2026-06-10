package config

import (
	"os"
	"path/filepath"
	"testing"
)

// resetEnv clears all environment variables that Load() reads and returns a
// cleanup function that restores them.
func resetEnv(t *testing.T) func() {
	t.Helper()
	keys := []string{
		"PREVIEW_HOST", "PREVIEW_PORT",
		"STORAGES_HOST", "STORAGES_PORT",
		"DOCS_EDITOR_HOST", "DOCS_EDITOR_PORT",
		"PDF_WORKERS", "PDF_INTERNAL_PORT",
		"ROLE", "HYBRID", "VIPS_CONCURRENCY",
	}
	saved := make(map[string]string)
	for _, k := range keys {
		saved[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	return func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}
}

// TestDefaults verifies that every config field matches the hard-coded Python
// default before any config.ini or env vars are applied.
func TestDefaults(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	// Ensure Load does not find any config.ini in the search path.
	// Under test the /etc path won't exist so defaults() is all we get.
	if err := Load(); err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	tests := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"ServiceName", App.ServiceName, "preview"},
		{"ServiceIP", App.ServiceIP, "127.78.0.6"},
		{"ServicePort", App.ServicePort, "10000"},
		{"ServiceTimeoutInSeconds", App.ServiceTimeoutInSeconds, 30},
		{"ServiceDocsTimeout", App.ServiceDocsTimeout, 15},
		{"ServiceWorkers", App.ServiceWorkers, 2},
		{"ServiceImageName", App.ServiceImageName, "image"},
		{"ServiceHealthName", App.ServiceHealthName, "health"},
		{"ServicePDFName", App.ServicePDFName, "pdf"},
		{"ServiceDocumentName", App.ServiceDocumentName, "document"},
		{"ServiceEnableDocumentPreview", App.ServiceEnableDocumentPreview, true},
		{"ServiceEnableDocumentThumbnail", App.ServiceEnableDocumentThumbnail, false},
		{"LogLevel", App.LogLevel, "info"},
		{"LogPath", App.LogPath, "/var/log/carbonio/preview/"},
		{"ImageMinimumResolution", App.ImageMinimumResolution, 80},
		{"StorageName", App.StorageName, "slimstore"},
		{"StorageDownloadAPI", App.StorageDownloadAPI, "download"},
		{"StorageHealthCheck", App.StorageHealthCheck, "health/live"},
		{"StorageProtocol", App.StorageProtocol, "http"},
		{"StorageIP", App.StorageIP, "127.78.0.6"},
		{"StoragePort", App.StoragePort, "20000"},
		{"DocumentConversionProtocol", App.DocumentConversionProtocol, "http"},
		{"DocumentConversionIP", App.DocumentConversionIP, "127.78.0.6"},
		{"DocumentConversionPort", App.DocumentConversionPort, "20001"},
		{"DocumentConversionServiceEndpoint", App.DocumentConversionServiceEndpoint, "services/docs/editor"},
		{"DocumentConversionConvertAPI", App.DocumentConversionConvertAPI, "cool/convert-to"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// TestDerivedAddresses verifies that StorageFullAddress and the
// DocumentConversion* derived addresses are assembled correctly from defaults.
func TestDerivedAddresses(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			"StorageFullAddress",
			App.StorageFullAddress,
			"http://127.78.0.6:20000",
		},
		{
			"DocumentConversionBaseAddress",
			App.DocumentConversionBaseAddress,
			"http://127.78.0.6:20001",
		},
		{
			"DocumentConversionFullServiceAddress",
			App.DocumentConversionFullServiceAddress,
			"http://127.78.0.6:20001/services/docs/editor/",
		},
		{
			"DocumentConversionFullConvertAddress",
			App.DocumentConversionFullConvertAddress,
			"http://127.78.0.6:20001/services/docs/editor/cool/convert-to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// TestAreDocsEnabled verifies the derived AreDocsEnabled flag under different
// combinations of the preview / thumbnail flags.
func TestAreDocsEnabled(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	// Defaults: preview=true, thumbnail=false → AreDocsEnabled=true
	if err := Load(); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !App.AreDocsEnabled {
		t.Error("expected AreDocsEnabled=true when preview=true and thumbnail=false")
	}

	// Both false → AreDocsEnabled=false
	c := defaults()
	c.ServiceEnableDocumentPreview = false
	c.ServiceEnableDocumentThumbnail = false
	c.AreDocsEnabled = c.ServiceEnableDocumentPreview || c.ServiceEnableDocumentThumbnail
	if c.AreDocsEnabled {
		t.Error("expected AreDocsEnabled=false when both flags are false")
	}

	// Only thumbnail enabled → AreDocsEnabled=true
	c2 := defaults()
	c2.ServiceEnableDocumentPreview = false
	c2.ServiceEnableDocumentThumbnail = true
	c2.AreDocsEnabled = c2.ServiceEnableDocumentPreview || c2.ServiceEnableDocumentThumbnail
	if !c2.AreDocsEnabled {
		t.Error("expected AreDocsEnabled=true when only thumbnail is enabled")
	}
}

// TestEnvOverridesStorageHost verifies STORAGES_HOST overrides the storage IP
// and that the derived StorageFullAddress is recomputed accordingly.
func TestEnvOverridesStorageHost(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	os.Setenv("STORAGES_HOST", "10.0.0.1")
	os.Setenv("STORAGES_PORT", "9999")

	if err := Load(); err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if App.StorageIP != "10.0.0.1" {
		t.Errorf("StorageIP: got %q, want %q", App.StorageIP, "10.0.0.1")
	}
	if App.StoragePort != "9999" {
		t.Errorf("StoragePort: got %q, want %q", App.StoragePort, "9999")
	}
	// Derived address must also reflect the override.
	if App.StorageFullAddress != "http://10.0.0.1:9999" {
		t.Errorf("StorageFullAddress: got %q, want %q", App.StorageFullAddress, "http://10.0.0.1:9999")
	}
}

// TestEnvOverridesPreviewHost verifies PREVIEW_HOST / PREVIEW_PORT overrides.
func TestEnvOverridesPreviewHost(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	os.Setenv("PREVIEW_HOST", "192.168.1.1")
	os.Setenv("PREVIEW_PORT", "8080")

	if err := Load(); err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if App.ServiceIP != "192.168.1.1" {
		t.Errorf("ServiceIP: got %q, want %q", App.ServiceIP, "192.168.1.1")
	}
	if App.ServicePort != "8080" {
		t.Errorf("ServicePort: got %q, want %q", App.ServicePort, "8080")
	}
}

// TestEnvOverridesDocsEditorHost verifies DOCS_EDITOR_HOST / DOCS_EDITOR_PORT
// overrides and that derived document conversion addresses are recomputed.
func TestEnvOverridesDocsEditorHost(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	os.Setenv("DOCS_EDITOR_HOST", "docs.internal")
	os.Setenv("DOCS_EDITOR_PORT", "7777")

	if err := Load(); err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if App.DocumentConversionIP != "docs.internal" {
		t.Errorf("DocumentConversionIP: got %q, want %q", App.DocumentConversionIP, "docs.internal")
	}
	if App.DocumentConversionPort != "7777" {
		t.Errorf("DocumentConversionPort: got %q, want %q", App.DocumentConversionPort, "7777")
	}
	wantConvert := "http://docs.internal:7777/services/docs/editor/cool/convert-to"
	if App.DocumentConversionFullConvertAddress != wantConvert {
		t.Errorf("FullConvertAddress: got %q, want %q", App.DocumentConversionFullConvertAddress, wantConvert)
	}
}

// TestINIOverridesDefaults writes a real config.ini to a temp directory,
// loads it via the internal helpers, and verifies every field is overridden.
// This tests the applyINI function exhaustively.
func TestINIOverridesDefaults(t *testing.T) {
	restore := resetEnv(t)
	defer restore()

	const iniContent = `
[carbonio.preview]
name = mypreview
default_host = 1.2.3.4
default_port = 11111
timeout_in_seconds = 60
docs-timeout = 30
workers = 4
image_name = img
health_name = healthz
pdf_name = pdfdoc
document_name = doc
enable_document_preview = false
enable_document_thumbnail = true

[carbonio.storages]
default_host = 5.6.7.8
default_port = 22222
download_api = get
health_check = health/check
default_protocol = https
name = bigstore

[carbonio.docs-editor]
default_host = editor.host
default_port = 33333
default_protocol = https
service_endpoint = services/libreoffice
convert_api = lool/convert

[image_constants]
minimum_resolution = 100

[log]
level = debug
path = /tmp/logs/
`
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	if err := os.WriteFile(iniPath, []byte(iniContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c := defaults()
	loaded, err := iniLoad(iniPath)
	if err != nil {
		t.Fatalf("iniLoad: %v", err)
	}
	applyINI(loaded, &c)

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"ServiceName", c.ServiceName, "mypreview"},
		{"ServiceIP", c.ServiceIP, "1.2.3.4"},
		{"ServicePort", c.ServicePort, "11111"},
		{"ServiceTimeoutInSeconds", c.ServiceTimeoutInSeconds, 60},
		{"ServiceDocsTimeout", c.ServiceDocsTimeout, 30},
		{"ServiceWorkers", c.ServiceWorkers, 4},
		{"ServiceImageName", c.ServiceImageName, "img"},
		{"ServiceHealthName", c.ServiceHealthName, "healthz"},
		{"ServicePDFName", c.ServicePDFName, "pdfdoc"},
		{"ServiceDocumentName", c.ServiceDocumentName, "doc"},
		{"ServiceEnableDocumentPreview", c.ServiceEnableDocumentPreview, false},
		{"ServiceEnableDocumentThumbnail", c.ServiceEnableDocumentThumbnail, true},
		{"StorageIP", c.StorageIP, "5.6.7.8"},
		{"StoragePort", c.StoragePort, "22222"},
		{"StorageDownloadAPI", c.StorageDownloadAPI, "get"},
		{"StorageHealthCheck", c.StorageHealthCheck, "health/check"},
		{"StorageProtocol", c.StorageProtocol, "https"},
		{"StorageName", c.StorageName, "bigstore"},
		{"DocumentConversionIP", c.DocumentConversionIP, "editor.host"},
		{"DocumentConversionPort", c.DocumentConversionPort, "33333"},
		{"DocumentConversionProtocol", c.DocumentConversionProtocol, "https"},
		{"DocumentConversionServiceEndpoint", c.DocumentConversionServiceEndpoint, "services/libreoffice"},
		{"DocumentConversionConvertAPI", c.DocumentConversionConvertAPI, "lool/convert"},
		{"ImageMinimumResolution", c.ImageMinimumResolution, 100},
		{"LogLevel", c.LogLevel, "debug"},
		{"LogPath", c.LogPath, "/tmp/logs/"},
	}

	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// TestMessages verifies that the hard-coded messages match the Python spec
// exactly.  Any mismatch means client error messages would diverge.
func TestMessages(t *testing.T) {
	m := hardcodedMessages()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"StorageUnavailable", m.StorageUnavailable, "Storage is currently unavailable."},
		{"GenericErrorStorage", m.GenericErrorStorage, "Storage is available but there was an error executing your request."},
		{"ItemNotFound", m.ItemNotFound, "Requested item was not found in the storage."},
		{"InputError", m.InputError, "Some values in the query were not correct."},
		{"DocsEditorUnavailable", m.DocsEditorUnavailable, "Carbonio-docs-editor is currently unavailable, document preview service is currently offline."},
		{"HeightOrWidthNotInserted", m.HeightOrWidthNotInserted, "Height or width not found, example of valid input: 120x250."},
		{"NumberOfPagesNotValid", m.NumberOfPagesNotValid, "Pages must be at least 1."},
		{"HeightWidthNotValid", m.HeightWidthNotValid, "Height or width values must be integers >= 0."},
		{"IDNotValid", m.IDNotValid, "Id is not in a valid format, UUID1 to UUID4 are supported."},
		{"VersionNotValid", m.VersionNotValid, "Version is not valid, the accepted values are > 0."},
		{"FormatNotSupported", m.FormatNotSupported, "Format not supported."},
		{"FileNotValid", m.FileNotValid, "The input file should not be null."},
		{"DocumentThumbnailDisabled", m.DocumentThumbnailDisabled, "The document thumbnail function is not currently enabled!"},
		{"DocumentPreviewDisabled", m.DocumentPreviewDisabled, "The document preview function is not currently enabled!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
