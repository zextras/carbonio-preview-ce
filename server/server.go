package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/zextras/carbonio-preview-ce/config"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// Server is the top-level HTTP server. Create it with New and start it with Run.
type Server struct {
	cfg   *config.Config
	store storage.Client
}

// New constructs a Server. cfg and store must not be nil.
func New(cfg *config.Config, store storage.Client) *Server {
	return &Server{cfg: cfg, store: store}
}

// Run starts the server. It blocks until the process receives SIGTERM or SIGINT.
//
// Role selection:
//   - ROLE=pdfworker → run as an internal PDF worker (PDFium + libvips, SO_REUSEPORT,
//     internal port, exposes /render/pdf only).
//   - Otherwise → run as main process (spawn PDF workers, serve public port, relay PDF
//     requests to workers, handle image/SVG in-process).
func (s *Server) Run() {
	if s.cfg.Role == "pdfworker" {
		s.runPDFWorker()
	} else {
		s.runMain()
	}
}

// -------------------------------------------------------------------------
// PDF worker role
// -------------------------------------------------------------------------

// runPDFWorker runs this process as a PDF rendering worker.
// Binds the internal port with SO_REUSEPORT, initialises PDFium + libvips,
// and serves the /render/pdf endpoint.
func (s *Server) runPDFWorker() {
	// Init libvips.
	if err := render.InitVips("carbonio-preview-worker"); err != nil {
		slog.Error("runPDFWorker: InitVips", "err", err)
		os.Exit(1)
	}
	render.SetVipsConcurrency(s.cfg.VIPSConcurrency)

	// Init PDFium.
	render.PDFWorkerInit()
	defer render.PDFWorkerClose()

	sem := render.BuildSemaphore(s.cfg.ServiceWorkers)

	mux := http.NewServeMux()
	mux.HandleFunc(internalPDFRenderPath, func(w http.ResponseWriter, r *http.Request) {
		handleInternalPDFRender(w, r, sem)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.PDFInternalPort)
	ln, err := listenWithReusePort(addr)
	if err != nil {
		slog.Error("runPDFWorker: listen", "addr", addr, "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
		WriteTimeout: time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
	}

	slog.Info("PDF worker listening", "addr", addr, "role", "internal")
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("runPDFWorker: serve", "err", err)
			os.Exit(1)
		}
	}()

	<-sigC
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("runPDFWorker: shutdown", "err", err)
	}
}

// handleInternalPDFRender is the /render/pdf endpoint served by PDF workers.
// It accepts a POST with a raw PDF body and render parameters as query params.
// Returns the rendered image (JPEG or PNG) bytes.
func handleInternalPDFRender(w http.ResponseWriter, r *http.Request, sem chan struct{}) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	width, _ := strconv.Atoi(q.Get("w"))
	height, _ := strconv.Atoi(q.Get("h"))
	outputFormat := q.Get("fmt")
	if outputFormat == "" {
		outputFormat = "jpeg"
	}
	quality := q.Get("quality")
	if quality == "" {
		quality = "medium"
	}
	shape := q.Get("shape")
	if shape == "" {
		shape = "rectangular"
	}

	data, err := readBody(r)
	if err != nil || len(data) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	start := time.Now()
	out, err := render.PDFRasterize(sem, data, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		slog.Error("handleInternalPDFRender: PDFRasterize", "err", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	slog.Debug("pdf worker: rendered page range", "w", width, "h", height, "fmt", outputFormat, "duration_ms", time.Since(start).Milliseconds())

	actualFormat := outputFormat
	if shape == "rounded" {
		actualFormat = "png"
	}

	w.Header().Set("Content-Type", contentTypeForFormat(actualFormat))
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		slog.Warn("handleInternalPDFRender: write", "err", werr)
	}
}

// -------------------------------------------------------------------------
// Main process role
// -------------------------------------------------------------------------

