// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cemig

import (
	"fmt"
	"strconv"

	"github.com/zextras/carbonio-preview-ce/v3/config/migrate"
)

func init() {
	if err := migrate.RegisterInSet("ce", V2MoveDBPoolKeys()); err != nil {
		panic("cemig: register V2 into set \"ce\": " + err.Error())
	}
}

// V2MoveDBPoolKeys returns the CE V2 migration that moves the three
// pre-8d14b8c database-pool Consul KV keys to their current flat db-pool-*
// paths (see config/registry.go's "Database connection-pool tuning" block).
// Unlike V1, this migration talks to Consul KV directly (ApplicationKVMoves)
// and does NOT touch the legacy Python config.ini, so it also applies to
// installs that already completed V1 (or never had a config.ini at all).
//
// Key moves (KV path = "carbonio-preview/" + dots→slashes, matching V1's
// ApplicationEntries convention above):
//
//	database.pool.max-connections                 -> database.db-pool-max-size      (verbatim)
//	database.pool.min-connections                 -> database.db-pool-min-size      (verbatim)
//	database.pool.connection-max-lifetime-seconds -> database.db-pool-max-lifetime  (seconds -> ms, x1000)
//
// The lifetime move's Transform parses the old value with strconv.Atoi; a
// non-numeric operator value returns an error, which the runner turns into a
// WARNING-and-skip (the old key is left in place for a retry — see
// Runner.runApplicationKVMoves), never panicking and never failing setup.
func V2MoveDBPoolKeys() migrate.Migration {
	secondsToMillis := func(oldValue string) (string, error) {
		n, err := strconv.Atoi(oldValue)
		if err != nil {
			return "", fmt.Errorf("parse seconds value %q: %w", oldValue, err)
		}
		return strconv.Itoa(n * 1000), nil
	}

	return migrate.Migration{
		Version: 2,
		Name:    "V2__MoveDBPoolKeys",

		// ── Application KV-to-KV moves (Consul KV, independent of the ini) ───────
		ApplicationKVMoves: []migrate.KVMove{
			{
				OldPath: "carbonio-preview/database/pool/max-connections",
				NewPath: "carbonio-preview/database/db-pool-max-size",
			},
			{
				OldPath: "carbonio-preview/database/pool/min-connections",
				NewPath: "carbonio-preview/database/db-pool-min-size",
			},
			{
				OldPath:   "carbonio-preview/database/pool/connection-max-lifetime-seconds",
				NewPath:   "carbonio-preview/database/db-pool-max-lifetime",
				Transform: secondsToMillis,
			},
		},
	}
}
