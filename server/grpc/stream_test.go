// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"bytes"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
)

// ---------------------------------------------------------------------------
// Fake server-streaming send buffer (for streamBlob tests)
// ---------------------------------------------------------------------------

type fakeDownloadStream struct {
	sent []*pb.PreviewChunk
}

func (f *fakeDownloadStream) Send(c *pb.PreviewChunk) error {
	f.sent = append(f.sent, c)
	return nil
}

// ---------------------------------------------------------------------------
// streamBlob tests
// ---------------------------------------------------------------------------

func TestStreamBlob_MetadataFirst(t *testing.T) {
	blob := []byte("hello world")
	mime := "image/jpeg"

	s := &fakeDownloadStream{}
	if err := streamBlob(s, mime, blob); err != nil {
		t.Fatalf("streamBlob error: %v", err)
	}

	if len(s.sent) == 0 {
		t.Fatal("expected at least 1 frame")
	}

	first := s.sent[0]
	meta, ok := first.Payload.(*pb.PreviewChunk_Metadata)
	if !ok {
		t.Fatalf("frame 0 is not PreviewMetadata, got %T", first.Payload)
	}
	if meta.Metadata.MimeType != mime {
		t.Errorf("mime_type: want %q, got %q", mime, meta.Metadata.MimeType)
	}
	if meta.Metadata.Length != int64(len(blob)) {
		t.Errorf("length: want %d, got %d", len(blob), meta.Metadata.Length)
	}
}

func TestStreamBlob_ChunksReassemble(t *testing.T) {
	// Build a blob larger than one chunk to force multiple chunk frames.
	blob := bytes.Repeat([]byte("x"), chunkSize*2+100)
	mime := "image/png"

	s := &fakeDownloadStream{}
	if err := streamBlob(s, mime, blob); err != nil {
		t.Fatalf("streamBlob error: %v", err)
	}

	// Collect chunk frames (skip metadata frame 0).
	var buf bytes.Buffer
	for _, frame := range s.sent[1:] {
		chunk, ok := frame.Payload.(*pb.PreviewChunk_Chunk)
		if !ok {
			t.Fatalf("expected chunk frame, got %T", frame.Payload)
		}
		buf.Write(chunk.Chunk)
	}

	if !bytes.Equal(buf.Bytes(), blob) {
		t.Errorf("reassembled blob mismatch: want len=%d got len=%d", len(blob), buf.Len())
	}
}

func TestStreamBlob_SmallBlobSingleChunk(t *testing.T) {
	blob := []byte("tiny")
	s := &fakeDownloadStream{}
	if err := streamBlob(s, "image/gif", blob); err != nil {
		t.Fatalf("streamBlob error: %v", err)
	}
	// frame 0 = metadata, frame 1 = single chunk
	if len(s.sent) != 2 {
		t.Errorf("want 2 frames (meta + 1 chunk), got %d", len(s.sent))
	}
}

func TestStreamBlob_EmptyBlob(t *testing.T) {
	// An empty blob should still send the metadata frame plus zero chunk frames.
	s := &fakeDownloadStream{}
	if err := streamBlob(s, "application/pdf", []byte{}); err != nil {
		t.Fatalf("streamBlob error: %v", err)
	}
	if len(s.sent) != 1 {
		t.Errorf("want 1 frame (meta only), got %d", len(s.sent))
	}
}

// ---------------------------------------------------------------------------
// Fake client-streaming recv buffer (for recvUpload tests)
// ---------------------------------------------------------------------------

// fakeUploadStream simulates a client that sends a sequence of UploadChunk frames.
type fakeUploadStream struct {
	frames []*pb.UploadChunk
	pos    int
}

func newFakeUploadStream(frames ...*pb.UploadChunk) *fakeUploadStream {
	return &fakeUploadStream{frames: frames}
}

