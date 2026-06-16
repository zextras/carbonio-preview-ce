// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.zextras.carbonio.preview.sdk.grpc.PreviewChunk;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.io.IOException;
import java.io.InputStream;
import java.util.Iterator;

/**
 * Adapts a blocking gRPC server-streaming {@link Iterator}{@code <PreviewChunk>} to an
 * {@link InputStream}.
 *
 * <p>Protocol contract:
 * <ol>
 *   <li>The <em>first</em> frame must be a {@code PreviewMetadata} frame. It is consumed
 *       immediately in the constructor to populate {@link #getMimeType()} and {@link #getLength()}.
 *       If the first frame is not metadata, a {@link PreviewException} with
 *       {@link Status.Code#INTERNAL} is thrown.</li>
 *   <li>Subsequent frames are {@code chunk} frames. Bytes are pulled lazily from the iterator
 *       as the caller reads from this stream — no full buffering in memory.</li>
 * </ol>
 *
 * <p>Any {@link StatusRuntimeException} thrown by the iterator is re-thrown as a
 * {@link PreviewException}.
 */
public final class ChunkIteratorInputStream extends InputStream {

  private final Iterator<PreviewChunk> iterator;
  private final String mimeType;
  private final long length;

  /** Current byte buffer from the last-fetched chunk frame. May be empty or null. */
  private byte[] buffer;
  private int bufferPos;
  private boolean done;

  /**
   * Constructs the stream and consumes the mandatory first {@code PreviewMetadata} frame.
   *
   * @param iterator blocking iterator from a gRPC server-streaming call
   * @throws PreviewException if the first frame is missing or is not a metadata frame,
   *                          or if a gRPC error occurs while reading the first frame
   */
  public ChunkIteratorInputStream(Iterator<PreviewChunk> iterator) {
    this.iterator = iterator;
    PreviewChunk first = nextChunk();
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
   * Returns the next chunk from the iterator, or {@code null} if the stream is exhausted.
   * Wraps {@link StatusRuntimeException} as {@link PreviewException}.
   */
  private PreviewChunk nextChunk() {
    try {
      if (!iterator.hasNext()) {
        return null;
      }
      return iterator.next();
    } catch (StatusRuntimeException e) {
      throw new PreviewException(e.getStatus(), e);
    }
  }
}
