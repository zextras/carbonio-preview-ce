// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"os"
	"path/filepath"
)

// Runner executes all registered migrations in version-ascending order.
// It is wired to three stores:
//   - ini: legacy source (config.ini) — read-only source, deleted/renamed after run
//   - net: networking destination (config.properties) — written if any net entry migrated
//   - kv:  application destination (Consul KV) — written per-entry
type Runner struct {
	ini        *iniStore
	net        *propertiesStore
	kv         *consulKvStore
	dropInPath string // destination for the log-level systemd drop-in
}

// HasApplicationWork returns true if at least one registered migration has an
// application entry whose old key is present in the legacy ini.  This is used
// by --setup to decide whether SETUP_CONSUL_TOKEN is required before any
// modification is made.
// It must be consulted BEFORE Run() — the token gate is enforced only by call ordering in RunSetup.
func (r *Runner) HasApplicationWork() bool {
	if r.ini.isAbsent() {
		return false
	}
	for _, m := range registered() {
		for oldKey := range m.ApplicationEntries {
			if _, ok := r.ini.get(oldKey); ok {
				return true
			}
		}
	}
	return false
}

// Paths holds the injectable file paths and consul URL, for testing.
type Paths struct {
	IniPath     string
	PropsPath   string
	ConsulURL   string
	ConsulToken string
	// DropInPath is the destination for the log-level systemd drop-in file.
	// Defaults to DefaultDropInPath when empty.
	DropInPath string
}

// DefaultDropInPath is the production path for the log-level systemd drop-in.
const DefaultDropInPath = "/etc/systemd/system/carbonio-preview.service.d/log-level.conf"

// effectiveDropInPath returns p.DropInPath if set, otherwise DefaultDropInPath.
func (p Paths) effectiveDropInPath() string {
	if p.DropInPath != "" {
		return p.DropInPath
	}
	return DefaultDropInPath
}

// NewRunner builds a Runner wired to the given paths and consul URL.
func NewRunner(p Paths) (*Runner, error) {
	ini, err := newIniStore(p.IniPath)
	if err != nil {
		return nil, err
	}
	net, err := newPropertiesStore(p.PropsPath)
	if err != nil {
		return nil, err
	}
	kv := newConsulKvStore(p.ConsulURL, p.ConsulToken)
	return &Runner{ini: ini, net: net, kv: kv, dropInPath: p.effectiveDropInPath()}, nil
}

// Run executes all registered migrations in version-ascending order.
//
// Output mirrors ConfigMigrationRunner:
//
//	Config migration: N migration(s) found.
//	  V1__Name: networking n/m, application j/k migrated
//
// Per-entry errors are WARNING'd to stderr; the old key is NOT deleted on
// error so a re-run can retry.  Per-migration class failures are warned and
// skipped.  The networking properties file is saved only if at least one net
// entry migrated.  The ini store is always saved (or renamed) at the end if
// the ini was not absent.
func (r *Runner) Run() {
	migrations := registered()
	if len(migrations) == 0 {
		fmt.Println("Config migration: no migrations found.")
		return
	}
	fmt.Printf("Config migration: %d migration(s) found.\n", len(migrations))

	for _, m := range migrations {
		netMigrated, appMigrated := r.runOne(m)
		totalNet := len(m.NetworkingEntries) + len(m.DropInEnvEntries)
		totalApp := len(m.ApplicationEntries) + len(m.DropEntries)
		fmt.Printf("  %s: networking %d/%d, application %d/%d migrated\n",
			m.Name, netMigrated, totalNet, appMigrated, totalApp)
	}

	// Save config.properties only if at least one networking entry actually
	// called Set() on the properties store (not just successfully ran —
	// log.level writes a drop-in instead and must not trigger a props save).
	if r.net.dirty {
		if err := r.net.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: cannot save config.properties: %v\n", err)
		}
	}

	if !r.ini.isAbsent() {
		if err := r.ini.save(); err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: cannot save config.ini: %v\n", err)
		}
	}
}

