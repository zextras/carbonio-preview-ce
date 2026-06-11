// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package config provides the configuration chain and key registry.
//
//go:generate go run ../cmd/configdocs
package config

import (
	"fmt"
	"sort"
	"sync"
)

// Namespace distinguishes which configuration layer a key belongs to.
type Namespace string

const (
	// NamespaceNetworking identifies networking-layer keys (loaded from the
	// properties file).
	NamespaceNetworking Namespace = "networking"

	// NamespaceApplication identifies application-layer keys (loaded from
	// Consul KV).
	NamespaceApplication Namespace = "application"
)

// KeyEntry describes a single configuration key in the registry.
type KeyEntry struct {
	// Key is the dot-notation property name (e.g. "storages.download-api").
	Key string

	// Namespace is the layer this key belongs to.
	Namespace Namespace

	// Default is the hard-coded fallback value used when no env, file, or KV
	// value is present. An empty string means "absent by default".
	Default string

	// IfNotPresent is a human-readable note describing what the runtime
	// behaviour is when no value is configured (used for keys whose effective
	// default is computed at runtime, e.g. "Defaults to the number of CPUs").
	IfNotPresent string

	// Description is a short human-readable explanation of the key's purpose.
	Description string
}

// registry holds all registered keys, guarded by a mutex so that advanced
// modules can safely call RegisterKey from their own init() functions.
var (
	registryMu sync.RWMutex
	registry   []KeyEntry
)

func init() {
	registerCEKeys()
}

// RegisterKey adds a new entry to the global key registry.
// It returns an error if a key with the same Key and Namespace combination is
// already registered, or if the new key's env-var name (EnvName) collides with
// an already-registered entry in the same namespace.
func RegisterKey(e KeyEntry) error {
	registryMu.Lock()
	defer registryMu.Unlock()

	prefix := namespacePrefix(e.Namespace)
	newEnv := EnvName(prefix, e.Key)

	for _, existing := range registry {
		if existing.Namespace != e.Namespace {
			continue
		}
		if existing.Key == e.Key {
			return fmt.Errorf("config: key %q (namespace %s) is already registered", e.Key, e.Namespace)
		}
		if EnvName(prefix, existing.Key) == newEnv {
			return fmt.Errorf("config: key %q and existing key %q (namespace %s) both map to env var %s", e.Key, existing.Key, e.Namespace, newEnv)
		}
	}
	registry = append(registry, e)
	return nil
}

// namespacePrefix returns the config key prefix string for the given namespace,
// matching the constants used by the resolver.
func namespacePrefix(ns Namespace) string {
	switch ns {
	case NamespaceNetworking:
		return NetworkingPrefix
	default:
		return ApplicationPrefix
	}
}

// RegisteredKeys returns a sorted snapshot of all registered keys.
// Keys are sorted alphabetically by Key within each namespace, then by
// namespace (networking before application).  A copy is returned so callers
// cannot mutate the registry.
func RegisteredKeys() []KeyEntry {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]KeyEntry, len(registry))
	copy(out, registry)

	sort.Slice(out, func(i, j int) bool {
		ni, nj := out[i].Namespace, out[j].Namespace
		if ni != nj {
			// networking < application
			return ni == NamespaceNetworking
		}
		return out[i].Key < out[j].Key
	})

	return out
}

// registeredKeys returns a snapshot of all registered keys for the given namespace.
func registeredKeys(ns Namespace) []KeyEntry {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]KeyEntry, 0, len(registry))
	for _, e := range registry {
		if e.Namespace == ns {
			out = append(out, e)
		}
	}
	return out
}

// registeredKeyMap returns a map of key→KeyEntry for the given namespace.
func registeredKeyMap(ns Namespace) map[string]KeyEntry {
	keys := registeredKeys(ns)
	m := make(map[string]KeyEntry, len(keys))
	for _, e := range keys {
		m[e.Key] = e
	}
	return m
}

