# server/grpc

gRPC transport layer for carbonio-preview-ce.

## Package layout

| Package | Purpose |
|---------|---------|
| `pb/`   | Generated protobuf + gRPC stubs — committed to the repo so CI needs no protoc |
| `grpc`  | Service implementation, streaming helpers, error mapper, server wiring |

## Regenerating pb/

Requirements (dev machine only; not needed for the Go build):

```sh
# Install plugins (once per machine)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Regenerate (run from repo root)
PATH="$PATH:$(go env GOPATH)/bin" protoc \
  --go_out=server/grpc/pb \
  --go_opt=paths=source_relative \
  --go-grpc_out=server/grpc/pb \
  --go-grpc_opt=paths=source_relative \
  -I proto \
  proto/preview.proto
```

protoc 3.21.x is required (the version shipped by Ubuntu 22.04/24.04).

## Streaming convention

- **Downloads** (`Get*` RPCs): server-streaming. The server sends a `PreviewMetadata`
  frame first (mime_type + total byte length), then zero or more `~64 KB` chunk frames.
- **Uploads** (`Post*` RPCs): bidi-streaming (client sends, server streams back).
  The client sends an `UploadMetadata` frame first, then one or more `~64 KB` data frames.
  The server replies with the same metadata-then-chunks convention as downloads.

## Error mapping

| HTTP status | gRPC code          |
|-------------|--------------------|
| 404         | NOT_FOUND          |
| 400         | INVALID_ARGUMENT   |
| 422         | FAILED_PRECONDITION |
| 500         | INTERNAL           |
