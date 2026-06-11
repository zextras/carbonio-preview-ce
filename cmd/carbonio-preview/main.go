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
		args := os.Args[1:]
		if idx+1 >= len(args) {
			fmt.Fprintln(os.Stderr, "Usage: --setup <consul-url>")
			fmt.Fprintln(os.Stderr, "Example: --setup http://127.0.0.1:8500")
			os.Exit(1)
		}
		consulURL := args[idx+1]
		paths := migrate.Paths{
			IniPath:   "/etc/carbonio/preview/config.ini",
			PropsPath: "/etc/carbonio/preview/config.properties",
		}
		if err := migrate.RunSetup(consulURL, paths, config.ConfigsTxt()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
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

// findArg returns the index of name in args, or -1 if not found.
func findArg(args []string, name string) int {
	for i, a := range args {
		if a == name {
			return i
		}
	}
	return -1
}
