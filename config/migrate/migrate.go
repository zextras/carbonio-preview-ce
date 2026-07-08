// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package migrate provides a config migration framework that mirrors the
// carbonio-quarkus-extensions package-scoped ConfigMigration /
// ConfigMigrationRunner semantics, split into two distinct phases:
//
//   - A one-time Bootstrap: the legacy Python config.ini → Go config import.
//     At most one Bootstrap may be registered per set (RegisterBootstrap).
//     It holds the ini-sourced entry tables (networking, application,
//     drop-in-env, drop-only) and has no version — there is only ever one
//     bootstrap per edition, run once per --setup before any KV→KV migration.
//
//   - A versioned KV→KV series: each Migration renames existing Consul KV
//     keys (ApplicationKVMoves), with an optional value transform,
//     independently of the legacy ini.  This is the recurring engine for
//     every future KV rename.
//
// Design overview:
//
//   - A Migration describes one entry in the KV→KV series: a unique integer
//     version (unique within its own set), a name matching V<n>__<desc>, and
//     a non-empty list of ApplicationKVMoves.
//
//   - This package is FRAMEWORK ONLY: it holds no bootstrap or migrations of
//     its own.  Both are registered into NAMED SETS via RegisterBootstrap /
//     RegisterInSet, so that each edition (e.g. CE, Advanced) owns its own
//     independent set and never inherits another edition's bootstrap or
//     migrations merely by importing this package.  Different sets MAY reuse
//     the same version number — a CE V1 and an Advanced V1 are unrelated and
//     do not collide.
//
//   - orderedSet returns a given set's migrations in ascending version order;
//     bootstrapFor returns a given set's registered Bootstrap, if any.  An
//     unknown/absent set name returns nil / not-found respectively.
//
//   - NewRunner builds a Runner wired to the stores (legacy INI source,
//     networking properties file, Consul KV) and to the selected set name.
//
//   - Runner.Run executes the selected set's Bootstrap (if registered) first,
//     then the Migration series in ascending version order.
package migrate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// migrationNameRE enforces the V<version>__<description> naming rule.
var migrationNameRE = regexp.MustCompile(`^V(\d+)__\w+$`)

// EntryFunc is the function signature for a single migration entry.
// It receives the old source key and its current value, and must write new
// entries via the injected ConfigStore.
type EntryFunc func(oldKey, oldValue string, dest ConfigStore) error

// KVMove describes one Consul KV → Consul KV key rename with an optional
// value transform.  Paths are raw slash KV paths (same convention as
// application entry destinations), e.g. "carbonio-preview/database/db-pool-max-size".
//
// Runner semantics (see Runner.runApplicationKVMoves):
//   - OldPath absent in KV → skip (nothing to migrate).
//   - NewPath already present → skip entirely (never clobber an operator's
//     new-style value; the old key is left untouched as a harmless leftover).
//   - Transform error → WARNING, skip, continue (never fails the setup).
//   - Otherwise Set(NewPath, transformed) then Delete(OldPath).
type KVMove struct {
	// OldPath is the full KV path of the key to move.
	OldPath string

	// NewPath is the full KV path the value is moved to.
	NewPath string

	// Transform converts the old value into the new one.  nil means identity
	// (the value is copied verbatim).
	Transform func(oldValue string) (string, error)
}

// Bootstrap describes the one-time legacy Python config.ini → Go config
// import for a set.  Unlike Migration it has no Version: there is exactly one
// Bootstrap per set, run once (per --setup invocation) before the set's
// Migration series.
type Bootstrap struct {
	// Name identifies the bootstrap in the setup log line (e.g.
	// "Bootstrap__MigrateFromPythonIni"). Must be non-empty.
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

// Migration describes one entry in a set's versioned KV→KV series.
// The Name field must match ^V(\d+)__\w+$ (validated at registration).
type Migration struct {
	// Version determines execution order.  Must be unique across all registered migrations.
	Version int

	// Name must match ^V(\d+)__\w+$ (e.g. "V1__MoveDBPoolKeys").
	Name string

	// ApplicationKVMoves lists Consul KV → Consul KV key renames (with optional
	// value transform) executed in declaration order.  Moves read from Consul
	// KV itself, NOT from the legacy ini, and run even when the ini is absent.
	// A Migration must declare at least one move (validated at registration);
	// its mere registration always counts as application work (SETUP_CONSUL_TOKEN
	// required). Moves are intrinsically idempotent: once the old key is gone,
	// re-runs skip.
	ApplicationKVMoves []KVMove
}

var (
	setsMu     sync.Mutex
	sets       = map[string][]Migration{}
	bootstraps = map[string]Bootstrap{}
)

// RegisterBootstrap registers b as the named set's one-time ini→KV bootstrap.
// It returns an error if:
//   - b.Name is empty.
//   - a bootstrap is already registered for setName.
func RegisterBootstrap(setName string, b Bootstrap) error {
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("migrate: bootstrap for set %q: Name must be non-empty", setName)
	}
	setsMu.Lock()
	defer setsMu.Unlock()
	if existing, ok := bootstraps[setName]; ok {
		return fmt.Errorf("migrate: a bootstrap is already registered in set %q (existing: %q)", setName, existing.Name)
	}
	bootstraps[setName] = b
	return nil
}

// bootstrapFor returns the named set's registered Bootstrap, if any.
func bootstrapFor(setName string) (Bootstrap, bool) {
	setsMu.Lock()
	defer setsMu.Unlock()
	b, ok := bootstraps[setName]
	return b, ok
}

// RegisterInSet adds m to the named migration set setName.
// It returns an error if:
//   - m.Name does not match ^V(\d+)__\w+$
//   - m.Version is already registered WITHIN THE SAME SET (a different set
//     may reuse the same version number — e.g. CE's V1 and Advanced's V1 are
//     unrelated and do not collide).
//   - m.ApplicationKVMoves is empty (a Vn migration with no moves does
//     nothing and is therefore a programming error, not a valid registration).
//   - any ApplicationKVMoves entry has an empty OldPath or NewPath, a path
//     without a "/" (KV paths are full slash paths, e.g. "<service>/<key>" —
//     a bare dotted config key here would silently never match), or
//     OldPath == NewPath.
func RegisterInSet(setName string, m Migration) error {
	if !migrationNameRE.MatchString(m.Name) {
		return fmt.Errorf("migrate: name %q does not match V<n>__<desc> pattern", m.Name)
	}
	if len(m.ApplicationKVMoves) == 0 {
		return fmt.Errorf("migrate: %s: ApplicationKVMoves must be non-empty (a Vn migration with no moves does nothing)", m.Name)
	}
	for _, mv := range m.ApplicationKVMoves {
		if mv.OldPath == "" || mv.NewPath == "" {
			return fmt.Errorf("migrate: %s: KV move paths must be non-empty (old=%q, new=%q)", m.Name, mv.OldPath, mv.NewPath)
		}
		if !strings.Contains(mv.OldPath, "/") || !strings.Contains(mv.NewPath, "/") {
			return fmt.Errorf("migrate: %s: KV move paths must be full slash KV paths (old=%q, new=%q)", m.Name, mv.OldPath, mv.NewPath)
		}
		if mv.OldPath == mv.NewPath {
			return fmt.Errorf("migrate: %s: KV move old and new path are identical (%q)", m.Name, mv.OldPath)
		}
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
