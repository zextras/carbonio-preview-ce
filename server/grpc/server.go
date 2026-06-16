// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package grpc provides the gRPC transport layer for carbonio-preview-ce.
// It implements PreviewService (defined in proto/preview.proto) and wires a
// grpc.Server alongside the existing HTTP server — both share the same
// render/storage pipeline.
//
// # Design
//
// PreviewServer holds injectable function vars for every render operation
// (imageThumbnail, pdfSlice, pdfRasterize, collaboraConvert) mirroring the
// pattern used by the REST handlers in package server. Production code sets
// them to the real render.* functions; tests swap them for stubs without
// linking against CGO.
//
// # Port
//
// The gRPC port is driven by config key "grpc-port" (application layer,
// Consul KV carbonio-preview/grpc-port). The default is 10001, distinct from
// the REST port (default 10000). The final deployed port is confirmed at
// package/deploy time and overridden via Consul KV or env
// APPLICATION_CONFIG_GRPC_PORT.
package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/render"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// PreviewServer implements pb.PreviewServiceServer.
// All render function fields are injectable for tests.
type PreviewServer struct {
	pb.UnimplementedPreviewServiceServer

	store   storage.Client
	cfg     *config.Config
	sem     chan struct{}

	// injectable render functions (mirrors render_hooks.go in package server).
	imageThumbnail    func(sem chan struct{}, data []byte, width, height int, outputFormat, quality, shape, cropMode string) ([]byte, error)
	pdfSlice          func(sem chan struct{}, data []byte, firstPage, lastPage int) ([]byte, error)
	pdfRasterize      func(sem chan struct{}, data []byte, page, width, height int, outputFormat, quality, shape string) ([]byte, error)
	collaboraConvert  func(ctx context.Context, data []byte, langTag, docsEditorURL string, timeout time.Duration) ([]byte, error)
}

// NewPreviewServer constructs a PreviewServer wired to the production render
// functions. store and cfg must not be nil.
func NewPreviewServer(store storage.Client, cfg *config.Config, sem chan struct{}) *PreviewServer {
	return &PreviewServer{
		store:  store,
		cfg:    cfg,
		sem:    sem,
		imageThumbnail:   render.ImageThumbnail,
		pdfSlice:         render.PDFSlice,
		pdfRasterize:     render.PDFRasterize,
		collaboraConvert: render.CollaboraConvert,
	}
}

// GRPCServer builds and returns a configured *grpc.Server with PreviewService
// and the standard gRPC health service registered.
//
// The health service is set to SERVING immediately; callers should call
// healthSvc.SetServingStatus before starting to accept connections if they
// want finer-grained control (e.g. NOT_SERVING during init).
func GRPCServer(ps *PreviewServer) (*grpc.Server, *health.Server) {
	srv := grpc.NewServer()
	pb.RegisterPreviewServiceServer(srv, ps)

	healthSvc := health.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, healthSvc)
	// Set the overall server status to SERVING.
	healthSvc.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	return srv, healthSvc
}

// SetImageThumbnailFunc replaces the imageThumbnail render function.
// For testing only — allows tests to stub out CGO-dependent render calls.
func (s *PreviewServer) SetImageThumbnailFunc(fn func(sem chan struct{}, data []byte, width, height int, outputFormat, quality, shape, cropMode string) ([]byte, error)) {
	s.imageThumbnail = fn
}

// SetPdfSliceFunc replaces the pdfSlice render function. For testing only.
func (s *PreviewServer) SetPdfSliceFunc(fn func(sem chan struct{}, data []byte, firstPage, lastPage int) ([]byte, error)) {
	s.pdfSlice = fn
}

// SetPdfRasterizeFunc replaces the pdfRasterize render function. For testing only.
func (s *PreviewServer) SetPdfRasterizeFunc(fn func(sem chan struct{}, data []byte, page, width, height int, outputFormat, quality, shape string) ([]byte, error)) {
	s.pdfRasterize = fn
}

// ListenAndServe starts the gRPC server on the address derived from cfg.
// It blocks until the server is stopped. Intended to be run in a goroutine
// alongside the HTTP server.
func ListenAndServe(srv *grpc.Server, cfg *config.Config) error {
	addr := fmt.Sprintf("%s:%s", cfg.ServiceIP, cfg.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc: listen %s: %w", addr, err)
	}
	slog.Info("gRPC server listening", "addr", addr)
	return srv.Serve(lis)
}
