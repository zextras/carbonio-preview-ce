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
  private String area;
  private String outputFormat;
  private String quality;
  private String shape;
  private String serviceType;
  private String ownerId;
  private boolean crop;
  private int firstPage;
  private int lastPage;
  private String langTag;

  public QueryBuilder fileId(String fileId) { this.fileId = fileId; return this; }
  public QueryBuilder version(int version) { this.version = version; return this; }
  public QueryBuilder area(String area) { this.area = area; return this; }
  public QueryBuilder outputFormat(String outputFormat) { this.outputFormat = lower(outputFormat); return this; }
  public QueryBuilder quality(String quality) { this.quality = lower(quality); return this; }
  public QueryBuilder shape(String shape) { this.shape = lower(shape); return this; }
  public QueryBuilder serviceType(String serviceType) { this.serviceType = lower(serviceType); return this; }
  public QueryBuilder ownerId(String ownerId) { this.ownerId = ownerId; return this; }
  public QueryBuilder crop(boolean crop) { this.crop = crop; return this; }
  public QueryBuilder firstPage(int firstPage) { this.firstPage = firstPage; return this; }
  public QueryBuilder lastPage(int lastPage) { this.lastPage = lastPage; return this; }
  public QueryBuilder langTag(String langTag) { this.langTag = langTag; return this; }

  // Enum-valued params (quality, shape, output_format, service_type) are lowercased
  // here so callers may pass any case (e.g. a Java enum's uppercase name() such as
  // "HIGH"/"JPEG"/"RECTANGULAR"). The preview service validates these against a
  // strict lowercase enum and 422s otherwise; the legacy SDK normalized the same way.
  private static String lower(String v) {
    return v == null ? null : v.toLowerCase(Locale.ROOT);
  }

  public Query build() {
    return new Query(this);
  }

  // Package-private accessors used by Query constructor
  String getFileId() { return fileId; }
  int getVersion() { return version; }
  String getArea() { return area; }
  String getOutputFormat() { return outputFormat; }
  String getQuality() { return quality; }
  String getShape() { return shape; }
  String getServiceType() { return serviceType; }
  String getOwnerId() { return ownerId; }
  boolean isCrop() { return crop; }
  int getFirstPage() { return firstPage; }
  int getLastPage() { return lastPage; }
  String getLangTag() { return langTag; }
}
