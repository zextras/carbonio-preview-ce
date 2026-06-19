// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command gendocs synchronises the OpenAPI specification artefacts.
//
// The source of truth for the API contract is docs/openapi.yaml (human-editable
// YAML, checked into version control). This generator:
//
//  1. Reads docs/openapi.yaml and converts it to JSON.
//  2. Writes docs/openapi.json (canonical JSON copy).
//  3. Copies docs/openapi.json to server/static/openapi.json so that the
//     go:embed directive in server/docs.go can embed it without cross-package
//     path restrictions (go:embed does not allow ../.. paths).
//
// Invoke via go:generate in server/docs.go:
//
//	//go:generate go run ../cmd/gendocs
//
// Or directly from the repo root:
//
//	go run ./cmd/gendocs
//
// The generator requires Python 3 with PyYAML (python3 -c "import yaml").
// On Debian/Ubuntu: apt install python3-yaml.
// This is a build-time only dependency — it is NOT required at runtime.
package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	root := repoRoot()

	yamlPath := filepath.Join(root, "docs", "openapi.yaml")
	docsJSONPath := filepath.Join(root, "docs", "openapi.json")
	staticJSONPath := filepath.Join(root, "server", "static", "openapi.json")

	// Convert YAML to JSON via Python3+PyYAML (lightweight build-time dep).
	// We use Python because adding a Go YAML library just for this generator
	// would pull an unnecessary runtime dependency into go.mod.
	jsonBytes := yamlToJSON(yamlPath)

	// Pretty-print the JSON for readability and stable diffs.
	var obj interface{}
	if err := json.Unmarshal(jsonBytes, &obj); err != nil {
		log.Fatalf("gendocs: json.Unmarshal: %v", err)
	}
	pretty, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		log.Fatalf("gendocs: json.MarshalIndent: %v", err)
	}
	pretty = append(pretty, '\n')

	// Write docs/openapi.json
	if err := os.WriteFile(docsJSONPath, pretty, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", docsJSONPath, err)
	}
	log.Printf("gendocs: wrote %s", docsJSONPath)

	// Write server/static/openapi.json (go:embed target).
	if err := os.MkdirAll(filepath.Dir(staticJSONPath), 0o755); err != nil {
		log.Fatalf("gendocs: mkdir %s: %v", filepath.Dir(staticJSONPath), err)
	}
	if err := os.WriteFile(staticJSONPath, pretty, 0o644); err != nil {
		log.Fatalf("gendocs: write %s: %v", staticJSONPath, err)
	}
	log.Printf("gendocs: wrote %s", staticJSONPath)
}

// yamlToJSON converts a YAML file to JSON bytes using python3 + PyYAML.
// Exits with a descriptive error if python3 or PyYAML is not available.
func yamlToJSON(yamlPath string) []byte {
	script := `import sys, yaml, json; print(json.dumps(yaml.safe_load(open(sys.argv[1]))))`
	out, err := exec.Command("python3", "-c", script, yamlPath).Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		log.Fatalf("gendocs: python3 yaml→json conversion failed: %v\nstderr: %s\n"+
			"Ensure python3 and PyYAML are installed (apt install python3-yaml).", err, stderr)
	}
	return out
}

// repoRoot returns the directory containing go.mod, searching upward from cwd.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("gendocs: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatalf("gendocs: could not locate repo root (go.mod not found)")
		}
		dir = parent
	}
}
