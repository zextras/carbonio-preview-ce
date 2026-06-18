// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// handler_params_test.go validates that the gRPC handlers pass the real
// PreviewParams fields (quality, crop, first_page/last_page, lang_tag) through
// to the underlying render/convert functions, mirroring the REST handlers.
//
// All render functions are stubbed (no CGO); captured call arguments are
// inspected to assert correct parameter flow.
package grpc_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zextras/carbonio-preview-ce/config"
	grpcserver "github.com/zextras/carbonio-preview-ce/server/grpc"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
	"github.com/zextras/carbonio-preview-ce/storage"
)

// ---------------------------------------------------------------------------
// Captured-argument stubs
// ---------------------------------------------------------------------------

type capturedImageArgs struct {
	outputFormat string
	quality      string
	shape        string
	cropMode     string
}

type capturedPdfSliceArgs struct {
	firstPage int
	lastPage  int
}

type capturedCollaboraArgs struct {
	langTag string
}

// newCapturingServer builds a PreviewServer with stubs that capture render args.
// docEnabled controls ServiceEnableDocumentPreview/Thumbnail.
func newCapturingServer(t *testing.T, store storage.Client, docEnabled bool) (
	*grpcserver.PreviewServer,
	*capturedImageArgs,
	*capturedPdfSliceArgs,
	*capturedCollaboraArgs,
) {
	t.Helper()

	cfg := &config.Config{
		ServiceEnableDocumentPreview:         docEnabled,
		ServiceEnableDocumentThumbnail:       docEnabled,
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}

	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)

	imgCap := &capturedImageArgs{}
	pdfSliceCap := &capturedPdfSliceArgs{}
	collaboraCap := &capturedCollaboraArgs{}

	ps.SetImageThumbnailFunc(func(_ chan struct{}, data []byte, _, _ int, outputFormat, quality, shape, cropMode string) ([]byte, error) {
		imgCap.outputFormat = outputFormat
		imgCap.quality = quality
		imgCap.shape = shape
		imgCap.cropMode = cropMode
		return data, nil
	})
	ps.SetPdfSliceFunc(func(_ chan struct{}, data []byte, firstPage, lastPage int) ([]byte, error) {
		pdfSliceCap.firstPage = firstPage
		pdfSliceCap.lastPage = lastPage
		return data, nil
	})
	ps.SetPdfRasterizeFunc(func(_ chan struct{}, data []byte, _, _, _ int, _, _, _ string) ([]byte, error) {
		return data, nil
	})
	ps.SetCollaboraConvertFunc(func(_ context.Context, data []byte, langTag, _ string, _ time.Duration) ([]byte, error) {
		collaboraCap.langTag = langTag
		return data, nil
	})

	return ps, imgCap, pdfSliceCap, collaboraCap
}

// drainStream reads all frames from a server-streaming RPC, discarding content.
// Returns nil on clean EOF, the first non-EOF error otherwise.
func drainStream(stream interface {
	Recv() (*pb.PreviewChunk, error)
}) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ---------------------------------------------------------------------------
// quality: string pass-through
// ---------------------------------------------------------------------------

func TestGetImagePreview_QualityStringPassThrough(t *testing.T) {
	blob := []byte("imgdata")
	ps, imgCap, _, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		Quality:     "highest",
	}})
	if err != nil {
		t.Fatalf("GetImagePreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if imgCap.quality != "highest" {
		t.Errorf("quality: want %q, got %q", "highest", imgCap.quality)
	}
}

func TestGetImagePreview_QualityEmptyDefaultsMedium(t *testing.T) {
	blob := []byte("imgdata")
	ps, imgCap, _, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		// Quality omitted → proto3 zero value ""
	}})
	if err != nil {
		t.Fatalf("GetImagePreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if imgCap.quality != "medium" {
		t.Errorf("quality: want %q (default), got %q", "medium", imgCap.quality)
	}
}

