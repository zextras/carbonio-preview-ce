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
package main

import (
	"log"
	"os"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/server"
	"github.com/zextras/carbonio-preview-ce/storage"
)

func main() {
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
