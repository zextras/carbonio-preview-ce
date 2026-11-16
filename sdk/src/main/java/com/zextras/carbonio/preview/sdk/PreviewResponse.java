// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import java.io.Closeable;
import java.io.IOException;
import java.io.InputStream;
import java.util.OptionalInt;

/**
 * Immutable response from a preview REST call.
 *
 * <p>Callers MUST close this object when done to release the underlying HTTP connection.
 */
public final class PreviewResponse implements Closeable {

  private final InputStream content;
  private final long length;
  private final String mimeType;
  private final int status;
  private final OptionalInt retryAfterSeconds;

  public PreviewResponse(
      InputStream content, long length, String mimeType, int status, OptionalInt retryAfterSeconds) {
    this.content = content;
    this.length = length;
    this.mimeType = mimeType;
    this.status = status;
    this.retryAfterSeconds = retryAfterSeconds;
  }

  /** The response body as a stream. Bytes are pulled from the HTTP response on read. */
  public InputStream getContent() {
    return content;
  }

  /** Total byte length as reported by the server's {@code Content-Length} header, or -1 if unknown. */
  public long getLength() {
    return length;
  }

  /** MIME type as reported by the server's {@code Content-Type} header. */
  public String getMimeType() {
    return mimeType;
  }

  /** HTTP status of the response: 200, or 202 while a thumbnail is being generated. */
  public int getStatus() {
    return status;
  }

  /** True when the server answered 202: the thumbnail is still being generated. */
  public boolean isGenerating() {
    return status == 202;
  }

  /** Seconds to wait before polling again, from the {@code Retry-After} header when present. */
  public OptionalInt getRetryAfterSeconds() {
    return retryAfterSeconds;
  }

  /** Closes the underlying content stream, releasing the HTTP connection. */
  @Override
  public void close() throws IOException {
    content.close();
  }
}
