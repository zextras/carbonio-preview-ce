// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/zextras/carbonio-preview-ce/config"
	grpcserver "github.com/zextras/carbonio-preview-ce/server/grpc"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// TestCmux_SinglePort_GRPCAndHTTP proves that ONE listener multiplexed via cmux
// can serve both a gRPC client (health Check) and an HTTP GET /health/live/ — on
// the SAME port. Uses a real net.Listen on 127.0.0.1:0 (ephemeral port) so the
// actual cmux matching logic is exercised (not bufconn).
func TestCmux_SinglePort_GRPCAndHTTP(t *testing.T) {
	// --- wire up grpc server ---
	cfg := &config.Config{
		ServiceEnableDocumentPreview:         false,
		ServiceEnableDocumentThumbnail:       false,
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(&fixedStoreForCmux{}, cfg, sem)
	// stub all render funcs so no CGO needed
	ps.SetImageThumbnailFunc(func(_ chan struct{}, data []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return data, nil
	})
	ps.SetPdfSliceFunc(func(_ chan struct{}, data []byte, _, _ int) ([]byte, error) { return data, nil })
	ps.SetPdfRasterizeFunc(func(_ chan struct{}, data []byte, _, _, _ int, _, _, _ string) ([]byte, error) {
		return data, nil
	})
	ps.SetCollaboraConvertFunc(func(_ context.Context, data []byte, _, _ string, _ time.Duration) ([]byte, error) {
		return data, nil
	})

	grpcSrv, healthSvc := grpcserver.GRPCServer(ps)

	// --- bind ONE listener on an ephemeral port ---
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := lis.Addr().String()

	// --- cmux setup ---
	m := cmux.New(lis)
	grpcL := m.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))
	httpL := m.Match(cmux.HTTP1Fast(), cmux.Any())

	// serve grpc on grpcL
	go func() {
		healthSvc.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
		_ = grpcSrv.Serve(grpcL)
	}()

	// serve HTTP health on httpL
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{Handler: mux}
	go func() { _ = httpSrv.Serve(httpL) }()

	go func() { _ = m.Serve() }()

	// give the server a moment to start
	time.Sleep(50 * time.Millisecond)

	// --- assert gRPC health works ---
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	hc := grpc_health_v1.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := hc.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("gRPC health Check on same port: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("gRPC health status: want SERVING, got %s", resp.Status)
	}

	// --- assert HTTP /health/live/ works on the SAME port ---
	httpResp, err := http.Get("http://" + addr + "/health/live/")
	if err != nil {
		t.Fatalf("HTTP GET /health/live/ on same port: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("HTTP health status: want 200, got %d", httpResp.StatusCode)
	}

	// --- teardown ---
	grpcSrv.Stop()
	_ = httpSrv.Close()
}

// fixedStoreForCmux is a minimal storage.Client stub for the cmux test.
type fixedStoreForCmux struct{}

func (f *fixedStoreForCmux) RetrieveData(_ context.Context, _ string, _ int, _ string, _ string) (storage.Blob, error) {
	return []byte("data"), nil
}

func (f *fixedStoreForCmux) RetrieveDataStreaming(_ context.Context, _ string, _ int, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte("data"))), nil
}
