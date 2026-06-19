// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

/**
 * Unchecked exception thrown by {@link PreviewClient} when the server returns an HTTP error.
 *
 * <h3>HTTP status code semantics</h3>
 * <ul>
 *   <li>400 – malformed or missing request parameter (was: INVALID_ARGUMENT)</li>
 *   <li>404 – item does not exist (was: NOT_FOUND)</li>
 *   <li>422 – semantic validation failed (was: FAILED_PRECONDITION)</li>
 *   <li>500/502/503 – unexpected server-side failure (was: INTERNAL)</li>
 * </ul>
 */
public final class PreviewException extends RuntimeException {

  private final int httpStatus;

  public PreviewException(int httpStatus, String message) {
    super(message);
    this.httpStatus = httpStatus;
  }

  public PreviewException(int httpStatus, String message, Throwable cause) {
    super(message, cause);
    this.httpStatus = httpStatus;
  }

  /** The HTTP status code returned by the server. */
  public int getHttpStatus() {
    return httpStatus;
  }

  /** Convenience: true when the server returned a 404. */
  public boolean isNotFound() {
    return httpStatus == 404;
  }

  /** Convenience: true when the server returned a 400 or 422. */
  public boolean isBadRequest() {
    return httpStatus == 400 || httpStatus == 422;
  }
}
