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
	"sync"
	"syscall"
	"time"

	"github.com/zextras/carbonio-preview-ce/v3/cache"
	"github.com/zextras/carbonio-preview-ce/v3/config"
	"github.com/zextras/carbonio-preview-ce/v3/db"
	"github.com/zextras/carbonio-preview-ce/v3/render"
	"github.com/zextras/carbonio-preview-ce/v3/storage"
)

// Server is the top-level HTTP server. Create it with New and start it with Run.
type Server struct {
	cfg   *config.Config
	store storage.Client
	cache *cache.Cache
	// dbGate is the request-time readiness signal for the video-preview DB.
	// It always exists (never nil). Its store starts nil (video disabled) and is
	// flipped to ready asynchronously by the caller (main.go) once the background
	// DB init succeeds. Video handlers and the worker consult it on every use, so
	// the DB can come up after boot and re-enable video with no process restart.
	dbGate *videoGate
	// worker is the video-preview background sweeper. It is constructed in
	// buildMux (gate-aware) and only actually processes rows while dbGate is
	// ready. Started by StartVideoWorker (called from main once the DB is up).
	worker *VideoWorker

	// workerStartMu guards workerStartRequested/workerStartCtx below. It makes
	// the "start the worker" request order-independent with respect to
	// buildMux constructing s.worker.
	//
	// Production ordering (main.go) is: New() [worker nil] → StartVideoDBAsync()
	// [background goroutine] → Run() [buildMux constructs worker]. If the
	// background DB init finishes and calls EnableVideoDB/StartVideoWorker
	// BEFORE Run() reaches buildMux, the old code's `if s.worker != nil` guard
	// in StartVideoWorker silently dropped the start request — buildMux later
	// assigns s.worker, but nothing ever calls StartOnce on it, so the sweep
	// goroutine never launches even though the DB gate is ready.
	//
	// The fix: whichever of {worker construction, start request} happens
	// LAST is responsible for actually calling StartOnce. If the start is
	// requested before the worker exists, it is recorded here and honoured by
	// buildMux right after s.worker is assigned. StartOnce's own atomic CAS
	// makes calling it from both paths (or multiple times) safe — it starts
	// the sweep goroutine exactly once regardless of ordering.
	workerStartMu        sync.Mutex
	workerStartRequested bool
	workerStartCtx       context.Context
}

// Option is a functional option for Server configuration.
type Option func(*Server)