func (f *fakeUploadStream) Recv() (*pb.UploadChunk, error) {
	if f.pos >= len(f.frames) {
		return nil, io.EOF
	}
	chunk := f.frames[f.pos]
	f.pos++
	return chunk, nil
}

// ---------------------------------------------------------------------------
// recvUpload tests
// ---------------------------------------------------------------------------

func TestRecvUpload_HappyPath(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 100)

	params := &pb.PreviewParams{
		FileId:       "11111111-1111-1111-1111-111111111111",
		Version:      1,
		Area:         "320x240",
		OutputFormat: "jpeg",
		Quality:      "high",
		Shape:        "rectangular",
		ServiceType:  "files",
	}

	metaFrame := &pb.UploadChunk{Payload: &pb.UploadChunk_Metadata{
		Metadata: &pb.UploadMetadata{Params: params},
	}}
	dataFrame := &pb.UploadChunk{Payload: &pb.UploadChunk_Data{Data: payload}}

	stream := newFakeUploadStream(metaFrame, dataFrame)
	gotParams, gotBlob, err := recvUpload(stream)
	if err != nil {
		t.Fatalf("recvUpload error: %v", err)
	}
	if gotParams.GetArea() != "320x240" {
		t.Errorf("params area: want 320x240, got %s", gotParams.GetArea())
	}
	if !bytes.Equal(gotBlob, payload) {
		t.Errorf("blob mismatch: want len=%d, got len=%d", len(payload), len(gotBlob))
	}
}

func TestRecvUpload_MultipleDataFrames(t *testing.T) {
	part1 := bytes.Repeat([]byte("b"), chunkSize)
	part2 := bytes.Repeat([]byte("c"), 42)

	params := &pb.PreviewParams{ServiceType: "files"}
	metaFrame := &pb.UploadChunk{Payload: &pb.UploadChunk_Metadata{
		Metadata: &pb.UploadMetadata{Params: params},
	}}
	dataFrame1 := &pb.UploadChunk{Payload: &pb.UploadChunk_Data{Data: part1}}
	dataFrame2 := &pb.UploadChunk{Payload: &pb.UploadChunk_Data{Data: part2}}

	stream := newFakeUploadStream(metaFrame, dataFrame1, dataFrame2)
	_, gotBlob, err := recvUpload(stream)
	if err != nil {
		t.Fatalf("recvUpload error: %v", err)
	}
	want := append(part1, part2...)
	if !bytes.Equal(gotBlob, want) {
		t.Errorf("blob mismatch: want len=%d, got len=%d", len(want), len(gotBlob))
	}
}

func TestRecvUpload_FirstFrameMustBeMetadata(t *testing.T) {
	dataFirst := &pb.UploadChunk{Payload: &pb.UploadChunk_Data{Data: []byte("bad")}}
	stream := newFakeUploadStream(dataFirst)

	_, _, err := recvUpload(stream)
	if err == nil {
		t.Fatal("expected error for non-metadata first frame")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", st.Code())
	}
}

func TestRecvUpload_ExceedsMaxSize(t *testing.T) {
	params := &pb.PreviewParams{ServiceType: "files"}
	metaFrame := &pb.UploadChunk{Payload: &pb.UploadChunk_Metadata{
		Metadata: &pb.UploadMetadata{Params: params},
	}}
	// Each frame is maxUploadBytes/2 + 1, two frames exceed the limit.
	bigChunk := bytes.Repeat([]byte("x"), maxUploadBytes/2+1)
	data1 := &pb.UploadChunk{Payload: &pb.UploadChunk_Data{Data: bigChunk}}
	data2 := &pb.UploadChunk{Payload: &pb.UploadChunk_Data{Data: bigChunk}}

	stream := newFakeUploadStream(metaFrame, data1, data2)
	_, _, err := recvUpload(stream)
	if err == nil {
		t.Fatal("expected error for oversized upload")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for oversize, got %s", st.Code())
	}
}
