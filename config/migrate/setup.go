// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"os"
	"strings"
)

// RunSetup executes registered config migrations and prints config documentation.
//
// It mirrors SetupAwareMain from carbonio-quarkus-extensions:
//   - Reads SETUP_CONSUL_TOKEN from the environment.
//   - Gates the token check: SETUP_CONSUL_TOKEN is required only when application
//     entries will actually run (ini present and contains at least one application
//     key).
//   - Runs migrations, then always prints configsMd (a blank line followed by
//     the embedded configs.md Markdown content).
//   - Returns a non-nil error on any failure; the caller is responsible for
//     exiting with a non-zero code.
//
// paths holds the injectable file paths and Consul URL; the production caller
// passes the production paths, tests pass t.TempDir() paths directly.
// configsMd is the embedded configs.md content (config.ConfigsMd()).
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

// printDocs prints a blank line followed by the embedded configs.md content.
// Mirrors SetupAwareMain.printConfigDocumentation.
func printDocs(configsMd string) {
	fmt.Println()
	fmt.Print(configsMd)
}
