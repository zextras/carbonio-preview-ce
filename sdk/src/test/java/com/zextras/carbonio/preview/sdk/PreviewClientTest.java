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
  private static final String ID = "11111111-1111-1111-1111-111111111111";

  private static Query thumbQuery() {
    return new QueryBuilder()
        .fileId(ID)
        .version(3)
        .size(ThumbnailSize.S)
        .serviceType("FILES")
        .mime("image/jpeg")
        .digest("sha256:abc")
        .lang("it-IT")
        .ownerId("owner-1")
        .build();
  }

  @Test
  void getThumbnail_buildsPathQueryAndOwnerHeader() throws IOException {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", MIME_JPEG)
            .withHeader("Content-Length", String.valueOf(IMAGE_BYTES.length))
            .withBody(IMAGE_BYTES)));

    try (PreviewResponse r = client.getThumbnail(thumbQuery())) {
      assertEquals(200, r.getStatus());
      assertFalse(r.isGenerating());
      assertEquals(MIME_JPEG, r.getMimeType());
      assertEquals(IMAGE_BYTES.length, r.getLength());
      assertTrue(r.getRetryAfterSeconds().isEmpty());
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }

    verify(getRequestedFor(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .withQueryParam("service_type", equalTo("files"))
        .withQueryParam("mime", equalTo("image/jpeg"))
        .withQueryParam("digest", equalTo("sha256:abc"))
        .withQueryParam("lang", equalTo("it-IT"))
        .withHeader("FileOwnerId", equalTo("owner-1")));
  }

  @Test
  void getThumbnail_sizeMGoesInPath() throws IOException {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/0/m"))
        .willReturn(aResponse().withStatus(200).withHeader("Content-Type", "image/png").withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().fileId(ID).version(0).size(ThumbnailSize.M)
        .serviceType("chats").mime("image/png").build();
    try (PreviewResponse r = client.getThumbnail(q)) {
      assertEquals("image/png", r.getMimeType());
    }
    verify(getRequestedFor(urlPathEqualTo("/thumbnail/" + ID + "/0/m"))
        .withQueryParam("service_type", equalTo("chats"))
        .withoutHeader("FileOwnerId"));
  }

  @Test
  void getThumbnail_202ExposesRetryAfterAndBody() throws IOException {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .willReturn(aResponse()
            .withStatus(202)
            .withHeader("Retry-After", "3")
            .withHeader("Cache-Control", "no-store")
            .withHeader("Content-Type", MIME_JPEG)
            .withBody(IMAGE_BYTES)));

    try (PreviewResponse r = client.getThumbnail(thumbQuery())) {
      assertEquals(202, r.getStatus());
      assertTrue(r.isGenerating());
      assertEquals(3, r.getRetryAfterSeconds().getAsInt());
      assertEquals(MIME_JPEG, r.getMimeType());
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getThumbnail_202WithoutBodyHasEmptyStream() throws IOException {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .willReturn(aResponse().withStatus(202).withHeader("Retry-After", "3")));

    try (PreviewResponse r = client.getThumbnail(thumbQuery())) {
      assertTrue(r.isGenerating());
      assertEquals(0, r.getContent().readAllBytes().length);
    }
  }

  @Test
  void getThumbnail_202WithoutRetryAfterHasNoValue() throws IOException {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .willReturn(aResponse().withStatus(202)));

    try (PreviewResponse r = client.getThumbnail(thumbQuery())) {
      assertTrue(r.getRetryAfterSeconds().isEmpty());
    }
  }

  @Test
  void getThumbnail_omitsEmptyDigestAndLang() throws IOException {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .willReturn(aResponse().withStatus(200).withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().fileId(ID).version(3).size(ThumbnailSize.S)
        .serviceType("files").mime("image/jpeg").digest("").build();
    try (PreviewResponse r = client.getThumbnail(q)) {
      r.getContent().readAllBytes();
    }
    verify(getRequestedFor(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
        .withoutQueryParam("digest")
        .withoutQueryParam("lang"));
  }

  @Test
  void getPreview_buildsPathAndQuery() throws IOException {
    stubFor(get(urlPathEqualTo("/preview/" + ID + "/3"))
        .willReturn(aResponse().withStatus(200).withHeader("Content-Type", "application/pdf").withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().fileId(ID).version(3).serviceType("FILES")
        .mime("application/msword").lang("en-US").digest("ignored").ownerId("o").build();
    try (PreviewResponse r = client.getPreview(q)) {
      assertEquals(200, r.getStatus());
      assertEquals("application/pdf", r.getMimeType());
    }
    verify(getRequestedFor(urlPathEqualTo("/preview/" + ID + "/3"))
        .withQueryParam("service_type", equalTo("files"))
        .withQueryParam("mime", equalTo("application/msword"))
        .withQueryParam("lang", equalTo("en-US"))
        .withoutQueryParam("digest")
        .withHeader("FileOwnerId", equalTo("o")));
  }

  @Test
  void getPreview_streamsBody() throws IOException {
    byte[] big = new byte[1 << 20];
    java.util.Arrays.fill(big, (byte) 7);
    stubFor(get(urlPathEqualTo("/preview/" + ID + "/1"))
        .willReturn(aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "video/mp4; codecs=avc1")
            .withHeader("Content-Length", String.valueOf(big.length))
            .withBody(big)));

    Query q = new QueryBuilder().fileId(ID).version(1).serviceType("files").mime("video/mp4").build();
    try (PreviewResponse r = client.getPreview(q)) {
      assertEquals("video/mp4", r.getMimeType());
      assertEquals(big.length, r.getLength());
      byte[] first = r.getContent().readNBytes(10);
      assertEquals(10, first.length);
      assertEquals(big.length - 10, r.getContent().readAllBytes().length);
    }
  }

  @Test
  void postThumbnail_multipartWithSizeAndMime() throws IOException {
    stubFor(post(urlPathEqualTo("/thumbnail/m"))
        .willReturn(aResponse().withStatus(200).withHeader("Content-Type", MIME_JPEG).withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().size(ThumbnailSize.M).mime("image/jpeg").lang("fr-FR").build();
    byte[] upload = "upload-content".getBytes(StandardCharsets.UTF_8);
    try (PreviewResponse r = client.postThumbnail(new ByteArrayInputStream(upload), q)) {
      assertEquals(200, r.getStatus());
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
    verify(postRequestedFor(urlPathEqualTo("/thumbnail/m"))
        .withQueryParam("mime", equalTo("image/jpeg"))
        .withQueryParam("lang", equalTo("fr-FR"))
        .withHeader("Content-Type", containing("multipart/form-data"))
        .withRequestBody(containing("name=\"file\""))
        .withRequestBody(containing("upload-content")));
  }

  @Test
  void postPreview_multipart() throws IOException {
    stubFor(post(urlPathEqualTo("/preview"))
        .willReturn(aResponse().withStatus(200).withHeader("Content-Type", "application/pdf").withBody(IMAGE_BYTES)));

    Query q = new QueryBuilder().mime("application/vnd.oasis.opendocument.text").build();
    try (PreviewResponse r = client.postPreview(
        new ByteArrayInputStream("doc".getBytes(StandardCharsets.UTF_8)), q)) {
      assertEquals("application/pdf", r.getMimeType());
      assertArrayEquals(IMAGE_BYTES, r.getContent().readAllBytes());
    }
    verify(postRequestedFor(urlPathEqualTo("/preview"))
        .withQueryParam("mime", equalTo("application/vnd.oasis.opendocument.text"))
        .withoutQueryParam("lang")
        .withHeader("Content-Type", containing("multipart/form-data")));
  }

  @Test
  void deleteThumbnails_usesGeneratedApi() {
    stubFor(delete(urlPathEqualTo("/thumbnail/" + ID)).willReturn(aResponse().withStatus(204)));

    assertDoesNotThrow(() -> client.deleteThumbnails(ID));
    verify(deleteRequestedFor(urlPathEqualTo("/thumbnail/" + ID)));
  }

  @Test
  void deleteThumbnails_424ThrowsPreviewException() {
    stubFor(delete(urlPathEqualTo("/thumbnail/" + ID)).willReturn(aResponse().withStatus(424)));

    PreviewException e = assertThrows(PreviewException.class, () -> client.deleteThumbnails(ID));
    assertEquals(424, e.getHttpStatus());
  }

  @Test
  void deleteThumbnails_withoutFileIdThrowsIllegalArgument() {
    assertThrows(IllegalArgumentException.class, () -> client.deleteThumbnails(""));
    assertThrows(IllegalArgumentException.class, () -> client.deleteThumbnails(null));
  }

  @Test
  void errorsThrowPreviewExceptionWithStatus() {
    for (int status : new int[] {400, 404, 415, 422, 424, 502, 503}) {
      stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s"))
          .willReturn(aResponse().withStatus(status).withBody("{\"detail\":\"x\"}")));
      stubFor(get(urlPathEqualTo("/preview/" + ID + "/3"))
          .willReturn(aResponse().withStatus(status)));
      stubFor(post(urlPathEqualTo("/preview")).willReturn(aResponse().withStatus(status)));

      PreviewException t = assertThrows(PreviewException.class, () -> client.getThumbnail(thumbQuery()));
      assertEquals(status, t.getHttpStatus());
      Query q = new QueryBuilder().fileId(ID).version(3).serviceType("files").mime("video/mp4").build();
      assertEquals(status, assertThrows(PreviewException.class, () -> client.getPreview(q)).getHttpStatus());
      Query pq = new QueryBuilder().mime("video/mp4").build();
      assertEquals(status, assertThrows(PreviewException.class,
          () -> client.postPreview(new ByteArrayInputStream(new byte[1]), pq)).getHttpStatus());
    }
  }

  @Test
  void errorMapsNotFoundAndBadRequestHelpers() {
    stubFor(get(urlPathEqualTo("/thumbnail/" + ID + "/3/s")).willReturn(aResponse().withStatus(404)));
    assertTrue(assertThrows(PreviewException.class, () -> client.getThumbnail(thumbQuery())).isNotFound());
  }

  @Test
  void missingMimeIsIllegalArgument() {
    Query noMime = new QueryBuilder().fileId(ID).version(1).size(ThumbnailSize.S).serviceType("files").build();
    assertThrows(IllegalArgumentException.class, () -> client.getThumbnail(noMime));
    assertThrows(IllegalArgumentException.class, () -> client.getPreview(noMime));
    assertThrows(IllegalArgumentException.class,
        () -> client.postThumbnail(new ByteArrayInputStream(new byte[0]), noMime));
    assertThrows(IllegalArgumentException.class,
        () -> client.postPreview(new ByteArrayInputStream(new byte[0]), noMime));
  }

  @Test
  void missingFileIdIsIllegalArgument() {
    Query q = new QueryBuilder().version(1).size(ThumbnailSize.S).serviceType("files").mime("image/jpeg").build();
    assertThrows(IllegalArgumentException.class, () -> client.getThumbnail(q));
    assertThrows(IllegalArgumentException.class, () -> client.getPreview(q));
  }

  @Test
  void getThumbnail_withoutSizeIsIllegalArgument() {
    Query q = new QueryBuilder().fileId(ID).version(1).serviceType("files").mime("image/jpeg").build();
    assertThrows(IllegalArgumentException.class, () -> client.getThumbnail(q));
    assertThrows(IllegalArgumentException.class,
        () -> client.postThumbnail(new ByteArrayInputStream(new byte[0]), q));
  }

  @Test
  void thumbnailSizeValues() {
    assertEquals("s", ThumbnailSize.S.value());
    assertEquals("m", ThumbnailSize.M.value());
  }

  @Test
  void queryBuilderLowercasesServiceType() {
    assertEquals("files", new QueryBuilder().serviceType("FILES").build().getServiceType());
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

}
