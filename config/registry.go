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

	// HiddenFromDocs, when true, causes the key to be excluded from the
	// generated docs/configs.md documentation.
	// Use for operator-facing keys that are intentionally undocumented
	// (e.g. internal tuning knobs not intended for general use).
	HiddenFromDocs bool
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
		{
			Key:         "carbonio.postgresql.host",
			Namespace:   NamespaceNetworking,
			Default:     "127.78.0.6",
			Description: "Hostname or IP of the carbonio-preview-db PostgreSQL service (via Consul mesh upstream, default port 20003).",
		},
		{
			Key:         "carbonio.postgresql.port",
			Namespace:   NamespaceNetworking,
			Default:     "20003",
			Description: "Port of the carbonio-preview-db PostgreSQL service (Consul mesh upstream; 20003 avoids collision with carbonio-storages on 20000).",
		},
	}

	application := []KeyEntry{
		{
			Key:         "document.enable-preview",
			Namespace:   NamespaceApplication,
			Default:     "true",
			Description: "Whether full-page document preview is enabled.",
		},
		{
			Key:         "document.enable-thumbnail",
			Namespace:   NamespaceApplication,
			Default:     "false",
			Description: "Whether document thumbnail generation is enabled.",
		},
		{
			Key:         "storage.fetch-timeout-seconds",
			Namespace:   NamespaceApplication,
			Default:     "30",
			Description: "Timeout (seconds) for fetching the source blob from carbonio-storages.",
		},
		{
			Key:         "document.conversion-timeout-seconds",
			Namespace:   NamespaceApplication,
			Default:     "15",
			Description: "Timeout (seconds) for the docs-editor (Collabora) conversion call.",
		},
		{
			Key:         "render.cache-max-mb",
			Namespace:   NamespaceApplication,
			Default:     "256",
			Description: "Size budget (MiB) of the shared image/PDF/document rendered-output cache; 0 disables it.",
		},
		{
			Key:          "render.max-concurrent-operations",
			Namespace:    NamespaceApplication,
			Default:      "", // computed at runtime → runtime.NumCPU()
			IfNotPresent: "Defaults to the number of CPUs",
			Description:  "Maximum render operations (image, PDF, document) processed concurrently; does not apply to video. Default: CPU count.",
		},
		{
			Key:          "render.pdf-subprocess-pool-size",
			Namespace:    NamespaceApplication,
			Default:      "", // computed at runtime → runtime.NumCPU()
			IfNotPresent: "Defaults to the number of CPUs",
			Description:  "Number of PDFium helper OS subprocesses used for PDF/document rendering; a render acquires one after passing render/max-concurrent-operations. Default: CPU count.",
		},
		{
			Key:          "video.max-concurrent-extractions",
			Namespace:    NamespaceApplication,
			Default:      "", // computed at runtime → runtime.NumCPU()
			IfNotPresent: "Defaults to the number of CPUs",
			Description:  "Maximum video first-frame extraction jobs run concurrently (each spawns an ffmpeg subprocess). Default: CPU count.",
		},
		// ── Database credentials ─────────────────────────────────────────────
		// Read from Consul KV at runtime inside config.Load() via
		// r.Application.Get("database.credentials.db-*"). Registered here so the
		// KV paths are documented. The secret value is NEVER rendered: Default:""
		// makes the generator emit *(not set)* plus the "If not set" note via the
		// IfNotPresent mechanism, without ever printing the value. Always set by
		// carbonio-preview-db-bootstrap. Mirrors carbonio-tasks-ce.
		{
			Key:          "database.credentials.db-name",
			Namespace:    NamespaceApplication,
			Default:      "",
			IfNotPresent: "Crashes; but always set by database bootstrap",
			Description:  "PostgreSQL database name. Read from Consul KV; set by carbonio-preview-db-bootstrap.",
		},
		{
			Key:          "database.credentials.db-password",
			Namespace:    NamespaceApplication,
			Default:      "",
			IfNotPresent: "Crashes; but always set by database bootstrap",
			Description:  "PostgreSQL password. Read from Consul KV; set by carbonio-preview-db-bootstrap.",
		},
		{
			Key:          "database.credentials.db-username",
			Namespace:    NamespaceApplication,
			Default:      "",
			IfNotPresent: "Crashes; but always set by database bootstrap",
			Description:  "PostgreSQL username. Read from Consul KV; set by carbonio-preview-db-bootstrap.",
		},
		// ── Database connection-pool tuning ──────────────────────────────────
		{
			Key:         "database.db-pool-max-size",
			Namespace:   NamespaceApplication,
			Default:     "10",
			Description: "Maximum size of the PostgreSQL (pgx) connection pool.",
		},
		{
			Key:         "database.db-pool-min-size",
			Namespace:   NamespaceApplication,
			Default:     "2",
			Description: "Minimum size of the PostgreSQL (pgx) connection pool.",
		},
		{
			Key:         "database.db-pool-max-lifetime",
			Namespace:   NamespaceApplication,
			Default:     "600000",
			Description: "Maximum lifetime (milliseconds) before a pooled PostgreSQL connection is recycled.",
		},
		// ── Video worker tuning ───────────────────────────────────────────────
		// NOTE: a separate worker-only concurrency bound is intentionally absent.
		// Total ffmpeg concurrency is governed by video.max-concurrent-extractions
		// (VideoSem), used by the background worker. A separate worker-only
		// concurrency bound was parsed but never used and has been removed.
		{
			Key:         "video.poll-interval-seconds",
			Namespace:   NamespaceApplication,
			Default:     "15",
			Description: "Interval (seconds) at which the background video worker sweeps for pending jobs.",
		},
		{
			Key:         "video.stuck-generation-timeout-seconds",
			Namespace:   NamespaceApplication,
			Default:     "900",
			Description: "A generation left in GENERATING state longer than this (seconds) is treated as crashed and reclaimed.",
		},
		{
			Key:         "video.max-attempts",
			Namespace:   NamespaceApplication,
			Default:     "3",
			Description: "Maximum generation attempts before a video job is marked FAILED.",
		},
		// NOTE: log.level is intentionally NOT registered here.
		// It is controlled by the PREVIEW_LOG_LEVEL environment variable directly
		// (a per-instance, framework-level knob equivalent to QUARKUS_LOG_LEVEL),
		// outside the extensions networking/application config chain.
		//
		// NOTE: vips_concurrency is NOT registered here. It is a libvips
		// internal-threads setting hardcoded to 1 as a plain code constant
		// (config.vipsConcurrency) — not an operator knob, no env var, no KV path.
		// Endpoint-path knobs are hardcoded constants.
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
