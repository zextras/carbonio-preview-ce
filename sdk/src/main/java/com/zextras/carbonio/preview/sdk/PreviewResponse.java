// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import java.io.Closeable;
import java.io.IOException;
import java.io.InputStream;

/**
 * Immutable response from a preview REST call.
 *
 * <p>Callers MUST close this object when done to release the underlying HTTP connection.
 */
public final class PreviewResponse implements Closeable {

  private final InputStream content;
  private final long length;
  private final String mimeType;

  public PreviewResponse(InputStream content, long length, String mimeType) {
    this.content = content;
    this.length = length;
    this.mimeType = mimeType;
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

  /** Closes the underlying content stream, releasing the HTTP connection. */
  @Override
  public void close() throws IOException {
    content.close();
  }
}
