// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"os"
)

// Runner executes all registered migrations in version-ascending order.
// It is wired to three stores:
//   - ini: legacy source (config.ini) — read-only source, deleted/renamed after run
//   - net: networking destination (config.properties) — written if any net entry migrated
//   - kv:  application destination (Consul KV) — written per-entry
type Runner struct {
	ini *iniStore
	net *propertiesStore
	kv  *consulKvStore
}

// HasApplicationWork returns true if at least one registered migration has an
// application entry whose old key is present in the legacy ini.  This is used
// by --setup to decide whether SETUP_CONSUL_TOKEN is required before any
// modification is made.
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
	return &Runner{ini: ini, net: net, kv: kv}, nil
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

	netModified := false

	for _, m := range migrations {
		netMigrated, appMigrated := r.runOne(m)
		if netMigrated > 0 {
			netModified = true
		}
		totalApp := len(m.ApplicationEntries) + len(m.DropEntries)
		fmt.Printf("  %s: networking %d/%d, application %d/%d migrated\n",
			m.Name, netMigrated, len(m.NetworkingEntries), appMigrated, totalApp)
	}

	if netModified {
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

// runOne runs a single migration's networking, application, and drop entries.
// It returns (netMigrated, appMigrated).
// Per-entry errors are logged as warnings; the entry's old key is NOT deleted.
func (r *Runner) runOne(m Migration) (int, int) {
	netMigrated := r.runNetworkingEntries(m)
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
