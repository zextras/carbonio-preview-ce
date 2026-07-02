// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "testing"

func TestEnvName(t *testing.T) {
	cases := []struct {
		prefix string
		key    string
		want   string
	}{
		{
			prefix: NetworkingPrefix,
			key:    "carbonio.service.host",
			want:   "NETWORKING_CONFIG_CARBONIO_SERVICE_HOST",
		},
		{
			prefix: NetworkingPrefix,
			key:    "carbonio.service-discover.host",
			want:   "NETWORKING_CONFIG_CARBONIO_SERVICE_DISCOVER_HOST",
		},
		{
			prefix: ApplicationPrefix,
			key:    "timeout-in-seconds",
			want:   "APPLICATION_CONFIG_TIMEOUT_IN_SECONDS",
		},
		{
			prefix: ApplicationPrefix,
			key:    "storages.download-api",
			want:   "APPLICATION_CONFIG_STORAGES_DOWNLOAD_API",
		},
	}

	for _, tc := range cases {
		got := EnvName(tc.prefix, tc.key)
		if got != tc.want {
			t.Errorf("EnvName(%q, %q) = %q, want %q", tc.prefix, tc.key, got, tc.want)
		}
	}
}

func TestKvPath(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"storages.download-api", "carbonio-preview/storages/download-api"},
		{"timeout-in-seconds", "carbonio-preview/timeout-in-seconds"},
		{"docs-editor.service-endpoint", "carbonio-preview/docs-editor/service-endpoint"},
	}

	for _, tc := range cases {
		got := KvPath(tc.key)
		if got != tc.want {
			t.Errorf("KvPath(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestKvPathRoundTrip(t *testing.T) {
	// Verify that the suffix of the KV path, with / → . reverts to the original key.
	keys := []string{
		"storages.download-api",
		"timeout-in-seconds",
		"docs-editor.service-endpoint",
		"workers",
	}

	prefix := ServiceName + "/"
	for _, key := range keys {
		path := KvPath(key)
		// strip prefix
		suffix := path[len(prefix):]
		// convert back: / → .
		recovered := kvSuffixToKey(suffix)
		if recovered != key {
			t.Errorf("round-trip for %q: KvPath=%q suffix=%q recovered=%q", key, path, suffix, recovered)
		}
	}
}

func TestServiceName(t *testing.T) {
	if ServiceName != "carbonio-preview" {
		t.Errorf("ServiceName = %q, want %q", ServiceName, "carbonio-preview")
	}
}

func TestShortName(t *testing.T) {
	if ShortName != "preview" {
		t.Errorf("ShortName = %q, want %q", ShortName, "preview")
	}
}

func TestNetworkingFilePath(t *testing.T) {
	if NetworkingFilePath != "/etc/carbonio/preview/config.properties" {
		t.Errorf("NetworkingFilePath = %q, want %q", NetworkingFilePath, "/etc/carbonio/preview/config.properties")
	}
}
