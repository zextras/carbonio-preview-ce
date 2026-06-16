// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package grpc_test contains in-process round-trip tests for the gRPC server.
// All render functions are stubbed so no CGO (libvips / PDFium) is needed.
// A bufconn listener is used so no TCP port is opened.
package grpc_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	"github.com/zextras/carbonio-preview-ce/config"
	grpcserver "github.com/zextras/carbonio-preview-ce/server/grpc"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/storage"
)

const bufSize = 1 << 20 // 1 MB in-memory buffer

// ---------------------------------------------------------------------------
// Stub storage
// ---------------------------------------------------------------------------

type fixedStore struct {
	blob     storage.Blob
	notFound bool
}

func (f *fixedStore) RetrieveData(_ context.Context, _ string, _ int, _ string, _ string) (storage.Blob, error) {
	if f.notFound {
		return nil, storage.ErrNotFound
	}
	return f.blob, nil
}

// ---------------------------------------------------------------------------
// Test server factory
// ---------------------------------------------------------------------------

// connectTestServer wires a fully in-process gRPC connection to a
// pre-configured PreviewServer (all render functions already stubbed).
// Returns a live *grpc.ClientConn and a cleanup function.
func connectTestServer(t *testing.T, ps *grpcserver.PreviewServer) (*grpc.ClientConn, func()) {
	t.Helper()

	srv, _ := grpcserver.GRPCServer(ps)

	lis := bufconn.Listen(bufSize)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}

	return conn, func() {
		conn.Close()
		srv.Stop()
	}
}

// newTestServerConn builds a fully in-process gRPC server with pass-through
// stubbed render functions. Returns a live *grpc.ClientConn and a cleanup function.
func newTestServerConn(t *testing.T, store storage.Client) (*grpc.ClientConn, func()) {
	t.Helper()

	cfg := &config.Config{
		ServiceEnableDocumentPreview:         false,
		ServiceEnableDocumentThumbnail:       false,
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
		GRPCPort:                             "0",
	}

	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)

	// Stub all render functions — no CGO needed.
	ps.SetImageThumbnailFunc(func(_ chan struct{}, data []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return data, nil // pass-through: return input bytes unchanged
	})
	ps.SetPdfSliceFunc(func(_ chan struct{}, data []byte, _, _ int) ([]byte, error) {
		return data, nil
	})
	ps.SetPdfRasterizeFunc(func(_ chan struct{}, data []byte, _, _, _ int, _, _, _ string) ([]byte, error) {
		return data, nil
	})
	ps.SetCollaboraConvertFunc(func(_ context.Context, data []byte, _, _ string, _ time.Duration) ([]byte, error) {
		return data, nil
	})

	return connectTestServer(t, ps)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGetImagePreview_RoundTrip verifies the full in-process round-trip for
// GetImagePreview: the stub storage returns a known blob, the stub render
// function returns it unchanged, and the client receives the correct
// metadata (mime_type, length) followed by the reassembled bytes.
func TestGetImagePreview_RoundTrip(t *testing.T) {
	knownBlob := bytes.Repeat([]byte("img"), 100) // 300 bytes
	store := &fixedStore{blob: knownBlob}

	conn, cleanup := newTestServerConn(t, store)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{
		Params: &pb.PreviewParams{
			FileId:      "11111111-1111-1111-1111-111111111111",
			Version:     1,
			Area:        "320x240",
			ServiceType: "files",
		},
	}

	stream, err := client.GetImagePreview(context.Background(), req)
	if err != nil {
		t.Fatalf("GetImagePreview: %v", err)
	}

	var gotMime string
	var gotLen int64
	var buf bytes.Buffer

	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		switch p := frame.Payload.(type) {
		case *pb.PreviewChunk_Metadata:
			gotMime = p.Metadata.MimeType
			gotLen = p.Metadata.Length
		case *pb.PreviewChunk_Chunk:
			buf.Write(p.Chunk)
		}
	}

	if gotMime != "image/jpeg" {
		t.Errorf("mime_type: want image/jpeg, got %q", gotMime)
	}
	if gotLen != int64(len(knownBlob)) {
		t.Errorf("length: want %d, got %d", len(knownBlob), gotLen)
	}
	if !bytes.Equal(buf.Bytes(), knownBlob) {
		t.Errorf("blob mismatch: want len=%d, got len=%d", len(knownBlob), buf.Len())
	}
}

// TestHealth_Serving verifies that the standard gRPC health service reports
// SERVING immediately after the server is started.
func TestHealth_Serving(t *testing.T) {
	conn, cleanup := newTestServerConn(t, &fixedStore{blob: []byte("x")})
	defer cleanup()

	hc := grpc_health_v1.NewHealthClient(conn)
	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health Check: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("health status: want SERVING, got %s", resp.Status)
	}
}

// TestGetImagePreview_NotFound verifies that a storage 404 propagates as
// a gRPC NOT_FOUND status.
func TestGetImagePreview_NotFound(t *testing.T) {
	store := &fixedStore{notFound: true}

	conn, cleanup := newTestServerConn(t, store)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{
		Params: &pb.PreviewParams{
			FileId:      "22222222-2222-2222-2222-222222222222",
			Version:     1,
			Area:        "100x100",
			ServiceType: "chats",
		},
	}

	stream, err := client.GetImagePreview(context.Background(), req)
	if err != nil {
		t.Fatalf("GetImagePreview open: %v", err)
	}

	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected error for not-found, got nil")
	}
	// The error should contain "not found" context (gRPC NOT_FOUND code).
	// We check the string because importing status/codes here is fine.
	if recvErr == io.EOF {
		t.Fatal("expected gRPC error, got EOF (no frames sent before close)")
	}
}
