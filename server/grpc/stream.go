// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
)

const (
	// chunkSize is the target size for each data chunk frame sent to the client.
	// Chosen to fit comfortably in a gRPC message while balancing throughput.
	chunkSize = 64 * 1024 // 64 KB

	// maxUploadBytes is the maximum total upload size accepted from a client
	// stream. Mirrors the 32 MB REST limit enforced by readMultipartFile.
	maxUploadBytes = 32 << 20 // 32 MB
)

// downloadSender is the minimal interface satisfied by any server-streaming
// gRPC send method that accepts PreviewChunks. Using an interface rather than
// the concrete generated type keeps streamBlob testable without a real gRPC
// transport.
type downloadSender interface {
	Send(*pb.PreviewChunk) error
}

// uploadReceiver is the minimal interface for a client-streaming recv method.
type uploadReceiver interface {
	Recv() (*pb.UploadChunk, error)
}

// streamBlob sends blob over stream as a sequence of PreviewChunk messages.
//
// The FIRST frame is always a PreviewMetadata frame containing the mime type
// and the total byte length. It is followed by zero or more chunk frames of
// up to chunkSize bytes each.
func streamBlob(stream downloadSender, mime string, blob []byte) error {
	// Send metadata frame first.
	metaFrame := &pb.PreviewChunk{
		Payload: &pb.PreviewChunk_Metadata{
			Metadata: &pb.PreviewMetadata{
				MimeType: mime,
				Length:   int64(len(blob)),
			},
		},
	}
	if err := stream.Send(metaFrame); err != nil {
		return err
	}

	// Send data in chunkSize pieces.
	for len(blob) > 0 {
		n := chunkSize
		if n > len(blob) {
			n = len(blob)
		}
		frame := &pb.PreviewChunk{
			Payload: &pb.PreviewChunk_Chunk{
				Chunk: blob[:n],
			},
		}
		if err := stream.Send(frame); err != nil {
			return err
		}
		blob = blob[n:]
	}
	return nil
}

// recvUpload reads an upload stream and reassembles the payload.
//
// Protocol:
//   - The FIRST frame MUST be an UploadMetadata frame (INVALID_ARGUMENT if not).
//   - Subsequent frames are data frames that are concatenated.
//   - Total accumulated size MUST NOT exceed maxUploadBytes (INVALID_ARGUMENT).
//
// Returns the parsed PreviewParams and the full assembled blob.
func recvUpload(stream uploadReceiver) (*pb.PreviewParams, []byte, error) {
	// Read first frame — must be metadata.
	// io.EOF means the client closed the send side without sending any frame —
	// treat as a malformed (empty) stream → INVALID_ARGUMENT.
	// Any other error is a transport/server fault → INTERNAL.
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, status.Errorf(codes.InvalidArgument, "upload stream contained no frames")
		}
		return nil, nil, status.Errorf(codes.Internal, "recv metadata frame: internal error")
	}
	meta, ok := first.Payload.(*pb.UploadChunk_Metadata)
	if !ok {
		return nil, nil, status.Errorf(codes.InvalidArgument,
			"first frame must be UploadMetadata, got data frame")
	}
	params := meta.Metadata.GetParams()

	// Read data frames.
	var buf []byte
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Warn("recvUpload: recv data frame error", "err", err)
			return nil, nil, status.Errorf(codes.Internal, "recv data frame: internal error")
		}
		data, ok := frame.Payload.(*pb.UploadChunk_Data)
		if !ok {
			return nil, nil, status.Errorf(codes.InvalidArgument, "expected data frame after metadata")
		}
		if len(buf)+len(data.Data) > maxUploadBytes {
			return nil, nil, status.Errorf(codes.InvalidArgument,
				"upload exceeds maximum size of %d bytes", maxUploadBytes)
		}
		buf = append(buf, data.Data...)
	}

	// Reject zero-byte uploads upfront with 422 FAILED_PRECONDITION, mirroring
	// REST readMultipartFile which returns FileNotValid for empty files (HTTP 422).
	if len(buf) == 0 {
		return nil, nil, toStatus(http.StatusUnprocessableEntity, config.Msg.FileNotValid)
	}

	return params, buf, nil
}
