// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package migrate provides a one-shot config migration framework that mirrors
// the carbonio-quarkus-extensions package-scoped ConfigMigration /
// ConfigMigrationRunner semantics.
//
// Design overview:
//
//   - A Migration describes a versioned migration: a unique integer version
//     (unique within its own set), a name matching V<n>__<desc>, and two entry
//     tables (networking and application).  Each entry is an old-source key
//     mapped to a function that writes new entries via the injected
//     ConfigStore.
//
//   - This package is FRAMEWORK ONLY: it holds no migrations of its own.
//     Migrations are registered into NAMED SETS via RegisterInSet, so that
//     each edition (e.g. CE, Advanced) owns its own independent set and never
//     inherits another edition's migrations merely by importing this package.
//     Different sets MAY reuse the same version number — a CE V1 and an
//     Advanced V1 are unrelated and do not collide.
//
//   - orderedSet returns a given set's migrations in ascending version order;
//     an unknown/absent set name returns nil.
//
//   - NewRunner builds a Runner wired to the stores (legacy INI source,
//     networking properties file, Consul KV) and to the selected set name.
//
//   - Runner.Run executes the selected set's migrations in ascending version
//     order.
package migrate

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
)

// migrationNameRE enforces the V<version>__<description> naming rule.
var migrationNameRE = regexp.MustCompile(`^V(\d+)__\w+$`)

// EntryFunc is the function signature for a single migration entry.
// It receives the old source key and its current value, and must write new
// entries via the injected ConfigStore.
type EntryFunc func(oldKey, oldValue string, dest ConfigStore) error

// Migration describes one versioned config migration.
// The Name field must match ^V(\d+)__\w+$ (validated at registration).
type Migration struct {
	// Version determines execution order.  Must be unique across all registered migrations.
	Version int

	// Name must match ^V(\d+)__\w+$ (e.g. "V1__MigrateFromPythonIni").
	Name string

	// NetworkingEntries maps old source (INI) key → function that writes to the
	// networking config.properties store.
	NetworkingEntries map[string]EntryFunc

	// ApplicationEntries maps old source (INI) key → function that writes to the
	// Consul KV store.
	ApplicationEntries map[string]EntryFunc

	// DropEntries lists ini "section.key" strings whose value is discarded with no
	// replacement.  The runner deletes the key from the ini (per-entry idempotency)
	// and counts each removed entry inside the application j/k summary tally.
	// Keys listed here do NOT require SETUP_CONSUL_TOKEN.
	DropEntries []string

	// DropInEnvEntries maps old ini key → systemd Environment variable name.
	// For each entry present in the ini, the runner writes a systemd drop-in file
	// atomically (tmp+rename, 0644) to the effective drop-in path
	// (Paths.DropInPath if non-empty, otherwise DefaultDropInPath).
	// The entry is removed from the ini on success and counted inside the
	// networking tally (networking n/m).
	//
	// Limitation: all entries share a single drop-in file.  If multiple entries
	// are present they are written together in one pass; partial failures leave
	// the drop-in absent and the successful keys still pending in the ini for
	// retry.
	DropInEnvEntries map[string]string
}

var (
	setsMu sync.Mutex
	sets   = map[string][]Migration{}
)

// RegisterInSet adds m to the named migration set setName.
// It returns an error if:
//   - m.Name does not match ^V(\d+)__\w+$
//   - m.Version is already registered WITHIN THE SAME SET (a different set
//     may reuse the same version number — e.g. CE's V1 and Advanced's V1 are
//     unrelated and do not collide).
func RegisterInSet(setName string, m Migration) error {
	if !migrationNameRE.MatchString(m.Name) {
		return fmt.Errorf("migrate: name %q does not match V<n>__<desc> pattern", m.Name)
	}
	setsMu.Lock()
	defer setsMu.Unlock()
	for _, existing := range sets[setName] {
		if existing.Version == m.Version {
			return fmt.Errorf("migrate: version %d is already registered in set %q (existing: %q)", m.Version, setName, existing.Name)
		}
	}
	sets[setName] = append(sets[setName], m)
	return nil
}

// orderedSet returns a version-ascending copy of the named set's migrations.
// An unknown or absent set name returns nil.
func orderedSet(setName string) []Migration {
	setsMu.Lock()
	defer setsMu.Unlock()
	src := sets[setName]
	if len(src) == 0 {
		return nil
	}
	out := make([]Migration, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})
	return out
}

// resetSets clears every registered set — used only in tests.
func resetSets() {
	setsMu.Lock()
	defer setsMu.Unlock()
	sets = map[string][]Migration{}
}
