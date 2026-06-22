// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command configdocs generates configuration documentation files from the
// key registry:
//
//   - docs/configs.md — Markdown pipe-table format, committed and embedded in
//     the binary via docs.ConfigsMd() (the docs package uses go:embed on it).
//
// It is invoked via the go:generate directive in config/registry.go:
//
//	//go:generate go run ../cmd/configdocs
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/configdocs"
)

func main() {
	// Determine the repo root by walking up from cwd until go.mod is found.
	// The go:generate directive in config/ sets the working directory to the
	// package directory; direct invocations from the repo root also work because
	// go.mod is found on the first iteration.
	root := repoRoot()

	keys := config.RegisteredKeys()
	raw := make([]configdocs.RawKey, len(keys))
	for i, k := range keys {
		raw[i] = configdocs.RawKey{
			Key:            k.Key,
			Namespace:      string(k.Namespace),
			Default:        k.Default,
			IfNotPresent:   k.IfNotPresent,
			HiddenFromDocs: k.HiddenFromDocs,
		}
	}

	docs := configdocs.BuildDocs(config.ServiceName, config.ShortName, raw)

	md := configdocs.RenderMd(docs)

	// Write docs/configs.md — the single committed copy. The docs package
	// embeds this file at compile time (go:embed configs.md), so the binary
	// serves it via docs.ConfigsMd() without a separate config/configs.md.
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
