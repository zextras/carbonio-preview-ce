// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"os"
	"strings"
)

// RunSetup executes the init-bootstrap + KV-move series belonging to
// paths.MigrationSet and prints config documentation.
//
// It mirrors SetupAwareMain from carbonio-quarkus-extensions' package-scoped
// migrations:
//   - Reads SETUP_CONSUL_TOKEN from the environment.
//   - Gates the token check: SETUP_CONSUL_TOKEN is required only when application
//     work will actually run (the set's Bootstrap, if any, has an ini present
//     containing at least one application key, OR the set has any Vn migration
//     registered — those talk to Consul KV regardless of the ini).
//   - Runs only the bootstrap/migrations registered under paths.MigrationSet
//     (e.g. "ce"), never another edition's set, then always prints configsMd
//     (a blank line followed by the configs.md Markdown content).
//   - Returns a non-nil error on any failure; the caller is responsible for
//     exiting with a non-zero code.
//
// paths holds the injectable file paths, Consul URL, and MigrationSet name;
// the production caller passes the production paths, tests pass t.TempDir()
// paths directly.
//
// configsMd is the Markdown config reference, passed in rather than looked up
// so this package stays independent of the config package (each edition's main
// supplies its own registry's rendering -- CE passes config.ConfigsMd()). It is
// rendered from the compiled-in key registry at call time, never read from
// docs/configs.md, which is build-time generated output only.
func RunSetup(consulURL string, paths Paths, configsMd string) error {
	token := strings.TrimSpace(os.Getenv("SETUP_CONSUL_TOKEN"))
	paths.ConsulURL = consulURL
	paths.ConsulToken = token

	runner, err := NewRunner(paths)
	if err != nil {
		printDocs(configsMd)
		return fmt.Errorf("setup failed: %w", err)
	}

	// Gate on token only when application-layer work is actually needed.
	if runner.HasApplicationWork() && token == "" {
		printDocs(configsMd)
		return fmt.Errorf("error: SETUP_CONSUL_TOKEN environment variable is not set")
	}

	runner.Run()
	printDocs(configsMd)
	return nil
}

// printDocs prints a blank line followed by the configs.md content.
// Mirrors SetupAwareMain.printConfigDocumentation.
func printDocs(configsMd string) {
	fmt.Println()
	fmt.Print(configsMd)
}
