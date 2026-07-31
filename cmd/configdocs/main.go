// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command configdocs generates configuration documentation files from the
// key registry:
//
//   - docs/configs.md -- Markdown pipe-table format, committed as build-time
//     generated OUTPUT only. Nothing embeds or reads it back: the binary
//     re-renders the same Markdown from the compiled-in registry via
//     config.ConfigsMd() when `--setup` runs.
//
// It is invoked via the go:generate directive in config/registry.go:
//
//	//go:generate go run ../cmd/configdocs
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/zextras/carbonio-preview-ce/v3/config"
)

func main() {
	// Determine the repo root by walking up from cwd until go.mod is found.
	// The go:generate directive in config/ sets the working directory to the
	// package directory; direct invocations from the repo root also work because
	// go.mod is found on the first iteration.
	root := repoRoot()

	// Exactly the string the binary prints at --setup time -- same registry,
	// same renderer -- so the committed file cannot drift from the runtime output.
	md := config.ConfigsMd()

	mdPath := filepath.Join(root, "docs", "configs.md")
	if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
		log.Fatalf("configdocs: mkdir %s: %v", filepath.Dir(mdPath), err)
	}
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		log.Fatalf("configdocs: write docs/configs.md: %v", err)
	}
	log.Printf("configdocs: wrote %s", mdPath)
}

// repoRoot returns the directory containing go.mod, searching upward from cwd.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("configdocs: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatalf("configdocs: could not locate repo root (go.mod not found)")
		}
		dir = parent
	}
}
