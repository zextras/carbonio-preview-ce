// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zextras/carbonio-preview-ce/config"
	pb "github.com/zextras/carbonio-preview-ce/server/grpc/pb"
)

const (
	// chunkSize is the target size for each data chunk frame sent to the client.
	// Chosen to fit comfortably in a gRPC message while balancing throughput.
	chunkSize = 64 * 1024 // 64 KB
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

// UploadResult holds the result of recvUpload.
//
// For uploads at or below the memory threshold, Mem holds the bytes and
// TempFile is nil. For uploads exceeding the threshold, TempFile is an open
// *os.File positioned at the start, and Mem is nil.
//
// Callers MUST call Cleanup() after they are done with the result (including
// on error paths). Cleanup closes and removes TempFile if present.
// Callers MUST call Bytes() to read the data before or instead of reading
// TempFile directly — Bytes() handles the file-read transparently.
type UploadResult struct {
	Mem      []byte
	TempFile *os.File
}

// Bytes returns the upload payload as a []byte. For in-memory uploads it
// returns Mem directly. For spilled uploads it reads TempFile from the
// beginning. The caller must call Cleanup() after using the bytes.
func (u *UploadResult) Bytes() ([]byte, error) {
	if u.TempFile == nil {
		return u.Mem, nil
	}
	if _, err := u.TempFile.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(u.TempFile)
}

// Cleanup closes and removes the temp file (if any). Safe to call multiple
// times — subsequent calls after the first are no-ops.
func (u *UploadResult) Cleanup() {
	if u.TempFile == nil {
		return
	}
	name := u.TempFile.Name()
	_ = u.TempFile.Close()
	_ = os.Remove(name)
	u.TempFile = nil
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
//   - If total bytes > memThreshold, the data is spilled to a temp file
//     (bounded receive memory; no hard cap by default).
//   - If maxBytes > 0 and total bytes > maxBytes, returns INVALID_ARGUMENT
//     (ops safety valve; default 0 = unlimited, matching REST behaviour).
//   - Zero-byte uploads → FAILED_PRECONDITION (422 FileNotValid).
//
// Returns an *UploadResult, the parsed PreviewParams, and an error.
// Callers MUST call result.Cleanup() after use, including on error (the
// result may be partially written on error; Cleanup is always safe).
func recvUpload(stream uploadReceiver, memThreshold int64, maxBytes int64) (*UploadResult, *pb.PreviewParams, error) {
	result := &UploadResult{}

	// Read first frame — must be metadata.
	// io.EOF means the client closed the send side without sending any frame —
	// treat as a malformed (empty) stream → INVALID_ARGUMENT.
	// Any other error is a transport/server fault → INTERNAL.
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return result, nil, status.Errorf(codes.InvalidArgument, "upload stream contained no frames")
		}
		return result, nil, status.Errorf(codes.Internal, "recv metadata frame: internal error")
	}
	meta, ok := first.Payload.(*pb.UploadChunk_Metadata)
	if !ok {
		return result, nil, status.Errorf(codes.InvalidArgument,
			"first frame must be UploadMetadata, got data frame")
	}
	params := meta.Metadata.GetParams()

	// Accumulate data frames.
	var memBuf bytes.Buffer
	var totalBytes int64

	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			slog.Warn("recvUpload: recv data frame error", "err", recvErr)
			result.Cleanup()
			return &UploadResult{}, nil, status.Errorf(codes.Internal, "recv data frame: internal error")
		}
		data, ok := frame.Payload.(*pb.UploadChunk_Data)
		if !ok {
			result.Cleanup()
			return &UploadResult{}, nil, status.Errorf(codes.InvalidArgument, "expected data frame after metadata")
		}
		chunk := data.Data
		totalBytes += int64(len(chunk))

		// Enforce optional hard cap if configured.
		if maxBytes > 0 && totalBytes > maxBytes {
			result.Cleanup()
			return &UploadResult{}, nil, status.Errorf(codes.InvalidArgument,
				"upload exceeds configured maximum size of %d bytes", maxBytes)
		}

		// Spill to disk if memory budget exceeded.
		if result.TempFile == nil && totalBytes > memThreshold {
			// Create temp file and write buffered bytes first.
			f, tmpErr := os.CreateTemp("", "carbonio-preview-upload-*")
			if tmpErr != nil {
				result.Cleanup()
				return &UploadResult{}, nil, status.Errorf(codes.Internal, "create temp file: internal error")
			}
			result.TempFile = f
			if _, writeErr := f.Write(memBuf.Bytes()); writeErr != nil {
				result.Cleanup()
				return &UploadResult{}, nil, status.Errorf(codes.Internal, "write to temp file: internal error")
			}
			memBuf.Reset()
		}

		if result.TempFile != nil {
			if _, writeErr := result.TempFile.Write(chunk); writeErr != nil {
				result.Cleanup()
				return &UploadResult{}, nil, status.Errorf(codes.Internal, "write chunk to temp file: internal error")
			}
		} else {
			memBuf.Write(chunk)
		}
	}

	// Reject zero-byte uploads upfront with 422 FAILED_PRECONDITION, mirroring
	// REST readMultipartFile which returns FileNotValid for empty files (HTTP 422).
	if totalBytes == 0 {
		result.Cleanup()
		return &UploadResult{}, nil, toStatus(http.StatusUnprocessableEntity, config.Msg.FileNotValid)
	}

	// For in-memory path: move buffer bytes into result.
	if result.TempFile == nil {
		result.Mem = memBuf.Bytes()
	}
	// For spill path: TempFile is already set; caller reads via result.Bytes().

	return result, params, nil
}