func TestGetImagePreview_InvalidQualityReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid quality → gRPC must return FAILED_PRECONDITION.
	ps, _, _, _ := newCapturingServer(t, &fixedStore{blob: []byte("x")}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		Quality:     "not-a-valid-bucket",
	}})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition {
			t.Fatalf("want FAILED_PRECONDITION (REST 422), got %v", err)
		}
		return
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected error for invalid quality, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", recvErr, recvErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FAILED_PRECONDITION (REST 422), got %s", st.Code())
	}
}

// ---------------------------------------------------------------------------
// crop: bool → cropMode
// ---------------------------------------------------------------------------

func TestGetImagePreview_CropTrueIsCenter(t *testing.T) {
	blob := []byte("imgdata")
	ps, imgCap, _, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		Crop:        true,
	}})
	if err != nil {
		t.Fatalf("GetImagePreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if imgCap.cropMode != "center" {
		t.Errorf("cropMode: want %q, got %q", "center", imgCap.cropMode)
	}
}

func TestGetImagePreview_CropFalseIsNone(t *testing.T) {
	blob := []byte("imgdata")
	ps, imgCap, _, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		Crop:        false,
	}})
	if err != nil {
		t.Fatalf("GetImagePreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if imgCap.cropMode != "none" {
		t.Errorf("cropMode: want %q, got %q", "none", imgCap.cropMode)
	}
}

// Image thumbnails always use CENTER crop regardless of params.Crop.
func TestGetImageThumbnail_AlwaysCenterCrop(t *testing.T) {
	blob := []byte("imgdata")
	ps, imgCap, _, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	for _, crop := range []bool{true, false} {
		stream, err := client.GetImageThumbnail(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
			FileId:      "11111111-1111-1111-1111-111111111111",
			Version:     1,
			Area:        "100x100",
			ServiceType: "files",
			Crop:        crop,
		}})
		if err != nil {
			t.Fatalf("GetImageThumbnail(crop=%v): %v", crop, err)
		}
		if err := drainStream(stream); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if imgCap.cropMode != "center" {
			t.Errorf("GetImageThumbnail(crop=%v): cropMode want %q, got %q", crop, "center", imgCap.cropMode)
		}
	}
}

// ---------------------------------------------------------------------------
// first_page / last_page flow-through (PDF preview)
// ---------------------------------------------------------------------------

func TestGetPdfPreview_PageRangeFlowsThrough(t *testing.T) {
	blob := []byte("pdfdata")
	ps, _, pdfSliceCap, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetPdfPreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "22222222-2222-2222-2222-222222222222",
		Version:     1,
		ServiceType: "files",
		FirstPage:   3,
		LastPage:    7,
	}})
	if err != nil {
		t.Fatalf("GetPdfPreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if pdfSliceCap.firstPage != 3 {
		t.Errorf("firstPage: want 3, got %d", pdfSliceCap.firstPage)
	}
	if pdfSliceCap.lastPage != 7 {
		t.Errorf("lastPage: want 7, got %d", pdfSliceCap.lastPage)
	}
}

func TestGetPdfPreview_ZeroPageDefaultsToOneAndEnd(t *testing.T) {
	blob := []byte("pdfdata")
	ps, _, pdfSliceCap, _ := newCapturingServer(t, &fixedStore{blob: blob}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetPdfPreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "22222222-2222-2222-2222-222222222222",
		Version:     1,
		ServiceType: "files",
		// FirstPage and LastPage omitted (proto3 zero = 0)
	}})
	if err != nil {
		t.Fatalf("GetPdfPreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if pdfSliceCap.firstPage != 1 {
		t.Errorf("firstPage: want 1 (default), got %d", pdfSliceCap.firstPage)
	}
	if pdfSliceCap.lastPage != 0 {
		t.Errorf("lastPage: want 0 (to end), got %d", pdfSliceCap.lastPage)
	}
}

// ---------------------------------------------------------------------------
// lang_tag flow-through (document conversion)
// ---------------------------------------------------------------------------

