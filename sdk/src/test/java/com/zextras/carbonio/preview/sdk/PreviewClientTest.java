// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.github.tomakehurst.wiremock.WireMockServer;
import com.github.tomakehurst.wiremock.client.WireMock;
import com.github.tomakehurst.wiremock.core.WireMockConfiguration;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static com.github.tomakehurst.wiremock.client.WireMock.*;
import static org.junit.jupiter.api.Assertions.*;

/**
 * Integration-style unit tests for {@link PreviewClient} backed by WireMock.
 *
 * <p>Each test stubs a specific endpoint and verifies the wrapper's URL construction,
 * query-string building, binary streaming, and error mapping.
 */
class PreviewClientTest {

  private static final byte[] IMAGE_BYTES = "fake-image-content".getBytes(StandardCharsets.UTF_8);
  private static final String MIME_JPEG = "image/jpeg";

  private WireMockServer wireMock;
  private PreviewClient client;

  @BeforeEach
  void setUp() {
    wireMock = new WireMockServer(WireMockConfiguration.wireMockConfig().dynamicPort());
    wireMock.start();
    WireMock.configureFor("localhost", wireMock.port());
    client = PreviewClient.atURL("http://localhost:" + wireMock.port());
  }

  @AfterEach
  void tearDown() throws IOException {
    client.close();
    wireMock.stop();
  }

  @Test
  void builderApiReadTimeoutAbortsSlowNonBlobCall() {
    stubFor(
        get(urlPathEqualTo("/health/ready/"))
            .willReturn(aResponse().withStatus(200).withFixedDelay(1500)));
    PreviewClient timed =
        PreviewClient.builder("http://localhost:" + wireMock.port())
            .apiReadTimeout(java.time.Duration.ofMillis(200))
            .build();
    assertTimeoutPreemptively(
        java.time.Duration.ofSeconds(1), () -> assertFalse(timed.healthReady()));
  }

  // ── GET downloads ────────────────────────────────────────────────────────────

  @Test
  void getPreviewOfImage_returnsStreamWithCorrectContent() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/image/abc-uuid/1/100x200/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withHeader("Content-Length", String.valueOf(IMAGE_BYTES.length))
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("abc-uuid")
        .version(1)
        .area("100x200")
        .serviceType("files")
        .build();

