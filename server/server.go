package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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
		log.Fatalf("runPDFWorker: InitVips: %v", err)
	}
	render.SetVipsConcurrency(s.cfg.VIPSConcurrency)

	// Init PDFium.
	render.PDFWorkerInit()
	defer render.PDFWorkerClose()

	sem := render.BuildSemaphore(runtime.NumCPU())

	mux := http.NewServeMux()
	mux.HandleFunc(internalPDFRenderPath, func(w http.ResponseWriter, r *http.Request) {
		handleInternalPDFRender(w, r, sem)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.PDFInternalPort)
	ln, err := listenWithReusePort(addr)
	if err != nil {
		log.Fatalf("runPDFWorker: listen on %s: %v", addr, err)
	}

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
		WriteTimeout: time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
	}

	log.Printf("PDF worker listening on %s (internal)", addr)
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("runPDFWorker: serve: %v", err)
		}
	}()

	<-sigC
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("runPDFWorker: shutdown: %v", err)
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

	out, err := render.PDFRasterize(sem, data, 0, width, height, outputFormat, quality, shape)
	if err != nil {
		log.Printf("handleInternalPDFRender: PDFRasterize: %v", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	actualFormat := outputFormat
	if shape == "rounded" {
		actualFormat = "png"
	}

	w.Header().Set("Content-Type", contentTypeForFormat(actualFormat))
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		log.Printf("handleInternalPDFRender: write: %v", werr)
	}
}

// -------------------------------------------------------------------------
// Main process role
// -------------------------------------------------------------------------

// runMain spawns PDF worker processes and serves the public HTTP port.
func (s *Server) runMain() {
	// Init libvips for in-process image/SVG rendering.
	if err := render.InitVips("carbonio-preview"); err != nil {
		log.Fatalf("runMain: InitVips: %v", err)
	}
	render.SetVipsConcurrency(s.cfg.VIPSConcurrency)

	sem := render.BuildSemaphore(runtime.NumCPU())

	// Spawn PDF worker pool.
	pdfInternalAddr := fmt.Sprintf("http://127.0.0.1:%d", s.cfg.PDFInternalPort)
	workers, err := s.spawnPDFWorkers()
	if err != nil {
		log.Fatalf("runMain: spawn PDF workers: %v", err)
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
	mux := s.buildMux(sem, relayClient, pdfInternalAddr)

	addr := fmt.Sprintf("%s:%s", s.cfg.ServiceIP, s.cfg.ServicePort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
		WriteTimeout: time.Duration(s.cfg.ServiceTimeoutInSeconds) * time.Second,
	}

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("carbonio-preview listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("runMain: serve: %v", err)
		}
	}()

	// Wait for termination signal.
	sig := <-sigC
	log.Printf("runMain: received signal %v, shutting down", sig)

	// Forward SIGTERM to child processes.
	for _, w := range workers {
		if w.Process != nil {
			if err := w.Process.Signal(syscall.SIGTERM); err != nil {
				log.Printf("runMain: forward SIGTERM to worker PID %d: %v", w.Process.Pid, err)
			}
		}
	}

	// Graceful shutdown of the HTTP server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("runMain: shutdown: %v", err)
	}

	// Wait for all child processes to exit.
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(cmd *exec.Cmd) {
			defer wg.Done()
			if err := cmd.Wait(); err != nil {
				log.Printf("runMain: worker exit: %v", err)
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
		log.Printf("spawned PDF worker PID %d", cmd.Process.Pid)
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

// formatInt formats an integer to its decimal string representation.
func formatInt(n int) string {
	return strconv.Itoa(n)
}
