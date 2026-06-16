// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import java.io.Closeable;
import java.io.IOException;
import java.io.InputStream;

/**
 * Immutable response from a preview RPC call.
 *
 * <p>Callers MUST close this object when done to release the underlying gRPC stream/resources.
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

  /** The response body as a stream. Lazy — bytes are pulled from the gRPC stream on read. */
  public InputStream getContent() {
    return content;
  }

  /** Total byte length as advertised by the server's PreviewMetadata frame. */
  public long getLength() {
    return length;
  }

  /** MIME type as advertised by the server's PreviewMetadata frame. */
  public String getMimeType() {
    return mimeType;
  }

  /** Closes the underlying content stream, cancelling the gRPC call if still in progress. */
  @Override
  public void close() throws IOException {
    content.close();
  }
}
