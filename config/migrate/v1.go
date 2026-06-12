// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	if err := Register(V1MigrateFromPythonIni(DefaultDropInPath)); err != nil {
		panic("migrate: register V1: " + err.Error())
	}
}

// V1MigrateFromPythonIni returns the CE V1 migration that reads entries from
// the legacy Python config.ini and writes them into config.properties
// (networking) and Consul KV (application).
//
// dropInPath is the destination for the log-level systemd drop-in file
// (typically DefaultDropInPath in production; a temp-dir path in tests).
//
// Drop-only entries (keys whose value is discarded with no replacement) are
// handled by a no-op function that logs "dropped" and returns nil so the runner
// still deletes the old key.
//
// log.level is special: it is NOT a DropEntry. Its value is written atomically
// to a systemd drop-in file as Environment="PREVIEW_LOG_LEVEL=<value>". The
// entry is placed in NetworkingEntries with a custom func that ignores the
// properties store and writes the drop-in directly (idempotent: re-running when
// the key is absent is a no-op because iniStore.get returns false).
//
// log.format and log.path remain DropEntries (values discarded, no replacement).
//
// Keys in the ADVANCED V2 territory (nginx lookup / memcached) are deliberately
// absent from both maps; they survive in the ini until the advanced binary runs
// its V2.
//
// Exported so that the ADVANCED module can call it directly (or re-use its
// entry maps to build V1+V2).
func V1MigrateFromPythonIni(dropInPath string) Migration {
	rename := func(newKey string) EntryFunc {
		return func(_, oldValue string, dest ConfigStore) error {
			return dest.Set(newKey, oldValue)
		}
	}

	// writeLogLevelDropIn is the entry function for log.level.
	// It ignores the dest ConfigStore entirely and writes a systemd drop-in file
	// atomically (tmp+rename, 0644) so the log level survives package upgrades.
	// The drop-in is picked up by systemctl daemon-reload (triggered by the
	// pending-setups script after --setup returns).
	writeLogLevelDropIn := func(_, oldValue string, _ ConfigStore) error {
		content := fmt.Sprintf("[Service]\nEnvironment=\"PREVIEW_LOG_LEVEL=%s\"\n", oldValue)

		// Ensure parent directory exists.
		if err := os.MkdirAll(filepath.Dir(dropInPath), 0o755); err != nil {
			return fmt.Errorf("migrate: create drop-in dir for %s: %w", dropInPath, err)
		}

		// Write atomically via temp file + rename.
		dir := filepath.Dir(dropInPath)
		tmp, err := os.CreateTemp(dir, "log-level-*.conf.tmp")
		if err != nil {
			return fmt.Errorf("migrate: create temp for drop-in %s: %w", dropInPath, err)
		}
		tmpName := tmp.Name()

		if _, err := fmt.Fprint(tmp, content); err != nil {
			tmp.Close()        //nolint:errcheck
			os.Remove(tmpName) //nolint:errcheck
			return fmt.Errorf("migrate: write temp drop-in %s: %w", tmpName, err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName) //nolint:errcheck
			return fmt.Errorf("migrate: close temp drop-in %s: %w", tmpName, err)
		}
		if err := os.Chmod(tmpName, 0o644); err != nil {
			os.Remove(tmpName) //nolint:errcheck
			return fmt.Errorf("migrate: chmod temp drop-in %s: %w", tmpName, err)
		}
		if err := os.Rename(tmpName, dropInPath); err != nil {
			os.Remove(tmpName) //nolint:errcheck
			return fmt.Errorf("migrate: rename temp drop-in to %s: %w", dropInPath, err)
		}
		return nil
	}

	return Migration{
		Version: 1,
		Name:    "V1__MigrateFromPythonIni",

		// ── Networking entries (INI section.key → config.properties key) ──────────
		// log.level is placed here because: its entry func ignores the properties
		// store and writes a systemd drop-in instead; the runner counts it in the
		// networking tally, keeping the application tally unaffected.
		NetworkingEntries: map[string]EntryFunc{
			"carbonio.preview.default_host":         rename("carbonio.service.host"),
			"carbonio.preview.default_port":         rename("carbonio.service.port"),
			"carbonio.storages.default_host":        rename("carbonio.storages.host"),
			"carbonio.storages.default_port":        rename("carbonio.storages.port"),
			"carbonio.storages.default_protocol":    rename("carbonio.storages.protocol"),
			"carbonio.docs-editor.default_host":     rename("carbonio.docs-editor.host"),
			"carbonio.docs-editor.default_port":     rename("carbonio.docs-editor.port"),
			"carbonio.docs-editor.default_protocol": rename("carbonio.docs-editor.protocol"),
			// log.level → systemd drop-in (does NOT write to config.properties)
			"log.level": writeLogLevelDropIn,
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

		// ── Drop-only entries (value discarded, no replacement) ──────────────────
		// log.format and log.path are dropped (no Go equivalent needed).
		// log.level is NOT here — it moved to NetworkingEntries (drop-in writer).
		DropEntries: []string{
			"carbonio.preview.name",
			"carbonio.preview.image_name",
			"carbonio.preview.pdf_name",
			"carbonio.preview.document_name",
			"carbonio.preview.health_name",
			"log.format",
			"log.path",
			"carbonio.storages.name",
		},
	}
}
