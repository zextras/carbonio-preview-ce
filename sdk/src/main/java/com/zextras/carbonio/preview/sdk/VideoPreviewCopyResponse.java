// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

/**
 * Response DTO returned by {@link PreviewClient#copyVideoPreview(Query, String, String)}.
 *
 * <p>The Go endpoint returns {@code {"preview_id":"<uuid>"}} on HTTP 200. This object holds
 * that UUID — the storage identifier of the newly created copy.
 */
public final class VideoPreviewCopyResponse {

  private final String previewId;

  public VideoPreviewCopyResponse(String previewId) {
    this.previewId = previewId;
  }

  /** The storage UUID of the copied video preview. */
  public String getPreviewId() {
    return previewId;
  }
}
