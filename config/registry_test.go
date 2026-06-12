// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

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
		"enable-document-preview",
		"enable-document-thumbnail",
		"timeout-in-seconds",
		"docs-timeout-in-seconds",
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

	// Verify HiddenFromDocs is set correctly.
	hiddenExpected := map[string]bool{
		"enable-document-preview":   false,
		"enable-document-thumbnail": false,
		"timeout-in-seconds":        true,
		"docs-timeout-in-seconds":   true,
	}
	for k, wantHidden := range hiddenExpected {
		e, ok := idx[k]
		if !ok {
			continue // already reported above
		}
		if e.HiddenFromDocs != wantHidden {
			t.Errorf("application key %q HiddenFromDocs = %v, want %v", k, e.HiddenFromDocs, wantHidden)
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
		{"enable-document-preview", "true", ""},
		{"enable-document-thumbnail", "false", ""},
		{"timeout-in-seconds", "30", ""},
		{"docs-timeout-in-seconds", "15", ""},
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

func TestAllKeysAreASCII(t *testing.T) {
	for _, e := range RegisteredKeys() {
		for _, s := range []string{e.Key, e.Default, e.IfNotPresent} {
			for i := 0; i < len(s); i++ {
				if s[i] > 127 {
					t.Errorf("non-ASCII byte 0x%02x at index %d in %q (key=%q)", s[i], i, s, e.Key)
				}
			}
		}
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

// TestRegisteredKeysSortOrder pins the exact contract of RegisteredKeys:
//   - networking namespace comes before application namespace
//   - within each namespace keys are sorted alphabetically by Key
func TestRegisteredKeysSortOrder(t *testing.T) {
	keys := RegisteredKeys()

	// Find the boundary between namespaces.
	firstAppIdx := -1
	for i, k := range keys {
		if k.Namespace == NamespaceApplication {
			firstAppIdx = i
			break
		}
	}
	if firstAppIdx < 0 {
		t.Fatal("no application-namespace key found in RegisteredKeys output")
	}

	// All entries before the boundary must be networking.
	for i := 0; i < firstAppIdx; i++ {
		if keys[i].Namespace != NamespaceNetworking {
			t.Errorf("index %d: expected NamespaceNetworking, got %q (key=%q)", i, keys[i].Namespace, keys[i].Key)
		}
	}
	// All entries from the boundary onward must be application.
	for i := firstAppIdx; i < len(keys); i++ {
		if keys[i].Namespace != NamespaceApplication {
			t.Errorf("index %d: expected NamespaceApplication, got %q (key=%q)", i, keys[i].Namespace, keys[i].Key)
		}
	}

	// Networking section: alphabetical by Key.
	for i := 1; i < firstAppIdx; i++ {
		if keys[i].Key < keys[i-1].Key {
			t.Errorf("networking keys out of order: %q before %q", keys[i-1].Key, keys[i].Key)
		}
	}

	// Application section: alphabetical by Key.
	for i := firstAppIdx + 1; i < len(keys); i++ {
		if keys[i].Key < keys[i-1].Key {
			t.Errorf("application keys out of order: %q before %q", keys[i-1].Key, keys[i].Key)
		}
	}

	// Spot-check: known networking keys that must appear in order.
	netExpected := []string{
		"carbonio.docs-editor.host",
		"carbonio.docs-editor.port",
		"carbonio.docs-editor.protocol",
		"carbonio.service-discover.host",
		"carbonio.service-discover.port",
		"carbonio.service.host",
		"carbonio.service.port",
		"carbonio.storages.host",
		"carbonio.storages.port",
		"carbonio.storages.protocol",
	}
	netKeys := keys[:firstAppIdx]
	for i, want := range netExpected {
		if i >= len(netKeys) {
			t.Errorf("networking section too short: missing key at index %d (want %q)", i, want)
			break
		}
		if netKeys[i].Key != want {
			t.Errorf("networking[%d] = %q, want %q", i, netKeys[i].Key, want)
		}
	}

	// Spot-check: known application keys that must appear in alphabetical order.
	// Order: docs-timeout-in-seconds < enable-document-preview < enable-document-thumbnail < timeout-in-seconds
	appExpected := []string{
		"docs-timeout-in-seconds",
		"enable-document-preview",
		"enable-document-thumbnail",
		"timeout-in-seconds",
	}
	appKeys := keys[firstAppIdx:]
	for i, want := range appExpected {
		if i >= len(appKeys) {
			t.Errorf("application section too short: missing key at index %d (want %q)", i, want)
			break
		}
		if appKeys[i].Key != want {
			t.Errorf("application[%d] = %q, want %q", i, appKeys[i].Key, want)
		}
	}
}
