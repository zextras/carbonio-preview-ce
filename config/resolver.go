// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"log/slog"
	"os"
)

// FrozenMap is an immutable snapshot of resolved key→value pairs.
// Callers may only read from it via Get; the underlying map is never exposed.
type FrozenMap struct {
	m map[string]string
}

// Get returns the value associated with key and true if the key is present, or
// ("", false) if it is absent.
func (f FrozenMap) Get(key string) (string, bool) {
	v, ok := f.m[key]
	return v, ok
}

// Resolved holds the two frozen configuration maps produced by Resolve.
type Resolved struct {
	// Networking contains all networking-layer keys (from properties file +
	// env + registry defaults).
	Networking FrozenMap

	// Application contains all application-layer keys (from Consul KV +
	// env + registry defaults).
	Application FrozenMap
}

// Resolve loads the networking properties file from the canonical path
// (NetworkingFilePath), fetches Consul KV, and resolves every registered key
// according to the priority chain: ENV > source (file/KV) > registry default.
//
// The service-discover host and port used to reach Consul are resolved with
// the same priority: ENV > properties file > registry default.
//
// Consul errors propagate immediately (fail-fast contract).
// A missing properties file is treated as an empty map (not an error).
func Resolve() (Resolved, error) {
	return resolveWith(NetworkingFilePath)
}

// resolveWith is the testable implementation of Resolve. It accepts an
// explicit properties file path so tests can supply a temp file or empty
// string (treated as missing).
func resolveWith(propPath string) (Resolved, error) {
	// Step 1 — load networking properties file.
	var fileValues map[string]string
	if propPath == "" {
		fileValues = map[string]string{}
	} else {
		var err error
		fileValues, err = readPropertiesFile(propPath)
		if err != nil {
			return Resolved{}, err
		}
	}

	// Step 2 — determine service-discover coordinates.
	//   Priority: env > file > registry default.
	sdHost := resolveServiceDiscoverCoord(
		EnvName(NetworkingPrefix, "carbonio.service-discover.host"),
		"carbonio.service-discover.host",
		"127.0.0.1",
		fileValues,
	)
	sdPort := resolveServiceDiscoverCoord(
		EnvName(NetworkingPrefix, "carbonio.service-discover.port"),
		"carbonio.service-discover.port",
		"8500",
		fileValues,
	)

	// Step 3 — fetch Consul KV (application layer).
	kvValues, err := fetchConsulKV(sdHost, sdPort)
	if err != nil {
		return Resolved{}, err
	}

	// Step 4 — build frozen networking map.
	netMap := buildFrozenMap(NamespaceNetworking, NetworkingPrefix, fileValues)

	// Step 5 — build frozen application map.
	appMap := buildFrozenMap(NamespaceApplication, ApplicationPrefix, kvValues)

	slog.Debug("config resolver: loaded layers",
		"consul_host", sdHost,
		"consul_port", sdPort,
		"file_keys", len(fileValues),
		"kv_keys", len(kvValues),
		"net_keys", len(netMap),
		"app_keys", len(appMap),
	)

	return Resolved{
		Networking:  FrozenMap{m: netMap},
		Application: FrozenMap{m: appMap},
	}, nil
}

// resolveServiceDiscoverCoord returns the effective value for a single
// service-discover coordinate (host or port).
//
//   - envVar: name of the environment variable to check first.
//   - fileKey: key to look up in fileValues.
//   - defaultVal: registry hard-coded default.
func resolveServiceDiscoverCoord(envVar, fileKey, defaultVal string, fileValues map[string]string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if v, ok := fileValues[fileKey]; ok && v != "" {
		return v
	}
	return defaultVal
}

// buildFrozenMap assembles the final key→value map for a namespace by applying
// the priority chain:
//
//  1. ENV (via EnvName)
//  2. Source values (fileValues for networking, kvValues for application)
//  3. Registry default (only if non-empty)
//
// Unregistered keys present in sourceValues are also included (pass-through).
func buildFrozenMap(ns Namespace, prefix string, sourceValues map[string]string) map[string]string {
	result := make(map[string]string)

	// Pass-through: include everything from sourceValues that is not registered
	// (registered keys are handled below with full priority logic).
	// Blank values are skipped: extensions parity requires blank = absent at
	// every layer, so an unregistered key with an empty value must not appear
	// in the frozen map (Get returns "", false).
	regMap := registeredKeyMap(ns)
	for k, v := range sourceValues {
		if _, registered := regMap[k]; !registered && v != "" {
			result[k] = v
		}
	}

	// Registered keys: apply priority chain.
	for key, entry := range regMap {
		envVar := EnvName(prefix, key)

		if v := os.Getenv(envVar); v != "" {
			result[key] = v
			slog.Debug("config: key resolved", "key", key, "source", "env")
			continue
		}
		if v, ok := sourceValues[key]; ok && v != "" {
			result[key] = v
			slog.Debug("config: key resolved", "key", key, "source", "file/kv")
			continue
		}
		if entry.Default != "" {
			result[key] = entry.Default
			slog.Debug("config: key resolved", "key", key, "source", "registry-default")
		}
		// Keys whose default is "" and that have no env/source value are
		// intentionally absent from the map (as in extensions).
	}

	return result
}
