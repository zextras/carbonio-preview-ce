package config

import (
	"strings"
	"testing"
)

func TestRegisterKeyDuplicate(t *testing.T) {
	// Duplicate key+namespace must return an error.
	err := RegisterKey(KeyEntry{
		Key:       "carbonio.service.host",
		Namespace: NamespaceNetworking,
		Default:   "1.2.3.4",
	})
	if err == nil {
		t.Error("RegisterKey duplicate: expected error, got nil")
	}
}

func TestRegisteredCENetworkingKeys(t *testing.T) {
	keys := registeredKeys(NamespaceNetworking)
	required := []string{
		"carbonio.service.host",
		"carbonio.service.port",
		"carbonio.service-discover.host",
		"carbonio.service-discover.port",
		"carbonio.storages.host",
		"carbonio.storages.port",
		"carbonio.storages.protocol",
		"carbonio.docs-editor.host",
		"carbonio.docs-editor.port",
		"carbonio.docs-editor.protocol",
	}

	idx := make(map[string]KeyEntry, len(keys))
	for _, e := range keys {
		idx[e.Key] = e
	}

	for _, k := range required {
		if _, ok := idx[k]; !ok {
			t.Errorf("networking key %q not registered", k)
		}
	}
}

func TestRegisteredCEApplicationKeys(t *testing.T) {
	keys := registeredKeys(NamespaceApplication)
	required := []string{
		"timeout-in-seconds",
		"docs-timeout-in-seconds",
		"workers",
		"pdf-workers",
		"vips-concurrency",
		"image-minimum-resolution",
		"enable-document-preview",
		"enable-document-thumbnail",
		"storages.download-api",
		"storages.health-check",
		"docs-editor.service-endpoint",
		"docs-editor.convert-api",
	}

	idx := make(map[string]KeyEntry, len(keys))
	for _, e := range keys {
		idx[e.Key] = e
	}

	for _, k := range required {
		if _, ok := idx[k]; !ok {
			t.Errorf("application key %q not registered", k)
		}
	}
}

func TestKeyDefaults(t *testing.T) {
	netKeys := registeredKeys(NamespaceNetworking)
	netIdx := make(map[string]KeyEntry, len(netKeys))
	for _, e := range netKeys {
		netIdx[e.Key] = e
	}

	appKeys := registeredKeys(NamespaceApplication)
	appIdx := make(map[string]KeyEntry, len(appKeys))
	for _, e := range appKeys {
		appIdx[e.Key] = e
	}

	netChecks := []struct{ key, want string }{
		{"carbonio.service.host", "127.78.0.6"},
		{"carbonio.service.port", "10000"},
		{"carbonio.service-discover.host", "127.0.0.1"},
		{"carbonio.service-discover.port", "8500"},
		{"carbonio.storages.host", "127.78.0.6"},
		{"carbonio.storages.port", "20000"},
		{"carbonio.storages.protocol", "http"},
		{"carbonio.docs-editor.host", "127.78.0.6"},
		{"carbonio.docs-editor.port", "20001"},
		{"carbonio.docs-editor.protocol", "http"},
	}
	for _, tc := range netChecks {
		e, ok := netIdx[tc.key]
		if !ok {
			t.Errorf("networking key %q not found", tc.key)
			continue
		}
		if e.Default != tc.want {
			t.Errorf("networking key %q default = %q, want %q", tc.key, e.Default, tc.want)
		}
	}

	appChecks := []struct{ key, dflt, ifNotPresent string }{
		{"timeout-in-seconds", "30", ""},
		{"docs-timeout-in-seconds", "15", ""},
		{"workers", "2", ""},
		{"pdf-workers", "", "Defaults to the number of CPUs"},
		{"vips-concurrency", "1", ""},
		{"image-minimum-resolution", "80", ""},
		{"enable-document-preview", "true", ""},
		{"enable-document-thumbnail", "false", ""},
		{"storages.download-api", "download", ""},
		{"storages.health-check", "health/live", ""},
		{"docs-editor.service-endpoint", "services/docs/editor", ""},
		{"docs-editor.convert-api", "cool/convert-to", ""},
	}
	for _, tc := range appChecks {
		e, ok := appIdx[tc.key]
		if !ok {
			t.Errorf("application key %q not found", tc.key)
			continue
		}
		if e.Default != tc.dflt {
			t.Errorf("application key %q default = %q, want %q", tc.key, e.Default, tc.dflt)
		}
		if tc.ifNotPresent != "" && e.IfNotPresent != tc.ifNotPresent {
			t.Errorf("application key %q IfNotPresent = %q, want %q", tc.key, e.IfNotPresent, tc.ifNotPresent)
		}
	}
}

func TestRegisterKeyEnvNameCollision(t *testing.T) {
	// "a.b-c" and "a.b.c" both map to APPLICATION_CONFIG_A_B_C via EnvName,
	// so registering the second must fail with an actionable error.
	ns := NamespaceApplication
	first := KeyEntry{Key: "a.b-c", Namespace: ns, Default: "v1", Description: "test key one"}
	second := KeyEntry{Key: "a.b.c", Namespace: ns, Default: "v2", Description: "test key two"}

	if err := RegisterKey(first); err != nil {
		t.Fatalf("registering first key: %v", err)
	}
	t.Cleanup(func() {
		// Remove the test keys so they don't pollute other tests.
		registryMu.Lock()
		defer registryMu.Unlock()
		filtered := registry[:0]
		for _, e := range registry {
			if e.Key != "a.b-c" && e.Key != "a.b.c" {
				filtered = append(filtered, e)
			}
		}
		registry = filtered
	})

	err := RegisterKey(second)
	if err == nil {
		t.Fatal("expected env-name collision error, got nil")
	}
	if !strings.Contains(err.Error(), "a.b-c") || !strings.Contains(err.Error(), "a.b.c") {
		t.Errorf("collision error should name both keys; got: %v", err)
	}
}

func TestAllKeysHaveDescription(t *testing.T) {
	for _, ns := range []Namespace{NamespaceNetworking, NamespaceApplication} {
		for _, e := range registeredKeys(ns) {
			if e.Description == "" {
				t.Errorf("key %q (ns=%s) has no description", e.Key, ns)
			}
		}
	}
}
