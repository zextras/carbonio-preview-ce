// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.zextras.carbonio.preview.sdk.grpc.PreviewParams;

/**
 * Immutable value object carrying all preview query parameters.
 * Build via {@link QueryBuilder}.
 */
public final class Query {

  private final String fileId;
  private final int version;
  private final String area;
  private final String outputFormat;
  private final String quality;
  private final String shape;
  private final String serviceType;
  private final String ownerId;
  private final boolean crop;
  private final int firstPage;
  private final int lastPage;
  private final String langTag;

  Query(QueryBuilder b) {
    this.fileId = b.getFileId();
    this.version = b.getVersion();
    this.area = b.getArea();
    this.outputFormat = b.getOutputFormat();
    this.quality = b.getQuality();
    this.shape = b.getShape();
    this.serviceType = b.getServiceType();
    this.ownerId = b.getOwnerId();
    this.crop = b.isCrop();
    this.firstPage = b.getFirstPage();
    this.lastPage = b.getLastPage();
    this.langTag = b.getLangTag();
  }

  public String getFileId() { return fileId; }
  public int getVersion() { return version; }
  public String getArea() { return area; }
  public String getOutputFormat() { return outputFormat; }
  public String getQuality() { return quality; }
  public String getShape() { return shape; }
  public String getServiceType() { return serviceType; }
  public String getOwnerId() { return ownerId; }
  public boolean isCrop() { return crop; }
  public int getFirstPage() { return firstPage; }
  public int getLastPage() { return lastPage; }
  public String getLangTag() { return langTag; }

  /**
   * Converts this query to a {@link PreviewParams} protobuf message.
   * Null/empty string fields are left at the proto default (empty string).
   */
  public PreviewParams toProto() {
    PreviewParams.Builder b = PreviewParams.newBuilder();
    if (fileId != null) b.setFileId(fileId);
    b.setVersion(version);
    if (area != null) b.setArea(area);
    if (outputFormat != null) b.setOutputFormat(outputFormat);
    if (quality != null) b.setQuality(quality);
    if (shape != null) b.setShape(shape);
    if (serviceType != null) b.setServiceType(serviceType);
    if (ownerId != null) b.setOwnerId(ownerId);
    b.setCrop(crop);
    b.setFirstPage(firstPage);
    b.setLastPage(lastPage);
    if (langTag != null) b.setLangTag(langTag);
    return b.build();
  }
}
