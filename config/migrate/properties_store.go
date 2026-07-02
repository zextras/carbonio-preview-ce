// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// propertiesStore is a ConfigStore backed by a Java-style .properties file.
// It loads the file into memory on construction and exposes Set/Get/Remove.
// Save() writes the full in-memory map back to disk with a header comment.
// The file is written only if at least one entry was actually written via Set()
// (tracked by the dirty flag).
type propertiesStore struct {
	path  string
	props map[string]string
	dirty bool // true after at least one Set() call
}

// newPropertiesStore loads the properties file at path.
// If the file does not exist it creates an empty, loaded store so that
// networking migrations can still write new entries.
func newPropertiesStore(path string) (*propertiesStore, error) {
	s := &propertiesStore{path: path, props: make(map[string]string)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *propertiesStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // treat missing as empty
		}
		return fmt.Errorf("migrate: open properties %s: %w", s.path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if key != "" {
			s.props[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("migrate: read properties %s: %w", s.path, err)
	}
	return nil
}

// Set implements ConfigStore. Sets dirty=true so the runner saves the file.
func (s *propertiesStore) Set(key, value string) error {
	s.props[key] = value
	s.dirty = true
	return nil
}

// Save writes the current in-memory map to disk with a header comment.
// The directory is created if it does not exist.
func (s *propertiesStore) Save() error {
	if err := os.MkdirAll(dirOf(s.path), 0o755); err != nil {
		return fmt.Errorf("migrate: create dir for properties: %w", err)
	}
	f, err := os.Create(s.path)
	if err != nil {
		return fmt.Errorf("migrate: create properties %s: %w", s.path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "# Migrated by carbonio-preview setup")
	for k, v := range s.props {
		fmt.Fprintf(w, "%s=%s\n", k, v)
	}
	return w.Flush()
}

// dirOf returns the directory part of a file path.
func dirOf(path string) string {
	idx := strings.LastIndexByte(path, '/')
	if idx < 0 {
		return "."
	}
	return path[:idx]
}
