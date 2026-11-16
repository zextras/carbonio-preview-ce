// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

/**
 * Immutable value object carrying all preview query parameters.
 * Build via {@link QueryBuilder}.
 */
public final class Query {

  private final String fileId;
  private final int version;
  private final ThumbnailSize size;
  private final String serviceType;
  private final String ownerId;
  private final String mime;
  private final String digest;
  private final String lang;

  Query(QueryBuilder b) {
    this.fileId = b.getFileId();
    this.version = b.getVersion();
    this.size = b.getSize();
    this.serviceType = b.getServiceType();
    this.ownerId = b.getOwnerId();
    this.mime = b.getMime();
    this.digest = b.getDigest();
    this.lang = b.getLang();
  }

  public String getFileId() { return fileId; }
  public int getVersion() { return version; }
  public ThumbnailSize getSize() { return size; }
  public String getServiceType() { return serviceType; }
  public String getOwnerId() { return ownerId; }
  public String getMime() { return mime; }
  public String getDigest() { return digest; }
  public String getLang() { return lang; }
}
