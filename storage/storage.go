// Package storage defines the seam interface between the preview service
// and whatever backing store holds the original files.
//
// The CE edition ships a DirectClient that talks to the carbonio-storages
// HTTP API.  The Advanced edition can wire in a different implementation
// (e.g. one that supports owner-ID-based routing) without touching this
// package.
package storage

import (
	"context"
	"io"
)

// Blob is an alias for raw file content retrieved from storage.
type Blob = []byte

// Client is the minimal interface the preview service requires from
// the storage layer.
type Client interface {
	// RetrieveData fetches the binary content of a file identified by
	// fileID + version from the given serviceType namespace.
	//
	// ownerID is provided for Advanced-edition implementations that
	// need it for routing.  The CE DirectClient ignores it.
	//
	// Errors:
	//   ErrNotFound   — storage returned HTTP 404
	//   ErrUnavailable — storage was unreachable or returned any other error
	RetrieveData(ctx context.Context, fileID string, version int, serviceType string, ownerID string) (Blob, error)

	// RetrieveDataStreaming fetches the same blob as RetrieveData but returns a
	// streaming body the caller reads incrementally. Closing the returned
	// ReadCloser (or cancelling ctx) aborts the in-flight HTTP transfer. Used by
	// the video path to avoid buffering whole videos in memory.
	//
	// Same error contract as RetrieveData (ErrNotFound / ErrUnavailable).
	RetrieveDataStreaming(ctx context.Context, fileID string, version int, serviceType string, ownerID string) (io.ReadCloser, error)
}
