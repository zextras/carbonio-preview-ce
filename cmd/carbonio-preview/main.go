// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command carbonio-preview is the entry point for the carbonio-preview-ce service.
//
// It wires together the config, storage, and server packages and starts the HTTP
// server. The binary can run in two roles, controlled by the ROLE environment variable:
//
//   - ROLE=pdfworker  — PDF rendering worker (internal port, SO_REUSEPORT)
//   - (default)       — main process (public port, spawns PDF workers, relays PDF requests)
//
// Configuration is loaded from the extensions-equivalent chain:
//   - Registry defaults
//   - /etc/carbonio/preview/config.properties  (networking layer)
//   - Consul KV carbonio-preview/?recurse       (application layer)
//   - Environment variables: NETWORKING_CONFIG_* / APPLICATION_CONFIG_*
//
// Process-level variables (not part of the chain): ROLE, PDF_INTERNAL_PORT.
//
// Setup mode:
//
//	carbonio-preview --setup <consul-url>
//
// Intercepts the --setup flag BEFORE the config chain runs (pure, no Consul
// fetch) and executes all registered config migrations. Exits 0 on success,
// 1 on failure. Always prints config documentation after the run.
// SETUP_CONSUL_TOKEN is required only if application entries are actually
// present in the legacy config.ini.
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/config/migrate"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/server"
	"github.com/zextras/carbonio-preview-ce/storage"
)

func main() {
	// Intercept --setup <consul-url> BEFORE the config chain.
	if idx := findArg(os.Args[1:], "--setup"); idx >= 0 {
		runSetup(os.Args[1:], idx)
		return
	}

	// Load config (registry defaults → config.properties / Consul KV → ENV).
	// Failure is fatal: an unreachable Consul or a bad value means the service
	// cannot start with a consistent configuration.
	if err := config.Load(); err != nil {
		log.Printf("FATAL: config.Load failed: %v", err)
		os.Exit(1)
	}

	cfg := &config.App

	// Propagate the configured minimum resolution to the render package so that
	// ConvertRequestedSize applies the operator-configured IMAGE_MIN_RES value
	// (default 80) instead of the package default.
	render.SetImageMinRes(cfg.ImageMinimumResolution)

	// Build the storage client.
	// CE uses DirectClient: a single-endpoint HTTP client to carbonio-storages.
	// ownerID is passed to RetrieveData but ignored by DirectClient;
	// Advanced's PowerStore implementation will use it for server routing.
	storageTimeout := time.Duration(cfg.ServiceTimeoutInSeconds) * time.Second
	storageClient := storage.NewDirectClient(
		cfg.StorageFullAddress,
		cfg.StorageDownloadAPI,
		storageTimeout,
	)

	// Create and run the server.
	srv := server.New(cfg, storageClient)
	srv.Run()
}

// runSetup executes registered config migrations and exits.
// It mirrors SetupAwareMain from carbonio-quarkus-extensions:
//   - Validates the consul-url argument.
//   - Checks SETUP_CONSUL_TOKEN is set only when application entries will
//     actually run (ini present + contains at least one application key).
//   - Runs migrations, then always prints config documentation.
//   - Exits 0 on success, 1 on failure.
func runSetup(args []string, setupIdx int) {
	if setupIdx+1 >= len(args) {
		fmt.Fprintln(os.Stderr, "Usage: --setup <consul-url>")
		fmt.Fprintln(os.Stderr, "Example: --setup http://127.0.0.1:8500")
		os.Exit(1)
	}
	consulURL := args[setupIdx+1]

	iniPath := "/etc/carbonio/preview/config.ini"
	propsPath := "/etc/carbonio/preview/config.properties"
	// Test hook: allow injecting temp paths without touching production logic.
	if v := os.Getenv("CARBONIO_PREVIEW_TEST_INI_PATH"); v != "" {
		iniPath = v
	}
	if v := os.Getenv("CARBONIO_PREVIEW_TEST_PROPS_PATH"); v != "" {
		propsPath = v
	}

	runner, err := migrate.NewRunner(migrate.Paths{
		IniPath:   iniPath,
		PropsPath: propsPath,
		ConsulURL: consulURL,
		// Token not set yet — we check below.
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		printConfigDocumentation()
		os.Exit(1)
	}

	// Gate on token only when application-layer work is actually needed.
	token := strings.TrimSpace(os.Getenv("SETUP_CONSUL_TOKEN"))
	if runner.HasApplicationWork() && token == "" {
		fmt.Fprintln(os.Stderr, "Error: SETUP_CONSUL_TOKEN environment variable is not set.")
		printConfigDocumentation()
		os.Exit(1)
	}

	// Re-build runner with the token now that we know it's available (or not needed).
	runner, err = migrate.NewRunner(migrate.Paths{
		IniPath:     iniPath,
		PropsPath:   propsPath,
		ConsulURL:   consulURL,
		ConsulToken: token,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		printConfigDocumentation()
		os.Exit(1)
	}

	runner.Run()
	printConfigDocumentation()
}

// printConfigDocumentation prints a blank line followed by the embedded
// configs.txt content.  Mirrors SetupAwareMain.printConfigDocumentation.
func printConfigDocumentation() {
	fmt.Println()
	fmt.Print(config.ConfigsTxt())
}

// findArg returns the index of name in args, or -1 if not found.
func findArg(args []string, name string) int {
	for i, a := range args {
		if a == name {
			return i
		}
	}
	return -1
}