    try (PreviewResponse r = client.getPreviewOfImage(q)) {
      assertEquals(MIME_JPEG, r.getMimeType());
      assertEquals(IMAGE_BYTES.length, r.getLength());
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getThumbnailOfImage_routesToCorrectPath() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/image/thumb-uuid/2/50x50/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("thumb-uuid")
        .version(2)
        .area("50x50")
        .serviceType("files")
        .quality("high")
        .build();

    try (PreviewResponse r = client.getThumbnailOfImage(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void enumValues_areLowercasedOnWire() throws IOException {
    // Callers may pass a Java enum's uppercase name() (HIGH/RECTANGULAR/JPEG/FILES);
    // the SDK must normalize to the lowercase the preview service's strict enum expects.
    stubFor(get(urlPathEqualTo("/preview/image/case-uuid/1/50x50/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("case-uuid")
        .version(1)
        .area("50x50")
        .serviceType("FILES")
        .quality("HIGH")
        .shape("RECTANGULAR")
        .outputFormat("JPEG")
        .build();

    assertEquals("high", q.getQuality());
    assertEquals("rectangular", q.getShape());
    assertEquals("jpeg", q.getOutputFormat());
    assertEquals("files", q.getServiceType());

    try (PreviewResponse r = client.getThumbnailOfImage(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }

    verify(getRequestedFor(urlPathEqualTo("/preview/image/case-uuid/1/50x50/thumbnail/"))
        .withQueryParam("quality", equalTo("high"))
        .withQueryParam("shape", equalTo("rectangular"))
        .withQueryParam("output_format", equalTo("jpeg"))
        .withQueryParam("service_type", equalTo("files")));
  }

  @Test
  void getPreviewOfPdf_routesToCorrectPath() throws IOException {
    byte[] pdfBytes = "%PDF-fake".getBytes(StandardCharsets.UTF_8);
    stubFor(get(urlPathEqualTo("/preview/pdf/pdf-uuid/3/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/pdf")
            .withBody(pdfBytes)));

    Query q = new QueryBuilder()
        .fileId("pdf-uuid")
        .version(3)
        .serviceType("files")
        .build();

    try (PreviewResponse r = client.getPreviewOfPdf(q)) {
      assertEquals("application/pdf", r.getMimeType());
      assertArrayEquals(pdfBytes, r.getContent().readAllBytes());
    }
  }

  @Test
  void getThumbnailOfPdf_routesToCorrectPath() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/pdf/pdf-uuid/1/100x100/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("pdf-uuid")
        .version(1)
        .area("100x100")
        .serviceType("files")
        .build();

    try (PreviewResponse r = client.getThumbnailOfPdf(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getPreviewOfDocument_routesToCorrectPath() throws IOException {
    byte[] docBytes = "doc-content".getBytes(StandardCharsets.UTF_8);
    stubFor(get(urlPathEqualTo("/preview/document/doc-uuid/1/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/pdf")
            .withBody(docBytes)));

    Query q = new QueryBuilder()
        .fileId("doc-uuid")
        .version(1)
        .serviceType("chats")
        .build();

    try (PreviewResponse r = client.getPreviewOfDocument(q)) {
      assertArrayEquals(docBytes, r.getContent().readAllBytes());
    }
  }

  @Test
  void getThumbnailOfDocument_routesToCorrectPath() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/document/doc-uuid/1/80x80/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("doc-uuid")
        .version(1)
        .area("80x80")
        .serviceType("files")
        .build();

    try (PreviewResponse r = client.getThumbnailOfDocument(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  // ── Error mapping ─────────────────────────────────────────────────────────

  @Test
  void getPreviewOfImage_404_throwsPreviewException() {
    stubFor(get(urlPathEqualTo("/preview/image/missing/1/100x100/"))
        .willReturn(aResponse()
            .withStatus(404)
            .withHeader("Content-Type", "application/json")
            .withBody("{\"detail\":\"not found\"}")));

    Query q = new QueryBuilder()
        .fileId("missing")
        .version(1)
        .area("100x100")
        .serviceType("files")
        .build();

    PreviewException ex = assertThrows(PreviewException.class,
        () -> client.getPreviewOfImage(q));
    assertEquals(404, ex.getHttpStatus());
    assertTrue(ex.isNotFound());
  }

  @Test
  void getPreviewOfImage_400_throwsPreviewException() {
    stubFor(get(urlPathEqualTo("/preview/image/bad/0/bad-area/"))
        .willReturn(aResponse()
            .withStatus(400)
            .withBody("{\"detail\":\"bad area\"}")));

    Query q = new QueryBuilder()
        .fileId("bad")
        .version(0)
        .area("bad-area")
        .serviceType("files")
        .build();

    PreviewException ex = assertThrows(PreviewException.class,
        () -> client.getPreviewOfImage(q));
    assertEquals(400, ex.getHttpStatus());
    assertTrue(ex.isBadRequest());
  }

  // ── POST uploads ──────────────────────────────────────────────────────────

  @Test
  void postPreviewOfImage_sendsMultipartBody_returnsResponse() throws IOException {
    stubFor(post(urlPathEqualTo("/preview/image/200x100/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .area("200x100")
        .serviceType("files")
        .build();

    byte[] uploadContent = "upload-content".getBytes(StandardCharsets.UTF_8);
    try (PreviewResponse r = client.postPreviewOfImage(
        new ByteArrayInputStream(uploadContent), q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }

    // Verify the request had multipart content-type
    verify(postRequestedFor(urlPathEqualTo("/preview/image/200x100/"))
        .withHeader("Content-Type", containing("multipart/form-data")));
  }

  @Test
  void postThumbnailOfImage_routesToCorrectPath() throws IOException {
    stubFor(post(urlPathEqualTo("/preview/image/160x120/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().area("160x120").build();
    try (PreviewResponse r = client.postThumbnailOfImage(
        new ByteArrayInputStream(new byte[]{1, 2, 3}), q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void postPreviewOfPdf_routesToCorrectPath() throws IOException {
    stubFor(post(urlPathEqualTo("/preview/pdf/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/pdf")
            .withBody("pdf-data".getBytes(StandardCharsets.UTF_8))));

    Query q = new QueryBuilder().firstPage(1).lastPage(5).build();
    try (PreviewResponse r = client.postPreviewOfPdf(
        new ByteArrayInputStream(new byte[]{0}), q)) {
      assertEquals("application/pdf", r.getMimeType());
    }
  }

  @Test
  void postThumbnailOfPdf_routesToCorrectPath() throws IOException {
    stubFor(post(urlPathEqualTo("/preview/pdf/120x80/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().area("120x80").build();
    try (PreviewResponse r = client.postThumbnailOfPdf(
        new ByteArrayInputStream(new byte[]{1}), q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void postPreviewOfDocument_routesToCorrectPath() throws IOException {
    stubFor(post(urlPathEqualTo("/preview/document/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/pdf")
            .withBody("doc-pdf".getBytes(StandardCharsets.UTF_8))));

    Query q = new QueryBuilder().langTag("it-IT").build();
    try (PreviewResponse r = client.postPreviewOfDocument(
        new ByteArrayInputStream(new byte[]{0}), q)) {
      assertEquals("application/pdf", r.getMimeType());
    }
  }

  @Test
  void postThumbnailOfDocument_routesToCorrectPath() throws IOException {
    stubFor(post(urlPathEqualTo("/preview/document/100x100/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().area("100x100").build();
    try (PreviewResponse r = client.postThumbnailOfDocument(
        new ByteArrayInputStream(new byte[]{1}), q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  // ── Health ────────────────────────────────────────────────────────────────

  @Test
  void healthReady_true_when200() {
    stubFor(get(urlPathEqualTo("/health/ready/"))
        .willReturn(aResponse().withStatus(200)));

    assertTrue(client.healthReady());
  }

  @Test
  void healthReady_false_when429() {
    stubFor(get(urlPathEqualTo("/health/ready/"))
        .willReturn(aResponse().withStatus(429)));

    assertFalse(client.healthReady());
  }

  @Test
  void healthReady_false_when502() {
    stubFor(get(urlPathEqualTo("/health/ready/"))
        .willReturn(aResponse().withStatus(502)));

    assertFalse(client.healthReady());
  }

  // ── Video preview (GET, DELETE, POST copy) ───────────────────────────────

  @Test
  void getPreviewOfVideo_routesToCorrectPath() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/640x480/"))
        .withQueryParam("service_type", equalTo("chats"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withHeader("Content-Length", String.valueOf(IMAGE_BYTES.length))
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(1)
        .area("640x480")
        .serviceType("chats")
        .build();

    try (PreviewResponse r = client.getPreviewOfVideo(q)) {
      assertEquals(MIME_JPEG, r.getMimeType());
      assertEquals(IMAGE_BYTES.length, r.getLength());
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getPreviewOfVideo_withOwnerId_sendsFileOwnerIdHeader() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/2/320x240/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(2)
        .area("320x240")
        .serviceType("files")
        .ownerId("owner-abc")
        .build();

    try (PreviewResponse r = client.getPreviewOfVideo(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }

    verify(getRequestedFor(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/2/320x240/"))
        .withHeader("FileOwnerId", equalTo("owner-abc")));
  }

  @Test
  void getPreviewOfVideo_404_throwsPreviewException() {
    stubFor(get(urlPathEqualTo("/preview/video/missing-vid/1/100x100/"))
        .willReturn(aResponse().withStatus(404)));

    Query q = new QueryBuilder()
        .fileId("missing-vid")
        .version(1)
        .area("100x100")
        .serviceType("files")
        .build();

    PreviewException ex = assertThrows(PreviewException.class,
        () -> client.getPreviewOfVideo(q));
    assertEquals(404, ex.getHttpStatus());
    assertTrue(ex.isNotFound());
  }

  @Test
  void getThumbnailOfVideo_routesToCorrectPath() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/video/thumb-vid/3/128x128/thumbnail/"))
        .withQueryParam("service_type", equalTo("files"))
        .withQueryParam("quality", equalTo("high"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("thumb-vid")
        .version(3)
        .area("128x128")
        .serviceType("files")
        .quality("high")
        .build();

    try (PreviewResponse r = client.getThumbnailOfVideo(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getThumbnailOfVideo_withoutFileId_throwsIllegalArgument() {
    Query q = new QueryBuilder().area("64x64").serviceType("files").build();
    assertThrows(IllegalArgumentException.class, () -> client.getThumbnailOfVideo(q));
  }

  @Test
  void deleteVideoPreview_200_completes() {
    stubFor(delete(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/"))
        .withQueryParam("service_type", equalTo("files"))
        .willReturn(aResponse().withStatus(200)));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(1)
        .serviceType("files")
        .build();

    // Must not throw
    assertDoesNotThrow(() -> client.deleteVideoPreview(q));
  }

  @Test
  void deleteVideoPreview_204_completes() {
    stubFor(delete(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/2/"))
        .withQueryParam("service_type", equalTo("chats"))
        .willReturn(aResponse().withStatus(204)));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(2)
        .serviceType("chats")
        .build();

    assertDoesNotThrow(() -> client.deleteVideoPreview(q));
  }

  @Test
  void deleteVideoPreview_withOwnerId_sendsFileOwnerIdHeader() {
    stubFor(delete(urlPathEqualTo("/preview/video/44444444-4444-4444-4444-444444444444/1/"))
        .willReturn(aResponse().withStatus(200)));

    Query q = new QueryBuilder()
        .fileId("44444444-4444-4444-4444-444444444444")
        .version(1)
        .serviceType("files")
        .ownerId("owner-xyz")
        .build();

    client.deleteVideoPreview(q);

    verify(deleteRequestedFor(urlPathEqualTo("/preview/video/44444444-4444-4444-4444-444444444444/1/"))
        .withHeader("FileOwnerId", equalTo("owner-xyz")));
  }

  @Test
  void deleteVideoPreview_404_throwsPreviewException() {
    stubFor(delete(urlPathEqualTo("/preview/video/33333333-3333-3333-3333-333333333333/1/"))
        .willReturn(aResponse().withStatus(404)));

    Query q = new QueryBuilder()
        .fileId("33333333-3333-3333-3333-333333333333")
        .version(1)
        .serviceType("files")
        .build();

    PreviewException ex = assertThrows(PreviewException.class,
        () -> client.deleteVideoPreview(q));
    assertEquals(404, ex.getHttpStatus());
  }

  @Test
  void copyVideoPreview_routesToCorrectPathAndReturnsPreviewId() {
    stubFor(post(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/copy/"))
        .withQueryParam("service_type", equalTo("files"))
        .withQueryParam("target", equalTo("22222222-2222-2222-2222-222222222222"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/json")
            .withBody("{\"preview_id\":\"22222222-2222-2222-2222-222222222222\"}")));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(1)
        .serviceType("files")
        .build();

    VideoPreviewCopyResponse resp = client.copyVideoPreview(q, "22222222-2222-2222-2222-222222222222", null);
    assertEquals("22222222-2222-2222-2222-222222222222", resp.getPreviewId());
  }

  @Test
  void copyVideoPreview_withOwnerIds_sendsCorrectHeaders() {
    stubFor(post(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/copy/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/json")
            .withBody("{\"preview_id\":\"22222222-2222-2222-2222-222222222222\"}")));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(1)
        .serviceType("chats")
        .ownerId("src-owner")
        .build();

    VideoPreviewCopyResponse resp = client.copyVideoPreview(q, "22222222-2222-2222-2222-222222222222", "tgt-owner");
    assertEquals("22222222-2222-2222-2222-222222222222", resp.getPreviewId());

    verify(postRequestedFor(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/copy/"))
        .withHeader("FileOwnerId", equalTo("src-owner"))
        .withHeader("TargetOwnerId", equalTo("tgt-owner")));
  }

  @Test
  void copyVideoPreview_withoutTargetOwnerId_doesNotSendTargetOwnerIdHeader() {
    stubFor(post(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/copy/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/json")
            .withBody("{\"preview_id\":\"x\"}")));

    Query q = new QueryBuilder()
        .fileId("11111111-1111-1111-1111-111111111111")
        .version(1)
        .serviceType("files")
        .ownerId("src-owner")
        .build();

    client.copyVideoPreview(q, "22222222-2222-2222-2222-222222222222", null);

    verify(postRequestedFor(urlPathEqualTo("/preview/video/11111111-1111-1111-1111-111111111111/1/copy/"))
        .withHeader("FileOwnerId", equalTo("src-owner"))
        .withoutHeader("TargetOwnerId"));
  }

  @Test
  void copyVideoPreview_404_throwsPreviewException() {
    stubFor(post(urlPathEqualTo("/preview/video/33333333-3333-3333-3333-333333333333/1/copy/"))
        .willReturn(aResponse().withStatus(404)));

    Query q = new QueryBuilder()
        .fileId("33333333-3333-3333-3333-333333333333")
        .version(1)
        .serviceType("files")
        .build();

    PreviewException ex = assertThrows(PreviewException.class,
        () -> client.copyVideoPreview(q, "22222222-2222-2222-2222-222222222222", null));
    assertEquals(404, ex.getHttpStatus());
  }

  // ── Query without fileId: IllegalArgumentException ───────────────────────

  @Test
  void getPreviewOfImage_withoutFileId_throwsIllegalArgument() {
    Query q = new QueryBuilder().area("100x100").serviceType("files").build();
    assertThrows(IllegalArgumentException.class, () -> client.getPreviewOfImage(q));
  }

  // ── FileOwnerId header propagation ────────────────────────────────────────

  @Test
  void getThumbnailOfImage_withOwnerId_sendsFileOwnerIdHeader() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/image/owner-file-uuid/1/50x50/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("owner-file-uuid")
        .version(1)
        .area("50x50")
        .serviceType("chats")
        .ownerId("owner-123")
        .build();

    try (PreviewResponse r = client.getThumbnailOfImage(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }

    verify(getRequestedFor(urlPathEqualTo("/preview/image/owner-file-uuid/1/50x50/thumbnail/"))
        .withHeader("FileOwnerId", equalTo("owner-123")));
  }

  @Test
  void getThumbnailOfImage_withoutOwnerId_doesNotSendFileOwnerIdHeader() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/image/no-owner-uuid/1/50x50/thumbnail/"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder()
        .fileId("no-owner-uuid")
        .version(1)
        .area("50x50")
        .serviceType("chats")
        .build();

    try (PreviewResponse r = client.getThumbnailOfImage(q)) {
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }

    verify(getRequestedFor(urlPathEqualTo("/preview/image/no-owner-uuid/1/50x50/thumbnail/"))
        .withoutHeader("FileOwnerId"));
  }
}