// registerCEKeys adds all CE (Community Edition) configuration keys to the
// registry. Called from init().
func registerCEKeys() {
	networking := []KeyEntry{
		{
			Key:         "carbonio.service.host",
			Namespace:   NamespaceNetworking,
			Default:     "127.78.0.6",
			Description: "Listen address of the carbonio-preview service itself.",
		},
		{
			Key:         "carbonio.service.port",
			Namespace:   NamespaceNetworking,
			Default:     "10000",
			Description: "Listen port of the carbonio-preview service itself.",
		},
		{
			Key:         "carbonio.service-discover.host",
			Namespace:   NamespaceNetworking,
			Default:     "127.0.0.1",
			Description: "Hostname or IP of the Consul service-discovery agent.",
		},
		{
			Key:         "carbonio.service-discover.port",
			Namespace:   NamespaceNetworking,
			Default:     "8500",
			Description: "HTTP port of the Consul service-discovery agent.",
		},
		{
			Key:         "carbonio.storages.host",
			Namespace:   NamespaceNetworking,
			Default:     "127.78.0.6",
			Description: "Hostname or IP of the carbonio-storages service.",
		},
		{
			Key:         "carbonio.storages.port",
			Namespace:   NamespaceNetworking,
			Default:     "20000",
			Description: "Port of the carbonio-storages service.",
		},
		{
			Key:         "carbonio.storages.protocol",
			Namespace:   NamespaceNetworking,
			Default:     "http",
			Description: "Protocol (http/https) used to reach carbonio-storages.",
		},
		{
			Key:         "carbonio.docs-editor.host",
			Namespace:   NamespaceNetworking,
			Default:     "127.78.0.6",
			Description: "Hostname or IP of the carbonio-docs-editor service.",
		},
		{
			Key:         "carbonio.docs-editor.port",
			Namespace:   NamespaceNetworking,
			Default:     "20001",
			Description: "Port of the carbonio-docs-editor service.",
		},
		{
			Key:         "carbonio.docs-editor.protocol",
			Namespace:   NamespaceNetworking,
			Default:     "http",
			Description: "Protocol (http/https) used to reach carbonio-docs-editor.",
		},
	}

	application := []KeyEntry{
		{
			Key:         "timeout-in-seconds",
			Namespace:   NamespaceApplication,
			Default:     "30",
			Description: "Default request timeout in seconds for preview operations.",
		},
		{
			Key:         "docs-timeout-in-seconds",
			Namespace:   NamespaceApplication,
			Default:     "15",
			Description: "Timeout in seconds for document-conversion preview requests.",
		},
		{
			Key:         "workers",
			Namespace:   NamespaceApplication,
			Default:     "2",
			Description: "Number of worker goroutines processing preview requests.",
		},
		{
			Key:          "pdf-workers",
			Namespace:    NamespaceApplication,
			Default:      "",
			IfNotPresent: "Defaults to the number of CPUs",
			Description:  "Number of workers dedicated to PDF rendering; defaults to CPU count when absent.",
		},
		{
			Key:         "vips-concurrency",
			Namespace:   NamespaceApplication,
			Default:     "1",
			Description: "Concurrency level passed to libvips for image processing.",
		},
		{
			Key:         "image-minimum-resolution",
			Namespace:   NamespaceApplication,
			Default:     "80",
			Description: "Minimum accepted resolution (px) for image preview requests.",
		},
		{
			Key:         "enable-document-preview",
			Namespace:   NamespaceApplication,
			Default:     "true",
			Description: "Whether full-page document preview is enabled.",
		},
		{
			Key:         "enable-document-thumbnail",
			Namespace:   NamespaceApplication,
			Default:     "false",
			Description: "Whether document thumbnail generation is enabled.",
		},
		{
			Key:         "storages.download-api",
			Namespace:   NamespaceApplication,
			Default:     "download",
			Description: "Path segment of the carbonio-storages download endpoint.",
		},
		{
			Key:         "storages.health-check",
			Namespace:   NamespaceApplication,
			Default:     "health/live",
			Description: "Path segment of the carbonio-storages health-check endpoint.",
		},
		{
			Key:         "docs-editor.service-endpoint",
			Namespace:   NamespaceApplication,
			Default:     "services/docs/editor",
			Description: "Base path of the docs-editor service endpoint.",
		},
		{
			Key:         "docs-editor.convert-api",
			Namespace:   NamespaceApplication,
			Default:     "cool/convert-to",
			Description: "Path of the docs-editor document-conversion API.",
		},
	}

	for _, e := range networking {
		if err := RegisterKey(e); err != nil {
			panic(err)
		}
	}
	for _, e := range application {
		if err := RegisterKey(e); err != nil {
			panic(err)
		}
	}
}
