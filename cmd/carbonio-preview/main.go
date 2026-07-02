// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command carbonio-preview is the entry point for the carbonio-preview-ce service.
//
// It wires together the config, storage, and server packages and starts the HTTP
// server.
//
// Configuration is loaded from the extensions-equivalent chain:
//   - Registry defaults
//   - /etc/carbonio/preview/config.properties  (networking layer)
//   - Consul KV carbonio-preview/?recurse       (application layer)
//   - Environment variables: NETWORKING_CONFIG_* / APPLICATION_CONFIG_*
//
// Process-level variables (not part of the chain):
//
//   - PREVIEW_LOG_LEVEL:             log verbosity (default: info)
//   - PREVIEW_PDFIUM_WORKER_PATH:    override path to the pdfium-worker binary
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
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/zextras/carbonio-preview-ce/v2/cache"
	"github.com/zextras/carbonio-preview-ce/v2/config"
	"github.com/zextras/carbonio-preview-ce/v2/config/migrate"
	// Blank-imported so its init() registers the "ce" migration set into the
	// config/migrate framework (RegisterInSet). Without this import, no CE
	// migrations exist to run at --setup time — a plain import of
	// config/migrate no longer carries them (that's the whole point: it lets
	// the Advanced binary import config/migrate for the framework alone,
	// without inheriting CE's migrations).
	_ "github.com/zextras/carbonio-preview-ce/v2/config/migrate/cemig"
	"github.com/zextras/carbonio-preview-ce/v2/docs"
	"github.com/zextras/carbonio-preview-ce/v2/render"
	"github.com/zextras/carbonio-preview-ce/v2/server"
	"github.com/zextras/carbonio-preview-ce/v2/storage"
)

func main() {
	// Intercept --setup <consul-url> BEFORE the config chain.
	if handled, code := runSetupIfRequested(os.Args[1:]); handled {
		os.Exit(code)
	}

	// Load config (registry defaults → config.properties / Consul KV → ENV).
	// Failure is fatal: an unreachable Consul or a bad value means the service
	// cannot start with a consistent configuration.
	if err := config.Load(); err != nil {
		slog.Error("FATAL: config.Load failed", "err", err)
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
		"upload",
		"delete",
		storageTimeout,
	)

	// Build the in-process rendered-output cache (0 MiB ⇒ nil ⇒ disabled).
	outCache := cache.New(cfg.CacheMaxBytes)

	// Create the server. The video-preview DB is NEVER a boot-time hard
	// dependency: preview must serve image/pdf/document/health regardless of DB
	// state. Video previews are enabled asynchronously once the DB is ready.
	srv := server.New(cfg, storageClient, outCache)

	// Root context cancelled on shutdown, used by the background DB init and the
	// video worker sweep goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// StartVideoDBAsync is the SHARED helper (server package) that encapsulates
	// the DSN-empty-vs-background-retry decision, so CE's and Advanced's main
	// can never diverge on how the video DB comes up.
	srv.StartVideoDBAsync(ctx, cfg)

	// Run blocks until SIGTERM/SIGINT; it always starts the HTTP listener.
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

// runSetupIfRequested intercepts the --setup <consul-url> flag.
//
// It returns handled=false when --setup is absent (the caller continues with the
// normal server boot). When --setup is present it runs the config migration and
// returns handled=true with the process exit code (0 on success, 1 on a missing
// URL or a RunSetup failure). It prints usage/error messages to stderr but never
// calls os.Exit itself, so it is directly unit-testable; main() owns the exit.
func runSetupIfRequested(args []string) (handled bool, exitCode int) {
	idx := findArg(args, "--setup")
	if idx < 0 {
		return false, 0
	}
	if idx+1 >= len(args) {
		fmt.Fprintln(os.Stderr, "Usage: --setup <consul-url>")
		fmt.Fprintln(os.Stderr, "Example: --setup http://127.0.0.1:8500")
		return true, 1
	}
	consulURL := args[idx+1]

	paths := migrate.Paths{
		IniPath:   "/etc/carbonio/preview/config.ini",
		PropsPath: "/etc/carbonio/preview/config.properties",
		// migrationSet is HARDCODED (dev-only), NOT operator/KV config: each
		// edition owns its migration set (mirrors extensions' BUILD_TIME
		// quarkus.carbonio-bootstrap.migrations-package). CE always runs "ce";
		// Advanced's own main hardcodes "advanced".
		MigrationSet: "ce",
	}
	if err := migrate.RunSetup(consulURL, paths, docs.ConfigsMd()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 1
	}
	return true, 0
}
