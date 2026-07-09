// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cemig

import "github.com/zextras/carbonio-preview-ce/v3/config/migrate"

func init() {
	if err := migrate.RegisterInSet("ce", V2RenameConfigNamespaces()); err != nil {
		panic("cemig: register V2 into set \"ce\": " + err.Error())
	}
}

// V2RenameConfigNamespaces returns the CE V2 migration — the second entry in
// the "ce" set's KV→KV series — that moves four Consul KV keys to a clearer
// namespace introduced on the refactor/kv-config-namespace branch (see
// config/registry.go's application key block). Like V1MoveDBPoolKeys, this
// migration talks to Consul KV directly (ApplicationKVMoves) and does NOT
// touch the legacy Python config.ini, so it also applies to installs that
// already completed the bootstrap (or never had a config.ini at all).
//
// Key moves (KV path = "carbonio-preview/" + dots→slashes, matching the
// bootstrap's ApplicationEntries convention above) — all four are verbatim
// value carry-overs, no Transform:
//
//	storage.fetch-timeout-seconds      -> image-document.fetch-timeout-seconds
//	render.cache-max-mb                -> cache-max-mb                       (moves to root — no namespace prefix)
//	render.max-concurrent-operations   -> image-document.max-concurrent-operations
//	render.pdf-subprocess-pool-size    -> document.subprocess-pool-size
//
// The "render" namespace is retired by this move: image/PDF/document timeout
// and concurrency knobs move under "image-document", the PDFium subprocess
// pool moves under "document", and the rendered-output cache budget — which
// is shared by ALL preview types including video, not just image/PDF/document
// — moves to the root (no namespace prefix at all).
func V2RenameConfigNamespaces() migrate.Migration {
	return migrate.Migration{
		Version: 2,
		Name:    "V2__RenameConfigNamespaces",

		// ── Application KV-to-KV moves (Consul KV, independent of the ini) ───────
		ApplicationKVMoves: []migrate.KVMove{
			{
				OldPath: "carbonio-preview/storage/fetch-timeout-seconds",
				NewPath: "carbonio-preview/image-document/fetch-timeout-seconds",
			},
			{
				OldPath: "carbonio-preview/render/cache-max-mb",
				NewPath: "carbonio-preview/cache-max-mb",
			},
			{
				OldPath: "carbonio-preview/render/max-concurrent-operations",
				NewPath: "carbonio-preview/image-document/max-concurrent-operations",
			},
			{
				OldPath: "carbonio-preview/render/pdf-subprocess-pool-size",
				NewPath: "carbonio-preview/document/subprocess-pool-size",
			},
		},
	}
}