// runMain spawns PDF worker processes and serves the public HTTP port.
func (s *Server) runMain() {
	// Init libvips for in-process image/SVG rendering.
	if err := render.InitVips("carbonio-preview"); err != nil {
		slog.Error("runMain: InitVips", "err", err)
		os.Exit(1)
	}
	render.SetVipsConcurrency(s.cfg.VIPSConcurrency)

	sem := render.BuildSemaphore(s.cfg.ServiceWorkers)

	// Spawn PDF worker pool.
	pdfInternalAddr := fmt.Sprintf("http://127.0.0.1:%d", s.cfg.PDFInternalPort)
	workers, err := s.spawnPDFWorkers()
	if err != nil {
		slog.Error("runMain: spawn PDF workers", "err", err)
		os.Exit(1)
	}

	// Build relay client. High MaxIdleConnsPerHost so keep-alive connections
	// are reused across concurrent PDF requests.
	relayClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        s.cfg.PDFWorkers * 4,
			MaxIdleConnsPerHost: s.cfg.PDFWorkers * 4,
			IdleConnTimeout:     90 * time.Second,
		},
		Timeout: time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
	}

	// Wait briefly for workers to bind their port.
	time.Sleep(500 * time.Millisecond)

	// Build the public mux.
	mux := loggingMiddleware(s.buildMux(sem, relayClient, pdfInternalAddr))

	addr := fmt.Sprintf("%s:%s", s.cfg.ServiceIP, s.cfg.ServicePort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
		WriteTimeout: time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
	}

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

	slog.Info("carbonio-preview starting",
		"addr", addr,
		"role", "main",
		"workers", s.cfg.ServiceWorkers,
		"pdf_workers", s.cfg.PDFWorkers,
		"vips_concurrency", s.cfg.VIPSConcurrency,
		"docs_enabled", s.cfg.AreDocsEnabled,
		"log_level", s.cfg.LogLevel.String(),
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("runMain: serve", "err", err)
			os.Exit(1)
		}
	}()

	// Wait for termination signal.
	sig := <-sigC
	slog.Info("runMain: received signal, shutting down", "signal", sig.String())

	// Forward SIGTERM to child processes.
	for _, w := range workers {
		if w.Process != nil {
			if err := w.Process.Signal(syscall.SIGTERM); err != nil {
				slog.Warn("runMain: forward SIGTERM to worker", "pid", w.Process.Pid, "err", err)
			}
		}
	}

	// Graceful shutdown of the HTTP server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("runMain: shutdown", "err", err)
	}

	// Wait for all child processes to exit.
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(cmd *exec.Cmd) {
			defer wg.Done()
			if err := cmd.Wait(); err != nil {
				slog.Warn("runMain: worker exit", "err", err)
			}
		}(w)
	}
	wg.Wait()
}

// buildMux assembles the public HTTP mux with all route groups.
func (s *Server) buildMux(
	sem chan struct{},
	relayClient *http.Client,
	pdfInternalAddr string,
) *http.ServeMux {
	mux := http.NewServeMux()
	registerImageRoutes(mux, s.cfg, s.store, sem)
	registerPDFRoutes(mux, s.cfg, s.store, sem, relayClient, pdfInternalAddr)
	registerDocumentRoutes(mux, s.cfg, s.store, sem, relayClient, pdfInternalAddr)
	registerHealthRoutes(mux, s.cfg)
	registerDocRoutes(mux)
	return mux
}

// spawnPDFWorkers launches PDFWorkers copies of the current binary as PDF workers.
// Returns the running *exec.Cmd handles.
func (s *Server) spawnPDFWorkers() ([]*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot determine executable path: %w", err)
	}

	// Build child env: inherit everything, then override/add worker-specific vars.
	childEnv := buildChildEnv(s.cfg)

	slog.Info("spawning PDF workers", "count", s.cfg.PDFWorkers, "internal_port", s.cfg.PDFInternalPort)

	workers := make([]*exec.Cmd, 0, s.cfg.PDFWorkers)
	for i := 0; i < s.cfg.PDFWorkers; i++ {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Env = childEnv
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			// Kill already-started workers on failure.
			for _, prev := range workers {
				if prev.Process != nil {
					prev.Process.Kill() //nolint:errcheck
				}
			}
			return nil, fmt.Errorf("start PDF worker %d: %w", i, err)
		}
		slog.Debug("spawned PDF worker", "pid", cmd.Process.Pid, "index", i)
		workers = append(workers, cmd)
	}
	return workers, nil
}

// buildChildEnv creates the environment slice for a PDF worker child process.
// It starts from the current process environment and overrides/adds the
// required worker markers.
func buildChildEnv(cfg *config.Config) []string {
	// Strip keys we are about to set, then append the worker values.
	strip := map[string]bool{
		"ROLE":              true,
		"REUSEPORT":         true,
		"PDF_INTERNAL_PORT": true,
		"PDF_WORKERS":       true,
	}

	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		key := kv
		if idx := len(kv); idx > 0 {
			for i, c := range kv {
				if c == '=' {
					key = kv[:i]
					break
				}
			}
		}
		if !strip[key] {
			env = append(env, kv)
		}
	}

	env = append(env,
		"ROLE=pdfworker",
		"REUSEPORT=1",
		fmt.Sprintf("PDF_INTERNAL_PORT=%d", cfg.PDFInternalPort),
		"PDF_WORKERS=0",
	)
	return env
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

// -------------------------------------------------------------------------
// Network helpers
// -------------------------------------------------------------------------

// listenWithReusePort creates a TCP listener on addr with SO_REUSEPORT set.
// This allows multiple processes to bind the same port; the kernel round-robins
// incoming connections across them (the gunicorn model for PDF workers).
func listenWithReusePort(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var setSockoptErr error
			if err := c.Control(func(fd uintptr) {
				setSockoptErr = unix.SetsockoptInt(
					int(fd),
					unix.SOL_SOCKET,
					unix.SO_REUSEPORT,
					1,
				)
			}); err != nil {
				return err
			}
			return setSockoptErr
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

// -------------------------------------------------------------------------
// Misc helpers
// -------------------------------------------------------------------------

// readBody reads the full request body and returns the bytes.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	data := make([]byte, 0, 1<<20) // pre-alloc 1 MiB
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return data, nil
}