func TestGetDocumentPreview_LangTagFlowsThrough(t *testing.T) {
	blob := []byte("docdata")
	ps, _, _, collaboraCap := newCapturingServer(t, &fixedStore{blob: blob}, true)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetDocumentPreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "33333333-3333-3333-3333-333333333333",
		Version:     1,
		ServiceType: "files",
		LangTag:     "it-IT",
	}})
	if err != nil {
		t.Fatalf("GetDocumentPreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if collaboraCap.langTag != "it-IT" {
		t.Errorf("langTag: want %q, got %q", "it-IT", collaboraCap.langTag)
	}
}

func TestGetDocumentPreview_EmptyLangTagDefaultsEnUS(t *testing.T) {
	blob := []byte("docdata")
	ps, _, _, collaboraCap := newCapturingServer(t, &fixedStore{blob: blob}, true)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetDocumentPreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "33333333-3333-3333-3333-333333333333",
		Version:     1,
		ServiceType: "files",
		// LangTag omitted → should default to "en-US"
	}})
	if err != nil {
		t.Fatalf("GetDocumentPreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if collaboraCap.langTag != "en-US" {
		t.Errorf("langTag: want %q (default), got %q", "en-US", collaboraCap.langTag)
	}
}

// ---------------------------------------------------------------------------
// Fix 2: document config-gate disabled → 400 INVALID_ARGUMENT (not 422)
// ---------------------------------------------------------------------------

func TestGetDocumentPreview_DisabledReturnsInvalidArgument(t *testing.T) {
	// REST returns HTTP 400 when document preview is disabled (errBadRequest).
	// gRPC must return INVALID_ARGUMENT (400), NOT FAILED_PRECONDITION (422).
	ps, _, _, _ := newCapturingServer(t, &fixedStore{blob: []byte("x")}, false /* docEnabled=false */)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetDocumentPreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "44444444-4444-4444-4444-444444444444",
		Version:     1,
		ServiceType: "files",
	}})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Fatalf("want INVALID_ARGUMENT (REST 400), got %v", err)
		}
		return
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected error for disabled doc preview, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", recvErr, recvErr)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("want INVALID_ARGUMENT (REST 400), got %s", st.Code())
	}
}

func TestGetDocumentThumbnail_DisabledReturnsInvalidArgument(t *testing.T) {
	// REST returns HTTP 400 when document thumbnail is disabled (errBadRequest).
	ps, _, _, _ := newCapturingServer(t, &fixedStore{blob: []byte("x")}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetDocumentThumbnail(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "44444444-4444-4444-4444-444444444444",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
	}})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Fatalf("want INVALID_ARGUMENT (REST 400), got %v", err)
		}
		return
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected error for disabled doc thumbnail, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", recvErr, recvErr)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("want INVALID_ARGUMENT (REST 400), got %s", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Fix 3: empty upload → 422 FAILED_PRECONDITION (FileNotValid)
// ---------------------------------------------------------------------------

