// Command configdocs generates two configuration documentation files from the
// key registry:
//
//   - docs/configs.md    — Markdown pipe-table format
//   - config/configs.txt — Unicode box-table plain-text (embedded in binary)
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
	"github.com/zextras/carbonio-preview-ce/internal/configdocs"
)

func main() {
	// Determine the repo root.  The go:generate directive in config/ changes
	// the working directory to the package directory, so we walk up one level.
	// When invoked directly from the repo root the working directory is already
	// correct.  We detect by looking for go.mod.
	root := repoRoot()

	keys := config.RegisteredKeys()
	raw := make([]configdocs.RawKey, len(keys))
	for i, k := range keys {
		raw[i] = configdocs.RawKey{
			Key:          k.Key,
			Namespace:    string(k.Namespace),
			Default:      k.Default,
			IfNotPresent: k.IfNotPresent,
		}
	}

	docs := configdocs.BuildDocs(config.ServiceName, config.ShortName, raw)

	// Write config/configs.txt
	txtPath := filepath.Join(root, "config", "configs.txt")
	if err := os.MkdirAll(filepath.Dir(txtPath), 0o755); err != nil {
		log.Fatalf("configdocs: mkdir %s: %v", filepath.Dir(txtPath), err)
	}
	if err := os.WriteFile(txtPath, []byte(configdocs.RenderTxt(docs)), 0o644); err != nil {
		log.Fatalf("configdocs: write configs.txt: %v", err)
	}
	log.Printf("configdocs: wrote %s", txtPath)

	// Write docs/configs.md
	mdPath := filepath.Join(root, "docs", "configs.md")
	if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
		log.Fatalf("configdocs: mkdir %s: %v", filepath.Dir(mdPath), err)
	}
	if err := os.WriteFile(mdPath, []byte(configdocs.RenderMd(docs)), 0o644); err != nil {
		log.Fatalf("configdocs: write configs.md: %v", err)
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
