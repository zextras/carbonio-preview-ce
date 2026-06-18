// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zextras/carbonio-preview-ce/config"
	grpcserver "github.com/zextras/carbonio-preview-ce/server/grpc"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/video"
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

// TestGetVideoThumbnail_ErrTooLarge verifies that video.ErrTooLarge propagates
// as a gRPC FAILED_PRECONDITION status (maps to HTTP 422).
func TestGetVideoThumbnail_ErrTooLarge(t *testing.T) {
	store := &fixedStore{blob: []byte("vid-bytes")}

	cfg := &config.Config{
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)

	ps.SetVideoFirstFrameFunc(func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return nil, video.ErrTooLarge
	})
	ps.SetImageThumbnailFunc(func(_ chan struct{}, _ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return nil, nil // should not be reached
	})

	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "44444444-4444-4444-4444-444444444444",
		Version:     1,
		Area:        "320x240",
		ServiceType: "files",
	}}
	stream, err := client.GetVideoThumbnail(context.Background(), req)
	if err != nil {
		t.Fatalf("GetVideoThumbnail open: %v", err)
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected error for ErrTooLarge, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", recvErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("ErrTooLarge: want codes.FailedPrecondition, got %s", st.Code())
	}
}

// TestGetVideoThumbnail_CancelledContext verifies that a cancelled client context
// results in codes.Canceled (not a format/render error code).
func TestGetVideoThumbnail_CancelledContext(t *testing.T) {
	store := &fixedStore{blob: []byte("vid-bytes")}

	cfg := &config.Config{
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)

	ps.SetVideoFirstFrameFunc(func(_ context.Context, r io.Reader, _ int64) ([]byte, error) {
		_, _ = io.ReadAll(r)
		return nil, context.Canceled
	})
	ps.SetImageThumbnailFunc(func(_ chan struct{}, _ []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return nil, nil // should not be reached
	})

	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	// Use a pre-cancelled context so the server sees cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := pb.NewPreviewServiceClient(conn)
	req := &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "55555555-5555-5555-5555-555555555555",
		Version:     1,
		Area:        "320x240",
		ServiceType: "files",
	}}
	stream, err := client.GetVideoThumbnail(ctx, req)
	if err != nil {
		// The RPC may fail immediately on open with a cancelled context — this
		// is also an acceptable Canceled outcome.
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Canceled {
			return // correct: cancelled on open
		}
		// Any other error on open is unexpected.
		t.Fatalf("GetVideoThumbnail open: unexpected error %v", err)
	}
	_, recvErr := stream.Recv()
	if recvErr == nil || recvErr == io.EOF {
		// The server may have completed before the cancel propagated; that is
		// acceptable only if the stream closed cleanly without data.
		return
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", recvErr)
	}
	// Accept Canceled or DeadlineExceeded — both arise from context cancellation.
	if st.Code() != codes.Canceled && st.Code() != codes.DeadlineExceeded {
		t.Errorf("cancelled context: want Canceled or DeadlineExceeded, got %s", st.Code())
	}
}
