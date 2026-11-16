// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import java.util.Locale;

/**
 * Builder for {@link Query}.
 *
 * <p>All fields are optional with sensible defaults; set only what you need.
 */
public final class QueryBuilder {

  private String fileId;
  private int version;
  private ThumbnailSize size;
  private String serviceType;
  private String ownerId;
  private String mime;
  private String digest;
  private String lang;

  public QueryBuilder fileId(String fileId) { this.fileId = fileId; return this; }
  public QueryBuilder version(int version) { this.version = version; return this; }
  public QueryBuilder size(ThumbnailSize size) { this.size = size; return this; }
  public QueryBuilder serviceType(String serviceType) { this.serviceType = lower(serviceType); return this; }
  public QueryBuilder ownerId(String ownerId) { this.ownerId = ownerId; return this; }
  public QueryBuilder mime(String mime) { this.mime = mime; return this; }
  public QueryBuilder digest(String digest) { this.digest = digest; return this; }
  public QueryBuilder lang(String lang) { this.lang = lang; return this; }

  // service_type is validated against a strict lowercase enum by the service, so callers may
  // pass any case (e.g. a Java enum's uppercase name()).
  private static String lower(String v) {
    return v == null ? null : v.toLowerCase(Locale.ROOT);
  }

  public Query build() {
    return new Query(this);
  }

  // Package-private accessors used by Query constructor
  String getFileId() { return fileId; }
  int getVersion() { return version; }
  ThumbnailSize getSize() { return size; }
  String getServiceType() { return serviceType; }
  String getOwnerId() { return ownerId; }
  String getMime() { return mime; }
  String getDigest() { return digest; }
  String getLang() { return lang; }
}
