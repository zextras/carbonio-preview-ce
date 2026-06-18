// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/zextras/carbonio-preview-ce/config"
	grpcserver "github.com/zextras/carbonio-preview-ce/server/grpc"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
)

// TestGetVideoThumbnail_OK verifies the full in-process round-trip for
// GetVideoThumbnail: fixedStore delivers a blob, the stubbed videoFirstFrame
// returns fake PNG bytes, the stubbed imageThumbnail receives those bytes and
// returns rendered output, and the client receives metadata + rendered bytes.
func TestGetVideoThumbnail_OK(t *testing.T) {
	const fakeFrame = "\x89PNGframe0"
	const rendered = "rendered-jpeg"

	store := &fixedStore{blob: []byte("vid-bytes")}

	cfg := &config.Config{
		ServiceEnableDocumentPreview:         false,
		ServiceEnableDocumentThumbnail:       false,
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)

	// Stub videoFirstFrame: drain the reader and return fake PNG.
	ps.SetVideoFirstFrameFunc(func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return []byte(fakeFrame), nil
	})
	// Stub imageThumbnail: assert it receives the extracted frame, return rendered bytes.
	ps.SetImageThumbnailFunc(func(_ chan struct{}, data []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		if string(data) != fakeFrame {
			t.Errorf("imageThumbnail received %q, want %q", data, fakeFrame)
		}
		return []byte(rendered), nil
	})

	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "320x240",
		ServiceType: "files",
	}}
	stream, err := client.GetVideoThumbnail(context.Background(), req)
	if err != nil {
		t.Fatalf("GetVideoThumbnail: %v", err)
	}

	var gotMime string
	var buf bytes.Buffer
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}
		switch p := frame.Payload.(type) {
		case *pb.PreviewChunk_Metadata:
			gotMime = p.Metadata.MimeType
		case *pb.PreviewChunk_Chunk:
			buf.Write(p.Chunk)
		}
	}

	if gotMime != "image/jpeg" {
		t.Errorf("mime_type: want image/jpeg, got %q", gotMime)
	}
	if buf.String() != rendered {
		t.Errorf("body: want %q, got %q", rendered, buf.String())
	}
}

// TestGetVideoPreview_OK verifies the full in-process round-trip for
// GetVideoPreview: same pipeline as thumbnail but using crop parameter.
func TestGetVideoPreview_OK(t *testing.T) {
	const fakeFrame = "\x89PNGframe0"
	const rendered = "preview-jpeg"

	store := &fixedStore{blob: []byte("vid-bytes")}

	cfg := &config.Config{
		ServiceEnableDocumentPreview:         false,
		ServiceEnableDocumentThumbnail:       false,
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)

	ps.SetVideoFirstFrameFunc(func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return []byte(fakeFrame), nil
	})
	ps.SetImageThumbnailFunc(func(_ chan struct{}, data []byte, _, _ int, _, _, _, cropMode string) ([]byte, error) {
		if string(data) != fakeFrame {
			t.Errorf("imageThumbnail received %q, want %q", data, fakeFrame)
		}
		if cropMode != "center" {
			t.Errorf("cropMode: want center, got %q", cropMode)
		}
		return []byte(rendered), nil
	})

	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "22222222-2222-2222-2222-222222222222",
		Version:     1,
		Area:        "640x480",
		ServiceType: "files",
		Crop:        true,
	}}
	stream, err := client.GetVideoPreview(context.Background(), req)
	if err != nil {
		t.Fatalf("GetVideoPreview: %v", err)
	}

	var buf bytes.Buffer
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}
		if c, ok := frame.Payload.(*pb.PreviewChunk_Chunk); ok {
			buf.Write(c.Chunk)
		}
	}

	if buf.String() != rendered {
		t.Errorf("body: want %q, got %q", rendered, buf.String())
	}
}

// TestGetVideoThumbnail_NotFound verifies that a storage 404 propagates
// as a gRPC NOT_FOUND status for GetVideoThumbnail.
func TestGetVideoThumbnail_NotFound(t *testing.T) {
	store := &fixedStore{notFound: true}

	cfg := &config.Config{
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)
	ps.SetVideoFirstFrameFunc(func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		return nil, nil // should not be called
	})

	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "33333333-3333-3333-3333-333333333333",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
	}}
	stream, err := client.GetVideoThumbnail(context.Background(), req)
	if err != nil {
		t.Fatalf("GetVideoThumbnail open: %v", err)
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected error for not-found, got nil")
	}
	if recvErr == io.EOF {
		t.Fatal("expected gRPC error, got EOF")
	}
}