// runOne runs a single migration's networking, application, drop, and drop-in
// entries.  It returns (netMigrated, appMigrated).
// Per-entry errors are logged as warnings; the entry's old key is NOT deleted.
func (r *Runner) runOne(m Migration) (int, int) {
	netMigrated := r.runNetworkingEntries(m)
	netMigrated += r.runDropInEnvEntries(m)
	appMigrated := r.runApplicationEntries(m)
	appMigrated += r.runDropEntries(m)
	return netMigrated, appMigrated
}

func (r *Runner) runNetworkingEntries(m Migration) int {
	if r.ini.isAbsent() {
		return 0
	}
	migrated := 0
	for oldKey, fn := range m.NetworkingEntries {
		val, ok := r.ini.get(oldKey)
		if !ok {
			continue
		}
		if err := fn(oldKey, val, r.net); err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: %s networking %q: %v\n", m.Name, oldKey, err)
			continue
		}
		r.ini.remove(oldKey)
		migrated++
	}
	return migrated
}

func (r *Runner) runApplicationEntries(m Migration) int {
	if r.ini.isAbsent() {
		return 0
	}
	migrated := 0
	for oldKey, fn := range m.ApplicationEntries {
		val, ok := r.ini.get(oldKey)
		if !ok {
			continue
		}
		if err := fn(oldKey, val, r.kv); err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: %s application %q: %v\n", m.Name, oldKey, err)
			continue
		}
		r.ini.remove(oldKey)
		migrated++
	}
	return migrated
}

// runDropInEnvEntries processes Migration.DropInEnvEntries.
//
// For each old ini key present in the store, it collects the (envVar, value)
// pairs and writes them all into a single systemd drop-in file atomically
// (tmp+rename, 0644, MkdirAll parent) at the effective drop-in path
// (r.dropInPath).  Successfully written keys are removed from the ini and
// counted toward the networking tally.
//
// If the file write fails, no ini keys are removed (retryable on next run).
// If no entries are present, the function is a no-op.
func (r *Runner) runDropInEnvEntries(m Migration) int {
	if r.ini.isAbsent() || len(m.DropInEnvEntries) == 0 {
		return 0
	}

	// Collect entries present in the ini.
	type pair struct {
		oldKey string
		envVar string
		value  string
	}
	var found []pair
	for oldKey, envVar := range m.DropInEnvEntries {
		val, ok := r.ini.get(oldKey)
		if !ok {
			continue
		}
		found = append(found, pair{oldKey: oldKey, envVar: envVar, value: val})
	}
	if len(found) == 0 {
		return 0
	}

	// Build drop-in content (one Environment line per entry).
	content := "[Service]\n"
	for _, p := range found {
		content += fmt.Sprintf("Environment=\"%s=%s\"\n", p.envVar, p.value)
	}

	dest := r.dropInPath

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "  WARNING: %s drop-in: create dir %s: %v\n", m.Name, filepath.Dir(dest), err)
		return 0
	}

	// Write atomically via temp file + rename.
	tmp, err := os.CreateTemp(filepath.Dir(dest), "drop-in-*.conf.tmp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  WARNING: %s drop-in: create temp: %v\n", m.Name, err)
		return 0
	}
	tmpName := tmp.Name()

	if _, err := fmt.Fprint(tmp, content); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		fmt.Fprintf(os.Stderr, "  WARNING: %s drop-in: write temp: %v\n", m.Name, err)
		return 0
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		fmt.Fprintf(os.Stderr, "  WARNING: %s drop-in: close temp: %v\n", m.Name, err)
		return 0
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		fmt.Fprintf(os.Stderr, "  WARNING: %s drop-in: chmod temp: %v\n", m.Name, err)
		return 0
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		fmt.Fprintf(os.Stderr, "  WARNING: %s drop-in: rename to %s: %v\n", m.Name, dest, err)
		return 0
	}

	// Success: remove all written keys from the ini.
	for _, p := range found {
		r.ini.remove(p.oldKey)
	}
	return len(found)
}

func (r *Runner) runDropEntries(m Migration) int {
	if r.ini.isAbsent() {
		return 0
	}
	dropped := 0
	for _, oldKey := range m.DropEntries {
		if _, ok := r.ini.get(oldKey); !ok {
			continue
		}
		r.ini.remove(oldKey)
		dropped++
	}
	return dropped
}
