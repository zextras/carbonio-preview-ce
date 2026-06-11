// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

func init() {
	if err := Register(V1MigrateFromPythonIni()); err != nil {
		panic("migrate: register V1: " + err.Error())
	}
}

// V1MigrateFromPythonIni returns the CE V1 migration that reads entries from
// the legacy Python config.ini and writes them into config.properties
// (networking) and Consul KV (application).
//
// Drop-only entries (keys whose value is discarded with no replacement) are
// handled by a no-op function that logs "dropped" and returns nil so the runner
// still deletes the old key.
//
// Keys in the ADVANCED V2 territory (nginx lookup / memcached) are deliberately
// absent from both maps; they survive in the ini until the advanced binary runs
// its V2.
//
// Exported so that the ADVANCED module can call it directly (or re-use its
// entry maps to build V1+V2).
func V1MigrateFromPythonIni() Migration {
	rename := func(newKey string) EntryFunc {
		return func(_, oldValue string, dest ConfigStore) error {
			return dest.Set(newKey, oldValue)
		}
	}

	return Migration{
		Version: 1,
		Name:    "V1__MigrateFromPythonIni",

		// ── Networking entries (INI section.key → config.properties key) ──────────
		NetworkingEntries: map[string]EntryFunc{
			"carbonio.preview.default_host":         rename("carbonio.service.host"),
			"carbonio.preview.default_port":         rename("carbonio.service.port"),
			"carbonio.storages.default_host":        rename("carbonio.storages.host"),
			"carbonio.storages.default_port":        rename("carbonio.storages.port"),
			"carbonio.storages.default_protocol":    rename("carbonio.storages.protocol"),
			"carbonio.docs-editor.default_host":     rename("carbonio.docs-editor.host"),
			"carbonio.docs-editor.default_port":     rename("carbonio.docs-editor.port"),
			"carbonio.docs-editor.default_protocol": rename("carbonio.docs-editor.protocol"),
		},

		// ── Application entries (INI section.key → Consul KV raw path) ───────────
		ApplicationEntries: map[string]EntryFunc{
			"carbonio.preview.timeout_in_seconds":        rename("carbonio-preview/timeout-in-seconds"),
			"carbonio.preview.docs-timeout":              rename("carbonio-preview/docs-timeout-in-seconds"),
			"carbonio.preview.workers":                   rename("carbonio-preview/workers"),
			"image_constants.minimum_resolution":         rename("carbonio-preview/image-minimum-resolution"),
			"carbonio.preview.enable_document_preview":   rename("carbonio-preview/enable-document-preview"),
			"carbonio.preview.enable_document_thumbnail": rename("carbonio-preview/enable-document-thumbnail"),
			"carbonio.storages.download_api":             rename("carbonio-preview/storages/download-api"),
			"carbonio.storages.health_check":             rename("carbonio-preview/storages/health-check"),
			"carbonio.docs-editor.service_endpoint":      rename("carbonio-preview/docs-editor/service-endpoint"),
			"carbonio.docs-editor.convert_api":           rename("carbonio-preview/docs-editor/convert-api"),
		},

		// ── Drop-only entries (value discarded, no Consul write) ─────────────────
		DropEntries: []string{
			"carbonio.preview.name",
			"carbonio.preview.image_name",
			"carbonio.preview.pdf_name",
			"carbonio.preview.document_name",
			"carbonio.preview.health_name",
			"log.format",
			"log.level",
			"log.path",
			"carbonio.storages.name",
		},
	}
}
