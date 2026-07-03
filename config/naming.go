// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "strings"

// Service-level constants mirroring carbonio-quarkus-extensions naming rules.
const (
	// ServiceName is the canonical Carbonio service name.
	ServiceName = "carbonio-preview"

	// ShortName is derived by stripping the leading "carbonio-" prefix.
	ShortName = "preview"

	// NetworkingPrefix is the config key prefix for networking-layer entries.
	NetworkingPrefix = "networking-config."

	// ApplicationPrefix is the config key prefix for application-layer entries.
	ApplicationPrefix = "application-config."

	// NetworkingFilePath is the path to the operator-supplied networking
	// properties file on-disk.
	NetworkingFilePath = "/etc/carbonio/preview/config.properties"
)

// EnvName returns the environment variable name for a given prefix and key.
// It concatenates prefix+key, uppercases the result, and replaces every '.'
// and '-' with '_'.
//
// NOTE: this mapping is lossy — both '.' and '-' collapse to '_', so distinct
// keys such as "a.b-c" and "a.b.c" produce the same env var name.
//
// Examples:
//
//	EnvName("networking-config.", "carbonio.service.host")
//	  → "NETWORKING_CONFIG_CARBONIO_SERVICE_HOST"
//
//	EnvName("application-config.", "storage.fetch-timeout-seconds")
//	  → "APPLICATION_CONFIG_STORAGE_FETCH_TIMEOUT_SECONDS"
func EnvName(prefix, key string) string {
	combined := prefix + key
	combined = strings.ToUpper(combined)
	combined = strings.ReplaceAll(combined, ".", "_")
	combined = strings.ReplaceAll(combined, "-", "_")
	return combined
}

// KvPath converts a dot-notation application key to its Consul KV path.
// The path is prefixed with "carbonio-preview/" and every '.' is replaced
// with '/'.
//
// Example: "storages.download-api" → "carbonio-preview/storages/download-api"
func KvPath(key string) string {
	return ServiceName + "/" + strings.ReplaceAll(key, ".", "/")
}

// kvSuffixToKey is the inverse of the key→suffix part of KvPath.
// It replaces every '/' with '.' in a KV path suffix to recover the
// original dot-notation key.
func kvSuffixToKey(suffix string) string {
	return strings.ReplaceAll(suffix, "/", ".")
}
