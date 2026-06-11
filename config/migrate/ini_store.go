// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/ini.v1"
)

// iniStore is the legacy source store backed by a Python-style config.ini file.
// Keys use the "section.key" dot-notation convention.
//
// After a migration run, if ZERO non-empty keys remain across all real sections
// (ignoring the DEFAULT section and any empty sections), the file is renamed to
// "<path>.migrated" instead of being saved.  Keys that are not consumed by the
// current binary's registered migrations survive untouched.
//
// If the INI file does not exist, all get() calls return ("", false) and the
// store is considered absent — migration entries that depend on it are skipped
// without error (mirrors extensions isLoaded()==false → networking 0/m).
type iniStore struct {
	path string
	cfg  *ini.File
	// absent is true when the file did not exist at load time.
	absent bool
}

// newIniStore loads the INI from path.  If path does not exist the store is
// created in the "absent" state — get() always returns ("", false).
func newIniStore(path string) (*iniStore, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &iniStore{path: path, absent: true}, nil
	}
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("migrate: load ini %s: %w", path, err)
	}
	return &iniStore{path: path, cfg: cfg}, nil
}

// isAbsent reports whether the file was missing at load time.
func (s *iniStore) isAbsent() bool { return s.absent }

// get returns the value for "section.key" and true if present, or ("", false).
func (s *iniStore) get(sectionKey string) (string, bool) {
	if s.absent {
		return "", false
	}
	section, key, ok := splitSectionKey(sectionKey)
	if !ok {
		return "", false
	}
	sec, err := s.cfg.GetSection(section)
	if err != nil {
		return "", false
	}
	k, err := sec.GetKey(key)
	if err != nil {
		return "", false
	}
	v := k.String()
	if v == "" {
		return "", false
	}
	return v, true
}

// remove deletes the "section.key" from the in-memory INI.
func (s *iniStore) remove(sectionKey string) {
	if s.absent {
		return
	}
	section, key, ok := splitSectionKey(sectionKey)
	if !ok {
		return
	}
	sec, err := s.cfg.GetSection(section)
	if err != nil {
		return
	}
	sec.DeleteKey(key)
}

// save writes the in-memory INI state back to disk, or renames the file to
// "<path>.migrated" if no non-empty keys remain (across all real sections,
// excluding the ini DEFAULT section).
//
// save is a no-op when the store is absent.
func (s *iniStore) save() error {
	if s.absent {
		return nil
	}
	if s.isEmpty() {
		return os.Rename(s.path, s.path+".migrated")
	}
	return s.cfg.SaveTo(s.path)
}

// isEmpty reports whether all real sections (excluding DEFAULT) have zero
// non-empty keys.
func (s *iniStore) isEmpty() bool {
	if s.absent {
		return true
	}
	for _, sec := range s.cfg.Sections() {
		if sec.Name() == ini.DefaultSection {
			continue
		}
		for _, k := range sec.Keys() {
			if k.String() != "" {
				return false
			}
		}
	}
	return true
}

// splitSectionKey splits "section.key" into (section, key, true) using the
// same rule as the Python configparser loader:
//
//   - If the string contains two or more dots, the section is everything up to
//     the LAST dot and the key is the final component.
//     e.g. "carbonio.preview.default_host" → section="carbonio.preview", key="default_host"
//   - If the string contains exactly one dot, split on it.
//     e.g. "log.format" → section="log", key="format"
//   - No dot → cannot split → returns ("", "", false).
func splitSectionKey(sectionKey string) (section, key string, ok bool) {
	idx := strings.LastIndexByte(sectionKey, '.')
	if idx < 0 {
		return "", "", false
	}
	return sectionKey[:idx], sectionKey[idx+1:], true
}
