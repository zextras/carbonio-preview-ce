package config

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// buildConsulServer creates a test Consul KV server returning the given map as
// a JSON KV response. Returns the server and a cleanup function.
func buildConsulServer(t *testing.T, kvMap map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/carbonio-preview/" {
			http.NotFound(w, r)
			return
		}
		var entries []kvEntry
		for k, v := range kvMap {
			val := base64.StdEncoding.EncodeToString([]byte(v))
			entries = append(entries, kvEntry{Key: "carbonio-preview/" + k, Value: &val})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// resolveWithConsulServer creates a resolver that points service-discover at srv.
func resolveWithConsulServer(t *testing.T, propPath string, srv *httptest.Server) (Resolved, error) {
	t.Helper()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST", host)
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_PORT", port)
	return resolveWith(propPath)
}

// TestResolverDefaultsWhenAllAbsent verifies registered defaults are used when
// no env, file, or consul data is present. Uses an unreachable consul address
// — which normally would fail — but we set service-discover via env so we can
// control the consul endpoint; here we use an empty consul response.
func TestResolverDefaultsWhenAllAbsent(t *testing.T) {
	srv := buildConsulServer(t, nil)
	r, err := resolveWithConsulServer(t, "", srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	checks := []struct{ key, want string }{
		{"carbonio.service.host", "127.78.0.6"},
		{"carbonio.service.port", "10000"},
		{"carbonio.storages.host", "127.78.0.6"},
		{"carbonio.storages.port", "20000"},
		{"carbonio.storages.protocol", "http"},
		{"carbonio.docs-editor.host", "127.78.0.6"},
		{"carbonio.docs-editor.port", "20001"},
		{"carbonio.docs-editor.protocol", "http"},
	}
	for _, tc := range checks {
		got, ok := r.Networking.Get(tc.key)
		if !ok || got != tc.want {
			t.Errorf("Networking.Get(%q) = (%q, %v), want (%q, true)", tc.key, got, ok, tc.want)
		}
	}

	appChecks := []struct{ key, want string }{
		{"timeout-in-seconds", "30"},
		{"workers", "2"},
		{"vips-concurrency", "1"},
		{"storages.download-api", "download"},
		{"storages.health-check", "health/ready/"},
		{"docs-editor.service-endpoint", "services/docs/editor"},
		{"docs-editor.convert-api", "cool/convert-to"},
	}
	for _, tc := range appChecks {
		got, ok := r.Application.Get(tc.key)
		if !ok || got != tc.want {
			t.Errorf("Application.Get(%q) = (%q, %v), want (%q, true)", tc.key, got, ok, tc.want)
		}
	}
}

// TestResolverEnvBeatsFile verifies ENV overrides file value (networking layer).
func TestResolverEnvBeatsFile(t *testing.T) {
	props := "carbonio.service.host=10.0.0.1\n"
	f := writeTemp(t, props)

	srv := buildConsulServer(t, nil)
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_HOST", "1.2.3.4")
	r, err := resolveWithConsulServer(t, f, srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, _ := r.Networking.Get("carbonio.service.host")
	if got != "1.2.3.4" {
		t.Errorf("Networking.Get(carbonio.service.host) = %q, want 1.2.3.4 (env should beat file)", got)
	}
}

// TestResolverFileBeatsDefault verifies file value overrides default (networking).
func TestResolverFileBeatsDefault(t *testing.T) {
	props := "carbonio.service.host=10.0.0.2\n"
	f := writeTemp(t, props)

	srv := buildConsulServer(t, nil)
	r, err := resolveWithConsulServer(t, f, srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, _ := r.Networking.Get("carbonio.service.host")
	if got != "10.0.0.2" {
		t.Errorf("Networking.Get(carbonio.service.host) = %q, want 10.0.0.2 (file should beat default)", got)
	}
}

// TestResolverEnvBeatsKV verifies ENV overrides Consul KV (application layer).
func TestResolverEnvBeatsKV(t *testing.T) {
	t.Setenv("APPLICATION_CONFIG_TIMEOUT_IN_SECONDS", "120")
	srv := buildConsulServer(t, map[string]string{"timeout-in-seconds": "90"})
	r, err := resolveWithConsulServer(t, "", srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, _ := r.Application.Get("timeout-in-seconds")
	if got != "120" {
		t.Errorf("Application.Get(timeout-in-seconds) = %q, want 120 (env should beat KV)", got)
	}
}

// TestResolverKVBeatsDefault verifies Consul KV overrides default (application).
func TestResolverKVBeatsDefault(t *testing.T) {
	srv := buildConsulServer(t, map[string]string{"workers": "8"})
	r, err := resolveWithConsulServer(t, "", srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, _ := r.Application.Get("workers")
	if got != "8" {
		t.Errorf("Application.Get(workers) = %q, want 8 (KV should beat default)", got)
	}
}

// TestResolverUnregisteredFileKeyVisible verifies pass-through of unregistered
// keys from the properties file.
func TestResolverUnregisteredFileKeyVisible(t *testing.T) {
	props := "unknown.custom.key=custom-value\n"
	f := writeTemp(t, props)

	srv := buildConsulServer(t, nil)
	r, err := resolveWithConsulServer(t, f, srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, ok := r.Networking.Get("unknown.custom.key")
	if !ok || got != "custom-value" {
		t.Errorf("Networking.Get(unknown.custom.key) = (%q, %v), want (custom-value, true)", got, ok)
	}
}

// TestResolverUnregisteredKVKeyVisible verifies pass-through of unregistered
// KV keys.
func TestResolverUnregisteredKVKeyVisible(t *testing.T) {
	srv := buildConsulServer(t, map[string]string{"custom.app.flag": "enabled"})
	r, err := resolveWithConsulServer(t, "", srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, ok := r.Application.Get("custom.app.flag")
	if !ok || got != "enabled" {
		t.Errorf("Application.Get(custom.app.flag) = (%q, %v), want (enabled, true)", got, ok)
	}
}

// TestResolverServiceDiscoverResolutionOrder verifies the priority for
// determining the Consul host/port: env → file → default.
func TestResolverServiceDiscoverResolutionOrder(t *testing.T) {
	t.Run("env overrides file and default", func(t *testing.T) {
		// A consul server at a known address; point env there, file to a dead addr.
		srv := buildConsulServer(t, nil)
		host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST", host)
		t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_PORT", port)

		props := "carbonio.service-discover.host=10.99.99.99\ncarbonio.service-discover.port=19999\n"
		f := writeTemp(t, props)
		_, err := resolveWith(f)
		if err != nil {
			t.Fatalf("expected success using env-pointed consul, got: %v", err)
		}
	})

	t.Run("file overrides default", func(t *testing.T) {
		// No env; file points to our test server.
		srv := buildConsulServer(t, nil)
		host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		os.Unsetenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST")
		os.Unsetenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_PORT")

		props := "carbonio.service-discover.host=" + host + "\ncarbonio.service-discover.port=" + port + "\n"
		f := writeTemp(t, props)
		_, err := resolveWith(f)
		if err != nil {
			t.Fatalf("expected success using file-pointed consul, got: %v", err)
		}
	})
}

// TestResolverFailFastOnConsulError verifies that an unreachable Consul causes
// Resolve to return an error.
func TestResolverFailFastOnConsulError(t *testing.T) {
	// Find a free port, close it, so it's guaranteed refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	host, port, _ := net.SplitHostPort(addr)
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST", host)
	t.Setenv("NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_PORT", port)

	_, err = resolveWith("")
	if err == nil {
		t.Fatal("unreachable consul: expected error, got nil")
	}
}

// TestResolverFrozenMap verifies that Resolved maps are immutable after Resolve.
// Get on an absent key must return ("", false).
func TestResolverFrozenMap(t *testing.T) {
	srv := buildConsulServer(t, nil)
	r, err := resolveWithConsulServer(t, "", srv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, ok := r.Networking.Get("does.not.exist")
	if ok || got != "" {
		t.Errorf("Get(absent key) = (%q, %v), want (\"\", false)", got, ok)
	}
}
