// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zextras/carbonio-preview-ce/cache"
	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
	grpcserver "github.com/zextras/carbonio-preview-ce/server/grpc"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// Server is the top-level HTTP server. Create it with New and start it with Run.
type Server struct {
	cfg   *config.Config
	store storage.Client
	cache *cache.Cache
}

// New constructs a Server. cfg and store must not be nil. c may be nil (cache disabled).
func New(cfg *config.Config, store storage.Client, c *cache.Cache) *Server {
	return &Server{cfg: cfg, store: store, cache: c}
}

// Run starts the server. It blocks until the process receives SIGTERM or SIGINT.
func (s *Server) Run() {
	// Init libvips.
	if err := render.InitVips("carbonio-preview"); err != nil {
		slog.Error("Run: InitVips", "err", err)
		os.Exit(1)
	}
	render.SetVipsConcurrency(s.cfg.VIPSConcurrency)

	// Resolve the pdfium-worker binary path.
	// Env PREVIEW_PDFIUM_WORKER_PATH overrides the default (dir-of-executable/carbonio-preview-pdfium-worker).
	workerBin := os.Getenv("PREVIEW_PDFIUM_WORKER_PATH")
	if workerBin == "" {
		exe, err := os.Executable()
		if err != nil {
			slog.Error("Run: os.Executable", "err", err)
			os.Exit(1)
		}
		workerBin = filepath.Join(filepath.Dir(exe), "carbonio-preview-pdfium-worker")
	}
	if _, err := os.Stat(workerBin); err != nil {
		slog.Error("Run: pdfium-worker binary not found — build cmd/pdfium-worker and place it next to the main binary, or set PREVIEW_PDFIUM_WORKER_PATH",
			"path", workerBin, "err", err)
		os.Exit(1)
	}

	// Init PDFium multi_threaded subprocess pool.
	if err := render.PDFInit(s.cfg.PDFWorkers, workerBin); err != nil {
		slog.Error("Run: PDFInit", "err", err)
		os.Exit(1)
	}
	defer render.PDFClose()

	sem := render.BuildSemaphore(s.cfg.RenderConcurrency)

	// Build the public request handler (semaphore + mux + logging).
	handler := loggingMiddleware(s.buildMux(sem))

	addr := fmt.Sprintf("%s:%s", s.cfg.ServiceIP, s.cfg.ServicePort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
		WriteTimeout: time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
	}

	// Build and start the gRPC server on a separate port alongside HTTP.
	ps := grpcserver.NewPreviewServer(s.store, s.cfg, sem)
	grpcSrv, _ := grpcserver.GRPCServer(ps)

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

	slog.Info("carbonio-preview starting",
		"http_addr", addr,
		"grpc_addr", fmt.Sprintf("%s:%s", s.cfg.ServiceIP, s.cfg.GRPCPort),
		"render_concurrency", s.cfg.RenderConcurrency,
		"pdf_workers", s.cfg.PDFWorkers,
		"vips_concurrency", s.cfg.VIPSConcurrency,
		"storages_timeout", s.cfg.ServiceTimeoutInSeconds,
		"docs_timeout", s.cfg.ServiceDocsTimeout,
		"docs_enabled", s.cfg.AreDocsEnabled,
		"log_level", s.cfg.LogLevel.String(),
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Run: serve HTTP", "err", err)
			os.Exit(1)
		}
	}()

	go func() {
		if err := grpcserver.ListenAndServe(grpcSrv, s.cfg); err != nil {
			slog.Error("Run: serve gRPC", "err", err)
			os.Exit(1)
		}
	}()

	sig := <-sigC
	slog.Info("Run: received signal, shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("Run: HTTP shutdown", "err", err)
	}
	// GracefulStop waits for in-flight RPCs to complete (up to the same 10s window).
	grpcSrv.GracefulStop()
}

// Handler builds and returns the server's HTTP request handler: the render
// concurrency semaphore (sized from cfg.RenderConcurrency), the public mux with
// all route groups, and the logging middleware. It does NOT start a listener,
// install signal handlers, or perform render initialisation (InitVips / the
// pdfium pool) — that remains Run's responsibility. Callers embedding the
// preview server (or testing it over httptest.Server) must ensure render is
// initialised before serving requests; Handler itself is pure and side-effect
// free beyond constructing the handler chain.
func (s *Server) Handler() http.Handler {
	sem := render.BuildSemaphore(s.cfg.RenderConcurrency)
	return loggingMiddleware(s.buildMux(sem))
}

// buildMux assembles the public HTTP mux with all route groups.
func (s *Server) buildMux(sem chan struct{}) *http.ServeMux {
	mux := http.NewServeMux()
	registerImageRoutes(mux, s.cfg, s.store, s.cache, sem)
	registerPDFRoutes(mux, s.cfg, s.store, s.cache, sem)
	registerDocumentRoutes(mux, s.cfg, s.store, s.cache, sem)
	registerHealthRoutes(mux, s.cfg)
	registerDocRoutes(mux)
	return mux
}

// -------------------------------------------------------------------------
// HTTP middleware
// -------------------------------------------------------------------------

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware wraps h and emits a slog.Debug record for every request
// containing method, path (no query string), HTTP status, and duration_ms.
// No secrets are logged (headers, query strings, auth tokens are not emitted).
func loggingMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rw, r)
		slog.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