func TestPostImagePreview_EmptyUploadReturnsFailedPrecondition(t *testing.T) {
	// REST rejects zero-byte uploads with HTTP 422 (FileNotValid).
	// gRPC must return FAILED_PRECONDITION.
	ps, _, _, _ := newCapturingServer(t, &fixedStore{blob: []byte("x")}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	uploadStream, err := client.PostImagePreview(context.Background())
	if err != nil {
		t.Fatalf("PostImagePreview open: %v", err)
	}

	// Send metadata frame only — no data frames → empty blob.
	if err := uploadStream.Send(&pb.UploadChunk{Payload: &pb.UploadChunk_Metadata{
		Metadata: &pb.UploadMetadata{Params: &pb.PreviewParams{
			Area:        "100x100",
			ServiceType: "files",
		}},
	}}); err != nil {
		t.Fatalf("Send metadata: %v", err)
	}

	// Close the send side to signal end-of-stream; then read the response.
	if err := uploadStream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	_, recvErr := uploadStream.Recv()
	if recvErr == nil {
		t.Fatal("expected FAILED_PRECONDITION for empty upload, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", recvErr, recvErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FAILED_PRECONDITION (REST 422 FileNotValid), got %s", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Fix 4: invalid output_format → 422 FAILED_PRECONDITION (not silent coerce)
// ---------------------------------------------------------------------------

func TestGetImagePreview_InvalidOutputFormatReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid output_format.
	// Old gRPC behaviour silently coerced to "jpeg" — that is wrong.
	ps, _, _, _ := newCapturingServer(t, &fixedStore{blob: []byte("x")}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:       "11111111-1111-1111-1111-111111111111",
		Version:      1,
		Area:         "100x100",
		ServiceType:  "files",
		OutputFormat: "bmp", // invalid non-empty value
	}})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition {
			t.Fatalf("want FAILED_PRECONDITION (REST 422), got %v", err)
		}
		return
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected FAILED_PRECONDITION for invalid output_format, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", recvErr, recvErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FAILED_PRECONDITION (REST 422), got %s", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Fix 5: invalid shape → 422 FAILED_PRECONDITION (not silent coerce)
// ---------------------------------------------------------------------------

func TestGetImageThumbnail_InvalidShapeReturnsFailedPrecondition(t *testing.T) {
	// REST returns HTTP 422 for invalid shape.
	// Old gRPC behaviour silently coerced to "rectangular" — that is wrong.
	ps, _, _, _ := newCapturingServer(t, &fixedStore{blob: []byte("x")}, false)
	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImageThumbnail(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		Shape:       "circle", // invalid non-empty value
	}})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition {
			t.Fatalf("want FAILED_PRECONDITION (REST 422), got %v", err)
		}
		return
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected FAILED_PRECONDITION for invalid shape, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", recvErr, recvErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FAILED_PRECONDITION (REST 422), got %s", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Fix 6: owner_id reaches storage
// ---------------------------------------------------------------------------

// capturingStore records the ownerID passed to RetrieveData.
type capturingStore struct {
	blob    []byte
	ownerID string
}

func (c *capturingStore) RetrieveData(_ context.Context, _ string, _ int, _ string, ownerID string) (storage.Blob, error) {
	c.ownerID = ownerID
	return c.blob, nil
}

func (c *capturingStore) RetrieveDataStreaming(_ context.Context, _ string, _ int, _, ownerID string) (io.ReadCloser, error) {
	c.ownerID = ownerID
	return io.NopCloser(bytes.NewReader(c.blob)), nil
}

func TestGetImagePreview_OwnerIDReachesStorage(t *testing.T) {
	// REST passes ownerID(r) (the "fileownerid" header) to RetrieveData.
	// gRPC was hardcoding "" — it must now pass p.GetOwnerId().
	store := &capturingStore{blob: []byte("imgdata")}

	cfg := &config.Config{
		ServiceEnableDocumentPreview:         false,
		ServiceEnableDocumentThumbnail:       false,
		ServiceDocsTimeout:                   15,
		DocumentConversionFullConvertAddress: "http://127.0.0.1:20001/cool/convert-to",
	}
	sem := make(chan struct{}, 4)
	ps := grpcserver.NewPreviewServer(store, cfg, sem)
	ps.SetImageThumbnailFunc(func(_ chan struct{}, data []byte, _, _ int, _, _, _, _ string) ([]byte, error) {
		return data, nil
	})

	conn, cleanup := connectTestServer(t, ps)
	defer cleanup()

	const wantOwner = "owner-uuid-goes-here"
	client := pb.NewPreviewServiceClient(conn)
	stream, err := client.GetImagePreview(context.Background(), &pb.GetRequest{Params: &pb.PreviewParams{
		FileId:      "11111111-1111-1111-1111-111111111111",
		Version:     1,
		Area:        "100x100",
		ServiceType: "files",
		OwnerId:     wantOwner,
	}})
	if err != nil {
		t.Fatalf("GetImagePreview: %v", err)
	}
	if err := drainStream(stream); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if store.ownerID != wantOwner {
		t.Errorf("ownerID: want %q, got %q", wantOwner, store.ownerID)
	}
}
