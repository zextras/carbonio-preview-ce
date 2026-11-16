// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

/** Thumbnail size: {@code S} = 320 px, {@code M} = 1280 px long side. */
public enum ThumbnailSize {
  S("s"),
  M("m");

  private final String value;

  ThumbnailSize(String value) {
    this.value = value;
  }

  /** The wire value used in the request path. */
  public String value() {
    return value;
  }
}
