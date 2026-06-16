// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import io.grpc.Status;
import io.grpc.Status.Code;

/**
 * Unchecked exception thrown by {@link PreviewClient} when the server returns a gRPC error status.
 *
 * <h3>Status code mapping to the old HTTP-SDK conventions</h3>
 * <ul>
 *   <li>{@link Code#NOT_FOUND} – item does not exist (old SDK: ItemNotFound / HTTP 404)</li>
 *   <li>{@link Code#FAILED_PRECONDITION} – semantic validation failed (old SDK: ValidationError / HTTP 422)</li>
 *   <li>{@link Code#INVALID_ARGUMENT} – malformed or missing request parameter (old SDK: BadRequest / HTTP 400)</li>
 *   <li>{@link Code#INTERNAL} – unexpected server-side failure (old SDK: InternalServerError / HTTP 500)</li>
 * </ul>
 */
public final class PreviewException extends RuntimeException {

  private final Status status;

  public PreviewException(Status status) {
    super(status.getDescription() != null ? status.getDescription() : status.getCode().name());
    this.status = status;
  }

  public PreviewException(Status status, Throwable cause) {
    super(status.getDescription() != null ? status.getDescription() : status.getCode().name(), cause);
    this.status = status;
  }

  /** The full gRPC {@link Status} returned by the server. */
  public Status getStatus() {
    return status;
  }

  /** Convenience accessor for the status code. */
  public Code getCode() {
    return status.getCode();
  }
}
