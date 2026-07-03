// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package cemig holds the CE (Community Edition) migration set.
//
// It registers itself into the "ce" migration set on import (see init below),
// which is the whole point of moving it out of the framework package
// (config/migrate): a plain import of config/migrate no longer pulls in the
// CE migrations, so the Advanced binary — which imports config/migrate for
// the framework but does NOT import this package — never inherits CE's V1.
package cemig

import "github.com/zextras/carbonio-preview-ce/v2/config/migrate"

func init() {
	if err := migrate.RegisterInSet("ce", V1MigrateFromPythonIni()); err != nil {
		panic("cemig: register V1 into set \"ce\": " + err.Error())
	}
}

// V1MigrateFromPythonIni returns the CE V1 migration that reads entries from
// the legacy Python config.ini and writes them into config.properties
// (networking), Consul KV (application), and a systemd drop-in file
// (DropInEnvEntries).
//
// Drop-only entries (keys whose value is discarded with no replacement) are
// listed in DropEntries; the runner deletes the old key and counts it in the
// application tally.
//
// log.level is handled via DropInEnvEntries: the runner writes a systemd
// drop-in file atomically to the effective drop-in path (Paths.DropInPath if
// non-empty, otherwise DefaultDropInPath).  The entry is counted in the
// networking tally (networking n/9).
//
// log.format and log.path remain DropEntries (values discarded, no replacement).
//
// Keys in the ADVANCED V2 territory (nginx lookup / memcached) are deliberately
// absent from both maps; they survive in the ini until the advanced binary runs
// its own V1/V2 (registered into the "advanced" set, entirely separate from
// this "ce" set).
func V1MigrateFromPythonIni() migrate.Migration {
	rename := func(newKey string) migrate.EntryFunc {
		return func(_, oldValue string, dest migrate.ConfigStore) error {
			return dest.Set(newKey, oldValue)
		}
	}

	return migrate.Migration{
		Version: 1,
		Name:    "V1__MigrateFromPythonIni",

		// ── Networking entries (INI section.key → config.properties key) ──────────
		NetworkingEntries: map[string]migrate.EntryFunc{
			"carbonio.preview.default_host":         rename("carbonio.service.host"),
			"carbonio.preview.default_port":         rename("carbonio.service.port"),
			"carbonio.storages.default_host":        rename("carbonio.storages.host"),
			"carbonio.storages.default_port":        rename("carbonio.storages.port"),
			"carbonio.storages.default_protocol":    rename("carbonio.storages.protocol"),
			"carbonio.docs-editor.default_host":     rename("carbonio.docs-editor.host"),
			"carbonio.docs-editor.default_port":     rename("carbonio.docs-editor.port"),
			"carbonio.docs-editor.default_protocol": rename("carbonio.docs-editor.protocol"),
		},

		// ── Drop-in env entries (INI key → systemd Environment variable name) ────
		// The runner writes a single drop-in file to the effective Paths.DropInPath
		// (or DefaultDropInPath) atomically (tmp+rename, 0644, MkdirAll parent).
		// These entries are counted in the networking tally (networking n/9).
		DropInEnvEntries: map[string]string{
			"log.level": "PREVIEW_LOG_LEVEL",
		},

		// ── Application entries (INI section.key → Consul KV raw path) ───────────
		ApplicationEntries: map[string]migrate.EntryFunc{
			"carbonio.preview.enable_document_preview":   rename("carbonio-preview/document/enable-preview"),
			"carbonio.preview.enable_document_thumbnail": rename("carbonio-preview/document/enable-thumbnail"),
			// Operator timeout overrides are carried into Consul KV so that an
			// apt upgrade from the old Python preview preserves customised values.
			// If the operator's value happens to equal the Go default it is still
			// written (harmless: the registry default wins on any equal value).
			"carbonio.preview.timeout_in_seconds": rename("carbonio-preview/storage/fetch-timeout-seconds"),
			"carbonio.preview.docs-timeout":       rename("carbonio-preview/document/conversion-timeout-seconds"),
		},

		// ── Drop-only entries (value discarded, no replacement) ──────────────────
		// log.format and log.path are dropped (no Go equivalent needed).
		// log.level is in DropInEnvEntries, not here.
		// Timeout keys (timeout_in_seconds, docs-timeout) are now migrated to
		// Consul KV application keys — see ApplicationEntries above.
		// Worker/concurrency (workers) and endpoint-path keys have no Go
		// equivalent and are intentionally discarded.
		DropEntries: []string{
			"carbonio.preview.name",
			"carbonio.preview.image_name",
			"carbonio.preview.pdf_name",
			"carbonio.preview.document_name",
			"carbonio.preview.health_name",
			"log.format",
			"log.path",
			"carbonio.storages.name",
			// concurrency/endpoint-path keys obsoleted by hardcoded constants / env vars
			"carbonio.preview.workers",
			"image_constants.minimum_resolution",
			"carbonio.storages.download_api",
			"carbonio.storages.health_check",
			"carbonio.docs-editor.service_endpoint",
			"carbonio.docs-editor.convert_api",
		},
	}
}
