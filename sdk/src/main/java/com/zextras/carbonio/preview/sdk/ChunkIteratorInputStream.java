// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.zextras.carbonio.preview.sdk.grpc.PreviewChunk;
import io.grpc.Status;
import io.grpc.StatusException;
import io.grpc.stub.BlockingClientCall;
import java.io.IOException;
import java.io.InputStream;
import java.util.Iterator;

/**
 * Adapts either a cancellable gRPC server-streaming {@link BlockingClientCall}{@code <?, PreviewChunk>}
 * or an in-memory {@link Iterator}{@code <PreviewChunk>} to an {@link InputStream}.
 *
 * <p>Protocol contract:
 * <ol>
 *   <li>The <em>first</em> frame must be a {@code PreviewMetadata} frame. It is consumed
 *       immediately in the constructor to populate {@link #getMimeType()} and {@link #getLength()}.
 *       If the first frame is not metadata, a {@link PreviewException} with
 *       {@link Status.Code#INTERNAL} is thrown.</li>
 *   <li>Subsequent frames are {@code chunk} frames. Bytes are pulled lazily as the caller reads
 *       from this stream — no full buffering in memory.</li>
 * </ol>
 *
 * <p>Closing this stream (via {@link #close()}) cancels the underlying gRPC call (if one is
 * present), preventing resource leaks when a consumer stops reading before the stream is exhausted.
 *
 * <p>Any {@link StatusException} thrown by the call is re-thrown as an {@link IOException}
 * wrapping a {@link PreviewException} (except in the constructor, where it is thrown directly
 * as a {@link PreviewException}).
 */
public final class ChunkIteratorInputStream extends InputStream {

  /** Non-null when backed by a live gRPC call (download path). Null for the iterator path. */
  private final BlockingClientCall<?, PreviewChunk> call;
  /** Non-null when backed by an in-memory iterator (upload response path). Null for the call path. */
  private final Iterator<PreviewChunk> iterator;

  private final String mimeType;
  private final long length;

  /** Current byte buffer from the last-fetched chunk frame. May be empty or null. */
  private byte[] buffer;
  private int bufferPos;
  private boolean done;

  /**
   * Constructs the stream backed by a live gRPC server-streaming call and consumes the mandatory
   * first {@code PreviewMetadata} frame.
   *
   * <p>Calling {@link #close()} will cancel the underlying gRPC call.
   *
   * @param call cancellable blocking call from a gRPC server-streaming V2 stub
   * @throws PreviewException if the first frame is missing or is not a metadata frame,
   *                          or if a gRPC error occurs while reading the first frame
   */
  public ChunkIteratorInputStream(BlockingClientCall<?, PreviewChunk> call) {
    this.call = call;
    this.iterator = null;
    PreviewChunk first = nextChunkForConstructor();
    if (first == null || first.getPayloadCase() != PreviewChunk.PayloadCase.METADATA) {
      throw new PreviewException(
          Status.INTERNAL.withDescription(
              "Protocol violation: first frame must be PreviewMetadata"));
    }
    this.mimeType = first.getMetadata().getMimeType();
    this.length = first.getMetadata().getLength();
  }

  /**
   * Constructs the stream backed by an in-memory iterator (e.g. from a buffered upload response)
   * and consumes the mandatory first {@code PreviewMetadata} frame.
   *
   * <p>{@link #close()} is a no-op for this constructor since there is no live gRPC call to cancel.
   *
   * @param iterator iterator over a fully-buffered list of {@link PreviewChunk} frames
   * @throws PreviewException if the first frame is missing or is not a metadata frame
   */
  ChunkIteratorInputStream(Iterator<PreviewChunk> iterator) {
    this.call = null;
    this.iterator = iterator;
    PreviewChunk first = nextChunkForConstructor();
    if (first == null || first.getPayloadCase() != PreviewChunk.PayloadCase.METADATA) {
      throw new PreviewException(
          Status.INTERNAL.withDescription(
              "Protocol violation: first frame must be PreviewMetadata"));
    }
    this.mimeType = first.getMetadata().getMimeType();
    this.length = first.getMetadata().getLength();
  }

  public String getMimeType() {
    return mimeType;
  }

  public long getLength() {
    return length;
  }

  @Override
  public int read() throws IOException {
    byte[] b = new byte[1];
    int n = read(b, 0, 1);
    return n == -1 ? -1 : (b[0] & 0xFF);
  }

  @Override
  public int read(byte[] b, int off, int len) throws IOException {
    if (off < 0 || len < 0 || off + len > b.length) {
      throw new IndexOutOfBoundsException(
          "off=" + off + ", len=" + len + ", b.length=" + b.length);
    }
    if (len == 0) {
      return 0;
    }
    if (done) {
      return -1;
    }
    // If the current buffer is exhausted, fetch the next chunk
    while (buffer == null || bufferPos >= buffer.length) {
      PreviewChunk next = nextChunk();
      if (next == null) {
        done = true;
        return -1;
      }
      if (next.getPayloadCase() != PreviewChunk.PayloadCase.CHUNK) {
        // Unexpected metadata frame mid-stream — treat as protocol error
        throw new IOException(new PreviewException(
            Status.INTERNAL.withDescription(
                "Protocol violation: unexpected metadata frame after first frame")));
      }
      buffer = next.getChunk().toByteArray();
      bufferPos = 0;
      if (buffer.length == 0) {
        continue;
      }
    }
    int available = buffer.length - bufferPos;
    int toRead = Math.min(available, len);
    System.arraycopy(buffer, bufferPos, b, off, toRead);
    bufferPos += toRead;
    return toRead;
  }

  /**
   * Cancels the underlying gRPC call (if present) and marks this stream as done.
   * Safe to call multiple times and even after the stream was fully consumed.
   */
  @Override
  public void close() {
    done = true;
    if (call != null) {
      call.cancel("stream closed by consumer", null);
    }
  }

  /**
   * Called only from the constructor — throws {@link PreviewException} directly so callers
   * see it before any InputStream use.
   */
  private PreviewChunk nextChunkForConstructor() {
    if (call != null) {
      try {
        if (!call.hasNext()) {
          return null;
        }
        return call.read();
      } catch (StatusException e) {
        throw new PreviewException(e.getStatus(), e);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new PreviewException(Status.CANCELLED.withDescription("interrupted").withCause(e));
      }
    } else {
      // iterator path (upload response — no checked exceptions)
      if (!iterator.hasNext()) {
        return null;
      }
      return iterator.next();
    }
  }

  /**
   * Returns the next chunk, or {@code null} if the stream is exhausted.
   * Wraps errors as {@link IOException} (containing {@link PreviewException}) for InputStream contract.
   */
  private PreviewChunk nextChunk() throws IOException {
    if (call != null) {
      try {
        if (!call.hasNext()) {
          return null;
        }
        return call.read();
      } catch (StatusException e) {
        throw new IOException(new PreviewException(e.getStatus(), e));
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new IOException(new PreviewException(
            Status.CANCELLED.withDescription("interrupted").withCause(e)));
      }
    } else {
      // iterator path
      if (!iterator.hasNext()) {
        return null;
      }
      return iterator.next();
    }
  }
}