// New constructs a Server. cfg and store must not be nil. c may be nil (cache
// disabled). Both editions (CE and Advanced) call New from their own main and
// then call StartVideoDBAsync(ctx, cfg) to enable video previews asynchronously
// (background pool-open + Migrate, then gate + worker start).
//
// When EnableVideoDB has not yet fired, the video-preview DB layer is disabled:
// video preview/thumbnail return 424 (Failed Dependency) and the worker does not
// process rows. Image/PDF/document/health are unaffected.
func New(cfg *config.Config, store storage.Client, c *cache.Cache, opts ...Option) *Server {
	s := &Server{cfg: cfg, store: store, cache: c, dbGate: newVideoGate()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnableVideoDB publishes a ready DB store to the gate and (idempotently) starts
// the video worker. It is safe to call from a background goroutine after the
// server is already serving: the gate flip is atomic and the handlers pick it up
// on the next request with no restart. Calling it more than once is harmless.
func (s *Server) EnableVideoDB(ctx context.Context, dbStore *db.Store) {
	s.dbGate.Set(dbStore)
	s.StartVideoWorker(ctx)
}

// StartVideoWorker starts the background sweep goroutine if a worker exists and
// has not been started yet. The worker itself no-ops each tick while the gate is
// not ready, so starting it early (or before the DB is up) is safe.
//
// If the worker has not been constructed yet (buildMux has not run), the start
// request is NOT dropped: it is recorded and honoured by buildMux as soon as
// s.worker is assigned. This makes the start order-independent with respect to
// buildMux — see the workerStartMu doc comment on Server for the full race this
// closes.
func (s *Server) StartVideoWorker(ctx context.Context) {
	s.workerStartMu.Lock()
	defer s.workerStartMu.Unlock()
	if s.worker != nil {
		s.worker.StartOnce(ctx)
		return
	}
	s.workerStartRequested = true
	s.workerStartCtx = ctx
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
	// Single-clock design: the per-request context deadline (set by video
	// handlers via context.WithTimeout(r.Context(), ServiceTimeoutInSeconds))
	// is the ONE authoritative operation cap. WriteTimeout is 0 so the server
	// does not race against that per-request ctx with its own independent
	// clock. ReadTimeout is kept to bound reading the inbound request headers/
	// body (a separate, harmless concern that does not overlap with the handler
	// ctx deadline).
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
		WriteTimeout: 0,
	}

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

	slog.Info("carbonio-preview starting",
		"addr", addr,
		"render_concurrency", s.cfg.RenderConcurrency,
		"pdf_workers", s.cfg.PDFWorkers,
		"video_concurrency", s.cfg.VideoConcurrency,
		"vips_concurrency", s.cfg.VIPSConcurrency,
		"storages_timeout", s.cfg.ServiceTimeoutInSeconds,
		"docs_timeout", s.cfg.ServiceDocsTimeout,
		"docs_enabled", s.cfg.AreDocsEnabled,
		"openapi_enabled", s.cfg.OpenAPIEnabled,
		"log_level", s.cfg.LogLevel.String(),
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Run: ListenAndServe", "err", err)
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
}

// Handler builds and returns the server's HTTP request handler. It does NOT
// start a listener or perform render initialisation — that remains Run's
// responsibility. Callers embedding the preview server (or testing it over
// httptest.Server) must ensure render is initialised before serving requests.
func (s *Server) Handler() http.Handler {
	sem := render.BuildSemaphore(s.cfg.RenderConcurrency)
	return loggingMiddleware(s.buildMux(sem))
}

// buildMux assembles the public HTTP mux with all route groups.
// All resource operations (image, health, pdf, document, video-generate) are
// registered under huma (code-first OpenAPI).
//
// huma also serves the OpenAPI document itself (/openapi.json|.yaml,
// /openapi-3.0.json|.yaml) and a Swagger UI at /docs — but only when the
// operator opts in via the "openapi.enabled" application key. It is off by
// default, so those paths 404 on a stock install. See newHumaAPI.
//
// sem is the shared render semaphore (image/PDF/document), sized by the
// image-document.max-concurrent-operations key. The dedicated video semaphore is built
// here from cfg.VideoConcurrency (APPLICATION key
// video.max-concurrent-extractions, default runtime.NumCPU()) so a flood of
// video jobs can never starve image previews, and vice-versa.
func (s *Server) buildMux(sem chan struct{}) *http.ServeMux {
	mux := http.NewServeMux()

	videoSem := make(chan struct{}, s.cfg.VideoConcurrency)

	// Register all operations under huma. The video worker is constructed here
	// (gate-aware) and retained on the Server so it can be started once the DB is
	// ready (EnableVideoDB / StartVideoWorker).
	api := newHumaAPI(mux, s.cfg)
	worker := RegisterOperations(api, Deps{
		Cfg:      s.cfg,
		Store:    s.store,
		Cache:    s.cache,
		Sem:      sem,
		VideoSem: videoSem,
		DBGate:   s.dbGate,
	})

	// Assign s.worker and check/honour a deferred start request atomically
	// under the SAME lock that StartVideoWorker uses to read/write s.worker.
	// This closes the race where EnableVideoDB/StartVideoWorker fires
	// concurrently with buildMux: without the shared lock, the assignment
	// below and the `if s.worker != nil` read in StartVideoWorker can race
	// (flagged by `go test -race`). See the workerStartMu doc comment on
	// Server for the full ordering issue this pairing fixes. StartOnce's
	// atomic CAS makes calling it here safe even if the normal post-ready
	// path also calls it (whichever runs first wins; the other is a no-op).
	s.workerStartMu.Lock()
	s.worker = worker
	if s.workerStartRequested {
		s.worker.StartOnce(s.workerStartCtx)
	}
	s.workerStartMu.Unlock()

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
