// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildKVServer returns an httptest.Server that answers the Consul KV recurse
// endpoint with the provided map. A nil map → 404 (prefix not found).
func buildKVServer(t *testing.T, kv map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/carbonio-preview/" {
			http.NotFound(w, r)
			return
		}
		if kv == nil {
			http.NotFound(w, r)
			return
		}
		var entries []kvEntry
		for k, v := range kv {
			val := base64.StdEncoding.EncodeToString([]byte(v))
			entries = append(entries, kvEntry{Key: "carbonio-preview/" + k, Value: &val})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pointConsulAt sets the NETWORKING_CONFIG env vars so Resolve() uses srv.
func pointConsulAt(t *testing.T, srv *httptest.Server) {
	t.Helper()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST", host)
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_PORT", port)
}

// loadWithConsul calls Load() with service-discover pointing at srv.
// propPath overrides the networking properties file (empty = none).
func loadWithConsul(t *testing.T, srv *httptest.Server) error {
	t.Helper()
	pointConsulAt(t, srv)
	return Load()
}

// ─── defaults ────────────────────────────────────────────────────────────────

// TestDefaults verifies that every config field matches the registry defaults
// when no properties file, Consul KV, or env vars are present.
func TestDefaults(t *testing.T) {
	srv := buildKVServer(t, nil) // 404 → empty application map
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	type chk struct {
		name string
		got  interface{}
		want interface{}
	}
	tests := []chk{
		// networking
		{"ServiceIP", App.ServiceIP, "127.78.0.6"},
		{"ServicePort", App.ServicePort, "10000"},
		{"StorageIP", App.StorageIP, "127.78.0.6"},
		{"StoragePort", App.StoragePort, "20000"},
		{"StorageProtocol", App.StorageProtocol, "http"},
		{"DocumentConversionIP", App.DocumentConversionIP, "127.78.0.6"},
		{"DocumentConversionPort", App.DocumentConversionPort, "20001"},
		{"DocumentConversionProtocol", App.DocumentConversionProtocol, "http"},
		// application (KV)
		{"ServiceEnableDocumentPreview", App.ServiceEnableDocumentPreview, true},
		{"ServiceEnableDocumentThumbnail", App.ServiceEnableDocumentThumbnail, false},
		// application (KV) — timeouts (HiddenFromDocs, but still KV-configurable)
		{"ServiceTimeoutInSeconds", App.ServiceTimeoutInSeconds, 30},
		{"ServiceDocsTimeout", App.ServiceDocsTimeout, 15},
		// env knobs (default values)
		{"RenderConcurrency", App.RenderConcurrency, runtime.NumCPU()},
		{"PDFWorkers", App.PDFWorkers, runtime.NumCPU()},
		{"VIPSConcurrency", App.VIPSConcurrency, 1},
		// hardcoded constants
		{"ImageMinimumResolution", App.ImageMinimumResolution, 80},
		{"StorageDownloadAPI", App.StorageDownloadAPI, "download"},
		{"StorageHealthCheck", App.StorageHealthCheck, "health/live"},
		{"DocumentConversionServiceEndpoint", App.DocumentConversionServiceEndpoint, "services/docs/editor"},
		{"DocumentConversionConvertAPI", App.DocumentConversionConvertAPI, "cool/convert-to"},
		// hardcoded routes
		{"ServiceName", App.ServiceName, "preview"},
		{"ServiceImageName", App.ServiceImageName, "image"},
		{"ServicePDFName", App.ServicePDFName, "pdf"},
		{"ServiceDocumentName", App.ServiceDocumentName, "document"},
		{"ServiceHealthName", App.ServiceHealthName, "health"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// TestPDFWorkersFallbackToCPUCount verifies that when pdf-workers is absent from
// all sources, PDFWorkers is set to runtime.NumCPU().
func TestPDFWorkersFallbackToCPUCount(t *testing.T) {
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.PDFWorkers != runtime.NumCPU() {
		t.Errorf("PDFWorkers = %d, want runtime.NumCPU() = %d", App.PDFWorkers, runtime.NumCPU())
	}
}

// TestRenderConcurrencyEnvOverride verifies that PREVIEW_RENDER_CONCURRENCY is honoured.
func TestRenderConcurrencyEnvOverride(t *testing.T) {
	t.Setenv("PREVIEW_RENDER_CONCURRENCY", "3")
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.RenderConcurrency != 3 {
		t.Errorf("RenderConcurrency = %d, want 3", App.RenderConcurrency)
	}
}

// TestPDFWorkersEnvOverride verifies that PREVIEW_PDF_WORKERS is honoured.
func TestPDFWorkersEnvOverride(t *testing.T) {
	t.Setenv("PREVIEW_PDF_WORKERS", "5")
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.PDFWorkers != 5 {
		t.Errorf("PDFWorkers = %d, want 5", App.PDFWorkers)
	}
}

// ─── hardcoded routes ─────────────────────────────────────────────────────────

// TestHardcodedRoutes verifies that route fields are always the hardcoded
// constants regardless of any env vars.
func TestHardcodedRoutes(t *testing.T) {
	// Attempt to set env vars that the old chain accepted; they must be ignored.
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_NAME", "changed")
	srv := buildKVServer(t, map[string]string{})
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	routes := map[string]string{
		"ServiceName":         App.ServiceName,
		"ServiceImageName":    App.ServiceImageName,
		"ServicePDFName":      App.ServicePDFName,
		"ServiceDocumentName": App.ServiceDocumentName,
		"ServiceHealthName":   App.ServiceHealthName,
	}
	want := map[string]string{
		"ServiceName":         "preview",
		"ServiceImageName":    "image",
		"ServicePDFName":      "pdf",
		"ServiceDocumentName": "document",
		"ServiceHealthName":   "health",
	}
	for k, v := range routes {
		if v != want[k] {
			t.Errorf("%s = %q, want %q (hardcoded)", k, v, want[k])
		}
	}
}

// ─── env overrides (standard names) ──────────────────────────────────────────

// TestEnvOverridesStorageHost verifies NETWORKING_CONFIG_CARBONIO_STORAGES_HOST
// overrides the storage IP.
func TestEnvOverridesStorageHost(t *testing.T) {
	t.Setenv("NETWORKING_CONFIG_CARBONIO_STORAGES_HOST", "10.0.0.1")
	t.Setenv("NETWORKING_CONFIG_CARBONIO_STORAGES_PORT", "9999")
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.StorageIP != "10.0.0.1" {
		t.Errorf("StorageIP = %q, want %q", App.StorageIP, "10.0.0.1")
	}
	if App.StoragePort != "9999" {
		t.Errorf("StoragePort = %q, want %q", App.StoragePort, "9999")
	}
	if App.StorageFullAddress != "http://10.0.0.1:9999" {
		t.Errorf("StorageFullAddress = %q, want %q", App.StorageFullAddress, "http://10.0.0.1:9999")
	}
}

// TestEnvOverridesServiceHost verifies NETWORKING_CONFIG_CARBONIO_SERVICE_HOST
// overrides the service IP.
func TestEnvOverridesServiceHost(t *testing.T) {
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_HOST", "192.168.1.1")
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_PORT", "8080")
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.ServiceIP != "192.168.1.1" {
		t.Errorf("ServiceIP = %q, want %q", App.ServiceIP, "192.168.1.1")
	}
	if App.ServicePort != "8080" {
		t.Errorf("ServicePort = %q, want %q", App.ServicePort, "8080")
	}
}

// TestEnvOverridesDocsEditorHost verifies NETWORKING_CONFIG_CARBONIO_DOCS_EDITOR_HOST
// overrides and derived DocumentConversion addresses are recomputed.
func TestEnvOverridesDocsEditorHost(t *testing.T) {
	t.Setenv("NETWORKING_CONFIG_CARBONIO_DOCS_EDITOR_HOST", "docs.internal")
	t.Setenv("NETWORKING_CONFIG_CARBONIO_DOCS_EDITOR_PORT", "7777")
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.DocumentConversionIP != "docs.internal" {
		t.Errorf("DocumentConversionIP = %q, want %q", App.DocumentConversionIP, "docs.internal")
	}
	if App.DocumentConversionPort != "7777" {
		t.Errorf("DocumentConversionPort = %q, want %q", App.DocumentConversionPort, "7777")
	}
	wantConvert := "http://docs.internal:7777/services/docs/editor/cool/convert-to"
	if App.DocumentConversionFullConvertAddress != wantConvert {
		t.Errorf("FullConvertAddress = %q, want %q", App.DocumentConversionFullConvertAddress, wantConvert)
	}
}

// TestStoragesTimeoutKVOverride verifies that the "timeout-in-seconds" Consul KV
// key overrides the default (30) and is reflected in ServiceTimeoutInSeconds.
func TestStoragesTimeoutKVOverride(t *testing.T) {
	srv := buildKVServer(t, map[string]string{
		"timeout-in-seconds": "60",
	})
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.ServiceTimeoutInSeconds != 60 {
		t.Errorf("ServiceTimeoutInSeconds = %d, want 60", App.ServiceTimeoutInSeconds)
	}
}

// TestDocsTimeoutKVOverride verifies that the "docs-timeout-in-seconds" Consul KV
// key overrides the default (15) and is reflected in ServiceDocsTimeout.
func TestDocsTimeoutKVOverride(t *testing.T) {
	srv := buildKVServer(t, map[string]string{
		"docs-timeout-in-seconds": "45",
	})
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.ServiceDocsTimeout != 45 {
		t.Errorf("ServiceDocsTimeout = %d, want 45", App.ServiceDocsTimeout)
	}
}

// TestTimeoutKVDefaultAppliedWhenAbsent verifies that when the KV server returns
// no timeout keys (404 → empty map), the registry defaults (30 / 15) are used.
func TestTimeoutKVDefaultAppliedWhenAbsent(t *testing.T) {
	srv := buildKVServer(t, nil) // 404 → empty application map
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.ServiceTimeoutInSeconds != 30 {
		t.Errorf("ServiceTimeoutInSeconds = %d, want 30 (registry default)", App.ServiceTimeoutInSeconds)
	}
	if App.ServiceDocsTimeout != 15 {
		t.Errorf("ServiceDocsTimeout = %d, want 15 (registry default)", App.ServiceDocsTimeout)
	}
}

// ─── properties-file override ─────────────────────────────────────────────────

// TestPropertiesFileOverride writes a config.properties file and verifies its
// values override the defaults for networking keys.
func TestPropertiesFileOverride(t *testing.T) {
	props := "carbonio.storages.host=5.6.7.8\ncarbonio.storages.port=22222\ncarbonio.storages.protocol=https\n"
	f := writeTemp(t, props)

	srv := buildKVServer(t, nil)
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST", host)
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_PORT", port)

	// Use resolveWith so we can point at our temp file.
	r, err := resolveWith(f)
	if err != nil {
		t.Fatalf("resolveWith: %v", err)
	}
	got, _ := r.Networking.Get("carbonio.storages.host")
	if got != "5.6.7.8" {
		t.Errorf("carbonio.storages.host = %q, want 5.6.7.8", got)
	}
	got, _ = r.Networking.Get("carbonio.storages.port")
	if got != "22222" {
		t.Errorf("carbonio.storages.port = %q, want 22222", got)
	}
	got, _ = r.Networking.Get("carbonio.storages.protocol")
	if got != "https" {
		t.Errorf("carbonio.storages.protocol = %q, want https", got)
	}
}

// ─── application KV override ─────────────────────────────────────────────────

// TestConsulKVOverride verifies that Consul KV entries for the remaining
// application flags are reflected in the populated Config.
func TestConsulKVOverride(t *testing.T) {
	srv2 := buildKVServer(t, map[string]string{
		"enable-document-preview":   "false",
		"enable-document-thumbnail": "true",
	})
	if err := loadWithConsul(t, srv2); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.ServiceEnableDocumentPreview != false {
		t.Errorf("ServiceEnableDocumentPreview = %v, want false", App.ServiceEnableDocumentPreview)
	}
	if App.ServiceEnableDocumentThumbnail != true {
		t.Errorf("ServiceEnableDocumentThumbnail = %v, want true", App.ServiceEnableDocumentThumbnail)
	}
}

// ─── derived addresses ────────────────────────────────────────────────────────

// TestDerivedAddresses verifies all derived addresses are assembled correctly.
func TestDerivedAddresses(t *testing.T) {
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"StorageFullAddress", App.StorageFullAddress, "http://127.78.0.6:20000"},
		{"DocumentConversionBaseAddress", App.DocumentConversionBaseAddress, "http://127.78.0.6:20001"},
		{"DocumentConversionFullServiceAddress", App.DocumentConversionFullServiceAddress, "http://127.78.0.6:20001/services/docs/editor/"},
		{"DocumentConversionFullConvertAddress", App.DocumentConversionFullConvertAddress, "http://127.78.0.6:20001/services/docs/editor/cool/convert-to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// ─── AreDocsEnabled ───────────────────────────────────────────────────────────

// TestAreDocsEnabled verifies the derived AreDocsEnabled flag.
func TestAreDocsEnabled(t *testing.T) {
	// default: preview=true, thumbnail=false → AreDocsEnabled=true (via preview)
	srv := buildKVServer(t, nil)
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !App.AreDocsEnabled {
		t.Error("expected AreDocsEnabled=true (defaults: preview=true, thumbnail=false)")
	}

	// Both disabled via KV
	srv2 := buildKVServer(t, map[string]string{
		"enable-document-preview":   "false",
		"enable-document-thumbnail": "false",
	})
	if err := loadWithConsul(t, srv2); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.AreDocsEnabled {
		t.Error("expected AreDocsEnabled=false when both flags are false")
	}

	// Only thumbnail enabled
	srv3 := buildKVServer(t, map[string]string{
		"enable-document-preview":   "false",
		"enable-document-thumbnail": "true",
	})
	if err := loadWithConsul(t, srv3); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !App.AreDocsEnabled {
		t.Error("expected AreDocsEnabled=true when only thumbnail enabled")
	}
}

// ─── parse-failure → error ────────────────────────────────────────────────────

// TestParseFailureKVTimeout verifies that a non-integer "timeout-in-seconds"
// Consul KV value causes Load() to return an error naming the key.
func TestParseFailureKVTimeout(t *testing.T) {
	srv := buildKVServer(t, map[string]string{
		"timeout-in-seconds": "not-a-number",
	})
	pointConsulAt(t, srv)
	err := Load()
	if err == nil {
		t.Fatal("expected error for bad timeout-in-seconds value, got nil")
	}
	if !strings.Contains(err.Error(), "timeout-in-seconds") {
		t.Errorf("error %q should mention key %q", err.Error(), "timeout-in-seconds")
	}
}

// TestParseFailureBoolKey verifies that an invalid boolean value causes an
// error naming the key.
func TestParseFailureBoolKey(t *testing.T) {
	srv := buildKVServer(t, map[string]string{
		"enable-document-preview": "yes",
	})
	pointConsulAt(t, srv)
	err := Load()
	if err == nil {
		t.Fatal("expected error for bad bool value, got nil")
	}
	const wantKey = "enable-document-preview"
	if !strings.Contains(err.Error(), wantKey) {
		t.Errorf("error %q should mention key %q", err.Error(), wantKey)
	}
}

// TestParseFailurePDFWorkers verifies that a bad PREVIEW_PDF_WORKERS value is an error.
func TestParseFailurePDFWorkers(t *testing.T) {
	t.Setenv("PREVIEW_PDF_WORKERS", "banana")
	srv := buildKVServer(t, nil)
	pointConsulAt(t, srv)
	err := Load()
	if err == nil {
		t.Fatal("expected error for bad PREVIEW_PDF_WORKERS value, got nil")
	}
	if !strings.Contains(err.Error(), "PREVIEW_PDF_WORKERS") {
		t.Errorf("error %q should mention PREVIEW_PDF_WORKERS", err.Error())
	}
}

// TestParseFailureZeroKVTimeout verifies that setting timeout KV keys to "0"
// (not a positive integer) causes Load() to return an error naming the key.
func TestParseFailureZeroKVTimeout(t *testing.T) {
	for _, tc := range []struct{ kvKey, name string }{
		{"timeout-in-seconds", "storages timeout"},
		{"docs-timeout-in-seconds", "docs timeout"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := buildKVServer(t, map[string]string{
				tc.kvKey: "0",
			})
			pointConsulAt(t, srv)
			err := Load()
			if err == nil {
				t.Fatalf("expected error for %s=0, got nil", tc.kvKey)
			}
			if !strings.Contains(err.Error(), tc.kvKey) {
				t.Errorf("error %q should mention key %q", err.Error(), tc.kvKey)
			}
		})
	}
}

// TestParseFailureNegativeRenderConcurrency verifies that PREVIEW_RENDER_CONCURRENCY=-1 is rejected.
func TestParseFailureNegativeRenderConcurrency(t *testing.T) {
	t.Setenv("PREVIEW_RENDER_CONCURRENCY", "-1")
	srv := buildKVServer(t, nil)
	pointConsulAt(t, srv)
	err := Load()
	if err == nil {
		t.Fatal("expected error for PREVIEW_RENDER_CONCURRENCY=-1, got nil")
	}
	if !strings.Contains(err.Error(), "PREVIEW_RENDER_CONCURRENCY") {
		t.Errorf("error %q should mention PREVIEW_RENDER_CONCURRENCY", err.Error())
	}
}

// TestParseFailureZeroPDFWorkers verifies that PREVIEW_PDF_WORKERS=0 is rejected.
func TestParseFailureZeroPDFWorkers(t *testing.T) {
	t.Setenv("PREVIEW_PDF_WORKERS", "0")
	srv := buildKVServer(t, nil)
	pointConsulAt(t, srv)
	err := Load()
	if err == nil {
		t.Fatal("expected error for PREVIEW_PDF_WORKERS=0, got nil")
	}
	if !strings.Contains(err.Error(), "PREVIEW_PDF_WORKERS") {
		t.Errorf("error %q should mention PREVIEW_PDF_WORKERS", err.Error())
	}
}

// ─── messages fix ─────────────────────────────────────────────────────────────

// TestMessages verifies that the hard-coded messages match the Python spec exactly.
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

// TestMessagesIniOverrideWithRealKeys writes a temp messages.ini using the ACTUAL
// lowercase key names from package/preview/messages.ini and verifies that
// overrideMessages picks them up correctly.
func TestMessagesIniOverrideWithRealKeys(t *testing.T) {
	const iniContent = `[hard_errors]
storage_unavailable_string = OVERRIDDEN storage unavailable
carbonio_docs_editor_not_running = OVERRIDDEN docs editor unavailable

[validation]
number_of_pages_not_valid_error = OVERRIDDEN pages not valid
height_or_width_not_valid_error = OVERRIDDEN hw not valid
`
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "messages.ini")
	if err := os.WriteFile(iniPath, []byte(iniContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Point the search path at our temp file for this test.
	orig := messagesSearchPaths
	messagesSearchPaths = []string{iniPath}
	t.Cleanup(func() { messagesSearchPaths = orig })

	m := loadMessages()

	if m.StorageUnavailable != "OVERRIDDEN storage unavailable" {
		t.Errorf("StorageUnavailable = %q, want overridden value", m.StorageUnavailable)
	}
	if m.DocsEditorUnavailable != "OVERRIDDEN docs editor unavailable" {
		t.Errorf("DocsEditorUnavailable = %q, want overridden value", m.DocsEditorUnavailable)
	}
	if m.NumberOfPagesNotValid != "OVERRIDDEN pages not valid" {
		t.Errorf("NumberOfPagesNotValid = %q, want overridden value", m.NumberOfPagesNotValid)
	}
	if m.HeightWidthNotValid != "OVERRIDDEN hw not valid" {
		t.Errorf("HeightWidthNotValid = %q, want overridden value", m.HeightWidthNotValid)
	}
	// Unoverridden field must stay default.
	if m.ItemNotFound != "Requested item was not found in the storage." {
		t.Errorf("ItemNotFound = %q, want default", m.ItemNotFound)
	}
}

// ─── accessor guard ───────────────────────────────────────────────────────────

// TestAccessorPanicsBeforeLoad verifies that calling Networking() or
// Application() before Load() panics with the documented message.
func TestAccessorPanicsBeforeLoad(t *testing.T) {
	// Reset the loaded flag for this test.
	orig := loaded
	loaded = false
	t.Cleanup(func() { loaded = orig })

	for _, name := range []string{"Networking", "Application"} {
		name := name
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s(): expected panic, got none", name)
				}
				msg, ok := r.(string)
				if !ok || msg != "config: accessor called before Load()" {
					t.Fatalf("%s(): unexpected panic value: %v", name, r)
				}
			}()
			if name == "Networking" {
				Networking()
			} else {
				Application()
			}
		})
	}
}

// TestAccessorWorksAfterLoad verifies that Networking() and Application()
// return non-zero FrozenMaps after a successful Load().
func TestAccessorWorksAfterLoad(t *testing.T) {
	srv := buildKVServer(t, nil) // 404 → empty application map
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// After Load(), Networking() must contain at least one registered key.
	net := Networking()
	if _, ok := net.Get("carbonio.service.host"); !ok {
		t.Error("Networking(): carbonio.service.host missing after Load()")
	}

	// Application() must also be accessible without panicking.
	_ = Application()
}

func TestCacheMaxMBDefault(t *testing.T) {
	// No KV value → registry default "256" → 256 MiB.
	srv := buildKVServer(t, nil) // 404 → empty application map
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	want := int64(256) * 1024 * 1024
	if App.CacheMaxBytes != want {
		t.Errorf("CacheMaxBytes = %d, want %d", App.CacheMaxBytes, want)
	}
}

func TestCacheMaxMBKVOverride(t *testing.T) {
	srv := buildKVServer(t, map[string]string{"cache-max-mb": "512"})
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	want := int64(512) * 1024 * 1024
	if App.CacheMaxBytes != want {
		t.Errorf("CacheMaxBytes = %d, want %d", App.CacheMaxBytes, want)
	}
}

func TestCacheMaxMBZeroDisables(t *testing.T) {
	srv := buildKVServer(t, map[string]string{"cache-max-mb": "0"})
	if err := loadWithConsul(t, srv); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if App.CacheMaxBytes != 0 {
		t.Errorf("CacheMaxBytes = %d, want 0 (disabled)", App.CacheMaxBytes)
	}
}

// resolvedWithApp builds a Resolved whose Application map holds the given pairs.
// White-box helper for testing the appNonNegativeInt accessor directly.
func resolvedWithApp(pairs map[string]string) Resolved {
	return Resolved{Application: FrozenMap{m: pairs}}
}

// TestAppNonNegativeInt_PriorError verifies a non-nil prior error short-circuits.
func TestAppNonNegativeInt_PriorError(t *testing.T) {
	prior := errSentinel("boom")
	n, err := appNonNegativeInt(resolvedWithApp(map[string]string{"k": "5"}), "k", prior)
	if n != 0 || err != prior {
		t.Errorf("got (%d, %v), want (0, prior error)", n, err)
	}
}

// TestAppNonNegativeInt_AbsentOrBlank verifies absent/blank → (0, nil).
func TestAppNonNegativeInt_AbsentOrBlank(t *testing.T) {
	if n, err := appNonNegativeInt(resolvedWithApp(map[string]string{}), "missing", nil); n != 0 || err != nil {
		t.Errorf("absent: got (%d, %v), want (0, nil)", n, err)
	}
	if n, err := appNonNegativeInt(resolvedWithApp(map[string]string{"k": ""}), "k", nil); n != 0 || err != nil {
		t.Errorf("blank: got (%d, %v), want (0, nil)", n, err)
	}
}

// TestAppNonNegativeInt_Valid verifies a present non-negative value parses.
func TestAppNonNegativeInt_Valid(t *testing.T) {
	n, err := appNonNegativeInt(resolvedWithApp(map[string]string{"k": "42"}), "k", nil)
	if n != 42 || err != nil {
		t.Errorf("got (%d, %v), want (42, nil)", n, err)
	}
}

// TestAppNonNegativeInt_Negative verifies a negative value is a fail-fast error.
func TestAppNonNegativeInt_Negative(t *testing.T) {
	_, err := appNonNegativeInt(resolvedWithApp(map[string]string{"k": "-3"}), "k", nil)
	if err == nil {
		t.Fatal("want error for negative value, got nil")
	}
	if !strings.Contains(err.Error(), "non-negative integer") {
		t.Errorf("error = %v, want it to mention non-negative integer", err)
	}
}

// TestAppNonNegativeInt_NonInteger verifies a non-integer value is an error.
func TestAppNonNegativeInt_NonInteger(t *testing.T) {
	_, err := appNonNegativeInt(resolvedWithApp(map[string]string{"k": "abc"}), "k", nil)
	if err == nil {
		t.Fatal("want error for non-integer value, got nil")
	}
}

// errSentinel is a trivial error type for the prior-error short-circuit test.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }
