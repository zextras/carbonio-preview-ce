// Command carbonio-preview is the entry point for the carbonio-preview-ce service.
//
// It wires together the config, storage, and server packages and starts the HTTP
// server. The binary can run in two roles, controlled by the ROLE environment variable:
//
//   - ROLE=pdfworker  — PDF rendering worker (internal port, SO_REUSEPORT)
//   - (default)       — main process (public port, spawns PDF workers, relays PDF requests)
//
// Configuration is read from /etc/carbonio/preview/config.ini (if present) and from
// environment variable overrides (PREVIEW_HOST, PREVIEW_PORT, STORAGES_HOST, etc.).
package main

import (
	"log"
	"time"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/server"
	"github.com/zextras/carbonio-preview-ce/storage"
)

func main() {
	// Load config (defaults → config.ini → env vars).
	if err := config.Load(); err != nil {
		// Non-fatal: Load() falls back to hardcoded defaults on any error.
		log.Printf("config.Load: %v (using defaults)", err)
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
