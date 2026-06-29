// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import java.io.Closeable;
import java.io.IOException;
import java.io.InputStream;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpRequest.BodyPublishers;
import java.net.http.HttpResponse;
import java.net.http.HttpResponse.BodyHandlers;
import java.nio.charset.StandardCharsets;

/**
 * Thin REST client for the Carbonio Preview CE service.
 *
 * <p>Construct via {@link #atURL(String)} for the common case, or pass your own
 * {@link HttpClient} (useful for testing or TLS customisation).
 *
 * <h3>Binary streaming</h3>
 * <p>The openapi-generator {@code java/native} library returns {@code File} objects for binary
 * download endpoints because it writes the response body to a temp file before returning. That is
 * unacceptable for backend-to-backend streaming (unnecessary disk I/O + temp-file lifecycle).
 * This wrapper bypasses the generated binary-download path entirely: it assembles the request URL
 * from {@link Query} fields and issues the HTTP call itself using
 * {@link BodyHandlers#ofInputStream()}, streaming bytes directly without touching disk.
 *
 * <p>For multipart POST (upload) endpoints the same bypass applies: the wrapper serialises the
 * {@link InputStream} content as a {@code multipart/form-data} body using a hand-written
 * boundary encoder and {@link BodyPublishers#ofInputStream()}, avoiding the temp-file path that
 * the generated multipart helper would take.
 *
 * <p>All methods are thread-safe; the underlying {@link HttpClient} is shared.
 *
 * <h3>Video preview methods</h3>
 * <ul>
 *   <li>{@link #getPreviewOfVideo(Query)} — retrieve a video first-frame preview image</li>
 *   <li>{@link #getThumbnailOfVideo(Query)} — retrieve a video thumbnail image</li>
 *   <li>{@link #deleteVideoPreview(Query)} — delete a stored video preview from the server</li>
 *   <li>{@link #copyVideoPreview(Query, String, String)} — copy a stored video preview to a new id</li>
 * </ul>
 */
public final class PreviewClient implements Closeable {

  private static final String MULTIPART_BOUNDARY = "PreviewClientBoundary1234567890";

  private final String baseUrl;
  private final HttpClient http;

  /**
   * Creates a client connected to {@code baseUrl}.
   *
   * @param baseUrl base URL of the service, e.g. {@code "http://localhost:20003"}.
   *                Must NOT have a trailing slash.
   * @param http    the {@link HttpClient} to use
   */
  public PreviewClient(String baseUrl, HttpClient http) {
    // Normalise: strip trailing slash
    this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
    this.http = http;
  }

  /**
   * Creates a client backed by a default {@link HttpClient} configured to use HTTP/1.1.
   *
   * <p>HTTP/1.1 is used explicitly because the Go preview service speaks HTTP/1.1, and
   * because {@link java.net.http.HttpClient} with HTTP/2 + chunked streaming (unknown
   * content-length) triggers RST_STREAM cancellation on some server stacks.
   *
   * @param baseUrl base URL, e.g. {@code "http://localhost:20003"}
   */
  public static PreviewClient atURL(String baseUrl) {
    return new PreviewClient(baseUrl,
        HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1).build());
  }

  // -------------------------------------------------------------------------
  // GET downloads
  // -------------------------------------------------------------------------

  public PreviewResponse getPreviewOfImage(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/image/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + enc(query.getArea()) + "/"
        + buildImageQueryString(query, false);
    return doGet(query, url);
  }

  public PreviewResponse getThumbnailOfImage(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/image/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + enc(query.getArea()) + "/thumbnail/"
        + buildImageThumbnailQueryString(query);
    return doGet(query, url);
  }

  public PreviewResponse getPreviewOfPdf(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/pdf/" + enc(query.getFileId())
        + "/" + query.getVersion() + "/"
        + buildPdfQueryString(query);
    return doGet(query, url);
  }

  public PreviewResponse getThumbnailOfPdf(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/pdf/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + enc(query.getArea()) + "/thumbnail/"
        + buildImageThumbnailQueryString(query);
    return doGet(query, url);
  }

  public PreviewResponse getPreviewOfDocument(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/document/" + enc(query.getFileId())
        + "/" + query.getVersion() + "/"
        + buildDocumentQueryString(query);
    return doGet(query, url);
  }

  public PreviewResponse getThumbnailOfDocument(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/document/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + enc(query.getArea()) + "/thumbnail/"
        + buildDocumentThumbnailQueryString(query);
    return doGet(query, url);
  }

  // -------------------------------------------------------------------------
  // Video preview (GET, DELETE, POST copy)
  // -------------------------------------------------------------------------

  /**
   * Retrieves a stored video first-frame preview image.
   *
   * <p>HTTP contract:
   * {@code GET /preview/video/{id}/{version}/{area}/?service_type=&quality=&output_format=&crop=}
   * with optional header {@code FileOwnerId} when {@code query.getOwnerId()} is set.
   *
   * <p>Mirrors {@link #getPreviewOfImage(Query)} exactly — same return type, same exception
   * behaviour — but targets the {@code /preview/video/} path.
   *
   * @param query must carry fileId, version, area and serviceType; ownerId is optional.
   * @return a streaming {@link PreviewResponse} carrying the JPEG/PNG image bytes.
   * @throws PreviewException on any non-200 HTTP response.
   * @throws IllegalArgumentException when fileId is null or empty.
   */
  public PreviewResponse getPreviewOfVideo(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/video/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + enc(query.getArea()) + "/"
        + buildImageQueryString(query, false);
    return doGet(query, url);
  }

  /**
   * Retrieves a stored video thumbnail image.
   *
   * <p>HTTP contract:
   * {@code GET /preview/video/{id}/{version}/{area}/thumbnail/?service_type=&shape=&quality=&output_format=}
   * with optional header {@code FileOwnerId} when {@code query.getOwnerId()} is set.
   *
   * <p>Mirrors {@link #getThumbnailOfImage(Query)} exactly — same return type, same exception
   * behaviour — but targets the {@code /preview/video/} path.
   *
   * @param query must carry fileId, version and area; serviceType, shape, quality, outputFormat
   *              and ownerId are optional.
   * @return a streaming {@link PreviewResponse} carrying the thumbnail image bytes.
   * @throws PreviewException on any non-200 HTTP response.
   * @throws IllegalArgumentException when fileId is null or empty.
   */
  public PreviewResponse getThumbnailOfVideo(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/video/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + enc(query.getArea()) + "/thumbnail/"
        + buildImageThumbnailQueryString(query);
    return doGet(query, url);
  }

  /**
   * Deletes a stored video preview from the server.
   *
   * <p>HTTP contract:
   * {@code DELETE /preview/video/{id}/{version}/?service_type=}
   * with optional header {@code FileOwnerId} when {@code query.getOwnerId()} is set.
   *
   * <p>Returns normally (void) on HTTP 200 or 204. Throws {@link PreviewException} on any other
   * status. Note: the server may return 404 when no preview exists for that id/version/service
   * tuple — callers should decide whether to treat that as a no-op.
   *
   * @param query must carry fileId, version and serviceType; ownerId is optional.
   * @throws PreviewException on any non-2xx HTTP response (e.g. 404 = not found, 422 = bad args).
   * @throws IllegalArgumentException when fileId is null or empty.
   */
  public void deleteVideoPreview(Query query) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/video/" + enc(query.getFileId())
        + "/" + query.getVersion() + "/"
        + buildVideoDeleteQueryString(query);
    doDelete(query, url);
  }

  /**
   * Copies a stored video preview to a new target blob id.
   *
   * <p>HTTP contract:
   * {@code POST /preview/video/{id}/{version}/copy/?service_type=&target={targetBlobId}}
   * with headers:
   * <ul>
   *   <li>{@code FileOwnerId: <ownerId from query>} — identifies the source owner for PowerStore
   *       routing (same convention as all other methods).</li>
   *   <li>{@code TargetOwnerId: <targetOwnerId>} — identifies the owner under which the copy
   *       is stored on PowerStore. Sent only when {@code targetOwnerId} is non-null and
   *       non-empty.</li>
   * </ul>
   * The target blob id is also conveyed as the {@code target} query parameter so the Go endpoint
   * can construct the storage key without parsing headers. Response 200 body:
   * {@code {"preview_id":"<uuid>"}}.
   *
   * @param query         must carry fileId, version and serviceType; ownerId is the source owner.
   * @param targetBlobId  the UUID under which the copy should be stored (minted by the caller).
   * @param targetOwnerId the owner id of the copy's destination; may be null/empty when the
   *                      destination owner matches the source owner or the storage layer does not
   *                      require per-owner routing.
   * @return a {@link VideoPreviewCopyResponse} carrying the new preview's storage UUID.
   * @throws PreviewException on any non-200 HTTP response.
   * @throws IllegalArgumentException when fileId is null or empty.
   */
  public VideoPreviewCopyResponse copyVideoPreview(Query query, String targetBlobId, String targetOwnerId) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/video/" + enc(query.getFileId())
        + "/" + query.getVersion() + "/copy/"
        + buildVideoCopyQueryString(query, targetBlobId);

    HttpRequest.Builder b = HttpRequest.newBuilder()
        .uri(URI.create(url))
        .header("Content-Type", "application/json")
        .POST(BodyPublishers.noBody());
    applyOwner(b, query);
    if (targetOwnerId != null && !targetOwnerId.isEmpty()) {
      b.header("TargetOwnerId", targetOwnerId);
    }

    HttpResponse<String> response;
    try {
      response = http.send(b.build(), BodyHandlers.ofString());
    } catch (IOException e) {
      throw new PreviewException(0, "I/O error sending copy request to " + url, e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new PreviewException(0, "Copy request interrupted", e);
    }

    int status = response.statusCode();
    if (status != 200) {
      throw new PreviewException(status,
          "Server returned HTTP " + status + " for " + url);
    }
    return new VideoPreviewCopyResponse(parsePreviewId(response.body()));
  }

  // -------------------------------------------------------------------------
  // POST uploads (multipart/form-data)
  // -------------------------------------------------------------------------

  public PreviewResponse postPreviewOfImage(InputStream content, Query query) {
    String url = baseUrl + "/preview/image/" + enc(query.getArea()) + "/"
        + buildImageQueryString(query, false);
    return doPost(query, url, content);
  }

  public PreviewResponse postThumbnailOfImage(InputStream content, Query query) {
    String url = baseUrl + "/preview/image/" + enc(query.getArea()) + "/thumbnail/"
        + buildImageThumbnailQueryString(query);
    return doPost(query, url, content);
  }

  public PreviewResponse postPreviewOfPdf(InputStream content, Query query) {
    String url = baseUrl + "/preview/pdf/" + buildPdfUploadQueryString(query);
    return doPost(query, url, content);
  }

  public PreviewResponse postThumbnailOfPdf(InputStream content, Query query) {
    String url = baseUrl + "/preview/pdf/" + enc(query.getArea()) + "/thumbnail/"
        + buildImageThumbnailQueryString(query);
    return doPost(query, url, content);
  }

  public PreviewResponse postPreviewOfDocument(InputStream content, Query query) {
    String url = baseUrl + "/preview/document/" + buildDocumentUploadQueryString(query);
    return doPost(query, url, content);
  }

  public PreviewResponse postThumbnailOfDocument(InputStream content, Query query) {
    String url = baseUrl + "/preview/document/" + enc(query.getArea()) + "/thumbnail/"
        + buildDocumentThumbnailQueryString(query);
    return doPost(query, url, content);
  }

  // -------------------------------------------------------------------------
  // Health
  // -------------------------------------------------------------------------

  /**
   * Checks {@code GET /health/ready/} and returns {@code true} when the server responds with
   * HTTP 200. Returns {@code false} on any error or non-200 status.
   */
  public boolean healthReady() {
    try {
      HttpResponse<Void> resp = http.send(
          HttpRequest.newBuilder()
              .uri(URI.create(baseUrl + "/health/ready/"))
              .GET()
              .build(),
          BodyHandlers.discarding());
      return resp.statusCode() == 200;
    } catch (Exception e) {
      return false;
    }
  }

  // -------------------------------------------------------------------------
  // Lifecycle
  // -------------------------------------------------------------------------

  /**
   * No-op: {@link HttpClient} is not {@link Closeable}. Included for API parity with the old
   * gRPC {@link PreviewClient} which had to shut down a {@code ManagedChannel}.
   */
  @Override
  public void close() throws IOException {
    // java.net.http.HttpClient does not require explicit shutdown.
  }

  // -------------------------------------------------------------------------
  // Internal HTTP execution
  // -------------------------------------------------------------------------

  /**
   * Issues a GET request and returns a streaming {@link PreviewResponse}.
   *
   * <p>The response body is NOT read eagerly — it is streamed to the caller via
   * {@link BodyHandlers#ofInputStream()}. The caller must close the returned
   * {@link PreviewResponse} to release the connection.
   *
   * <p>If {@code query} carries a non-empty {@code ownerId}, the {@code FileOwnerId} HTTP header
   * is added to the request so that PowerStore-based infra can route to the correct storage node.
   */
  private PreviewResponse doGet(Query query, String url) {
    HttpRequest.Builder b = HttpRequest.newBuilder()
        .uri(URI.create(url))
        .GET();
    applyOwner(b, query);
    return execute(b.build());
  }

  /**
   * Issues a multipart/form-data POST and returns a streaming {@link PreviewResponse}.
   *
   * <p>The {@code content} InputStream is serialised as a {@code multipart/form-data} part
   * named {@code "file"} using a hard-coded boundary. The part content-type defaults to
   * {@code application/octet-stream}; the server does not validate it.
   *
   * <p>We write the boundary preamble as bytes, then pipe {@code content} directly through
   * {@link BodyPublishers#ofInputStream()} — the body is streamed without buffering the whole
   * file in memory.
   *
   * <p>NOTE: {@code java.net.http.HttpClient} with {@code ofInputStream()} cannot know the
   * content-length in advance, so the request is sent chunked or with a large-enough buffer
   * depending on the JVM. The preview server accepts this without issue.
   *
   * <p>If {@code query} carries a non-empty {@code ownerId}, the {@code FileOwnerId} HTTP header
   * is added to the request so that PowerStore-based infra can route to the correct storage node.
   */
  private PreviewResponse doPost(Query query, String url, InputStream content) {
    byte[] preamble = (
        "--" + MULTIPART_BOUNDARY + "\r\n"
        + "Content-Disposition: form-data; name=\"file\"; filename=\"file\"\r\n"
        + "Content-Type: application/octet-stream\r\n"
        + "\r\n"
    ).getBytes(StandardCharsets.UTF_8);

    byte[] epilogue = (
        "\r\n--" + MULTIPART_BOUNDARY + "--\r\n"
    ).getBytes(StandardCharsets.UTF_8);

    InputStream multipartBody = new SequencedInputStream(preamble, content, epilogue);

    HttpRequest.Builder b = HttpRequest.newBuilder()
        .uri(URI.create(url))
        .header("Content-Type", "multipart/form-data; boundary=" + MULTIPART_BOUNDARY)
        .POST(BodyPublishers.ofInputStream(() -> multipartBody));
    applyOwner(b, query);
    return execute(b.build());
  }

  /**
   * Issues a DELETE request and discards the response body.
   *
   * <p>Returns normally on HTTP 200 or 204; throws {@link PreviewException} on any other status.
   * The optional {@code FileOwnerId} header is sent if the query carries a non-empty ownerId.
   */
  private void doDelete(Query query, String url) {
    HttpRequest.Builder b = HttpRequest.newBuilder()
        .uri(URI.create(url))
        .DELETE();
    applyOwner(b, query);

    HttpResponse<Void> response;
    try {
      response = http.send(b.build(), BodyHandlers.discarding());
    } catch (IOException e) {
      throw new PreviewException(0, "I/O error sending DELETE request to " + url, e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new PreviewException(0, "DELETE request interrupted", e);
    }

    int status = response.statusCode();
    if (status != 200 && status != 204) {
      throw new PreviewException(status,
          "Server returned HTTP " + status + " for DELETE " + url);
    }
  }

  /**
   * Adds the {@code FileOwnerId} header to the request builder when the query carries a
   * non-null, non-empty owner ID. The preview server uses this header for PowerStore storage
   * routing; it is ignored by legacy storage nodes that do not require it.
   */
  private static void applyOwner(HttpRequest.Builder b, Query query) {
    String owner = query.getOwnerId();
    if (owner != null && !owner.isEmpty()) b.header("FileOwnerId", owner);
  }

  private PreviewResponse execute(HttpRequest request) {
    HttpResponse<InputStream> response;
    try {
      response = http.send(request, BodyHandlers.ofInputStream());
    } catch (IOException e) {
      throw new PreviewException(0, "I/O error sending request to " + request.uri(), e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new PreviewException(0, "Request interrupted", e);
    }

    int status = response.statusCode();
    if (status == 200) {
      String contentType = response.headers().firstValue("Content-Type").orElse("");
      String mimeType = contentType.contains(";")
          ? contentType.substring(0, contentType.indexOf(';')).trim()
          : contentType.trim();
      long contentLength = response.headers().firstValueAsLong("Content-Length").orElse(-1L);
      return new PreviewResponse(response.body(), contentLength, mimeType);
    }

    // Non-200: consume and discard the error body to release the connection
    try {
      response.body().close();
    } catch (IOException ignored) { /* best-effort */ }
    throw new PreviewException(status, "Server returned HTTP " + status + " for " + request.uri());
  }

  // -------------------------------------------------------------------------
  // Query-string builders
  // -------------------------------------------------------------------------

  private String buildImageQueryString(Query q, boolean thumbnailMode) {
    StringBuilder sb = new StringBuilder("?");
    appendRequired(sb, "service_type", q.getServiceType());
    appendOptional(sb, "quality", q.getQuality());
    appendOptional(sb, "output_format", q.getOutputFormat());
    if (!thumbnailMode) {
      appendOptional(sb, "crop", q.isCrop() ? "true" : null);
    }
    return sb.toString();
  }

  private String buildImageThumbnailQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    appendOptional(sb, "service_type", q.getServiceType());
    appendOptional(sb, "shape", q.getShape());
    appendOptional(sb, "quality", q.getQuality());
    appendOptional(sb, "output_format", q.getOutputFormat());
    return sb.toString();
  }

  private String buildPdfQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    appendRequired(sb, "service_type", q.getServiceType());
    if (q.getFirstPage() > 0) appendOptional(sb, "first_page", String.valueOf(q.getFirstPage()));
    if (q.getLastPage() > 0) appendOptional(sb, "last_page", String.valueOf(q.getLastPage()));
    return sb.toString();
  }

  private String buildPdfUploadQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    if (q.getFirstPage() > 0) appendOptional(sb, "first_page", String.valueOf(q.getFirstPage()));
    if (q.getLastPage() > 0) appendOptional(sb, "last_page", String.valueOf(q.getLastPage()));
    return sb.toString();
  }

  private String buildDocumentQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    appendRequired(sb, "service_type", q.getServiceType());
    if (q.getFirstPage() > 0) appendOptional(sb, "first_page", String.valueOf(q.getFirstPage()));
    if (q.getLastPage() > 0) appendOptional(sb, "last_page", String.valueOf(q.getLastPage()));
    appendOptional(sb, "lang_tag", q.getLangTag());
    return sb.toString();
  }

  private String buildDocumentUploadQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    if (q.getFirstPage() > 0) appendOptional(sb, "first_page", String.valueOf(q.getFirstPage()));
    if (q.getLastPage() > 0) appendOptional(sb, "last_page", String.valueOf(q.getLastPage()));
    appendOptional(sb, "lang_tag", q.getLangTag());
    return sb.toString();
  }

  private String buildDocumentThumbnailQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    appendOptional(sb, "service_type", q.getServiceType());
    appendOptional(sb, "shape", q.getShape());
    appendOptional(sb, "quality", q.getQuality());
    appendOptional(sb, "output_format", q.getOutputFormat());
    appendOptional(sb, "lang_tag", q.getLangTag());
    return sb.toString();
  }

  private String buildVideoDeleteQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    appendRequired(sb, "service_type", q.getServiceType());
    return sb.toString();
  }

  private String buildVideoCopyQueryString(Query q, String targetBlobId) {
    StringBuilder sb = new StringBuilder("?");
    appendRequired(sb, "service_type", q.getServiceType());
    appendRequired(sb, "target", targetBlobId);
    return sb.toString();
  }

  private static void appendRequired(StringBuilder sb, String key, String value) {
    if (value == null || value.isEmpty()) return;
    // sb starts with "?" so always use "&" if there is already a param
    if (sb.length() > 1) sb.append('&');
    sb.append(enc(key)).append('=').append(enc(value));
  }

  private static void appendOptional(StringBuilder sb, String key, String value) {
    if (value == null || value.isEmpty()) return;
    if (sb.length() > 1) sb.append('&');
    sb.append(enc(key)).append('=').append(enc(value));
  }

  private static String enc(String value) {
    if (value == null) return "";
    return URLEncoder.encode(value, StandardCharsets.UTF_8);
  }

  private static void requireFileId(Query query) {
    if (query.getFileId() == null || query.getFileId().isEmpty()) {
      throw new IllegalArgumentException("fileId is required for GET requests");
    }
  }

  /**
   * Extracts the {@code preview_id} field from a JSON response body
   * ({@code {"preview_id":"<id>"}}). Used by {@link #copyVideoPreview(Query, String, String)}.
   * Uses a minimal dependency-free scan to avoid coupling the client to the Jackson runtime.
   *
   * @throws PreviewException (status 0) when the field is absent or the body is malformed.
   */
  private static String parsePreviewId(String json) {
    if (json != null) {
      int key = json.indexOf("\"preview_id\"");
      if (key >= 0) {
        int colon = json.indexOf(':', key + "\"preview_id\"".length());
        if (colon >= 0) {
          int firstQuote = json.indexOf('"', colon + 1);
          if (firstQuote >= 0) {
            int secondQuote = json.indexOf('"', firstQuote + 1);
            if (secondQuote > firstQuote) {
              return json.substring(firstQuote + 1, secondQuote);
            }
          }
        }
      }
    }
    throw new PreviewException(0,
        "Generate response missing \"preview_id\" field: " + json);
  }

  // -------------------------------------------------------------------------
  // Helper: concatenate preamble + content + epilogue as a single InputStream
  // -------------------------------------------------------------------------

  /**
   * Concatenates three byte sources into a single InputStream without buffering:
   * a fixed-size byte array preamble, a streaming content body, and a fixed-size epilogue.
   */
  private static final class SequencedInputStream extends InputStream {

    private int phase = 0; // 0 = preamble, 1 = content, 2 = epilogue, 3 = done
    private int preamblePos = 0;
    private int epiloguePos = 0;

    private final byte[] preamble;
    private final InputStream content;
    private final byte[] epilogue;

    SequencedInputStream(byte[] preamble, InputStream content, byte[] epilogue) {
      this.preamble = preamble;
      this.content = content;
      this.epilogue = epilogue;
    }

    @Override
    public int read() throws IOException {
      while (true) {
        switch (phase) {
          case 0 -> {
            if (preamblePos < preamble.length) return preamble[preamblePos++] & 0xFF;
            phase = 1;
          }
          case 1 -> {
            int b = content.read();
            if (b != -1) return b;
            phase = 2;
          }
          case 2 -> {
            if (epiloguePos < epilogue.length) return epilogue[epiloguePos++] & 0xFF;
            phase = 3;
          }
          default -> { return -1; }
        }
      }
    }

    @Override
    public int read(byte[] buf, int off, int len) throws IOException {
      if (len == 0) return 0;
      while (true) {
        switch (phase) {
          case 0 -> {
            int avail = preamble.length - preamblePos;
            if (avail > 0) {
              int n = Math.min(avail, len);
              System.arraycopy(preamble, preamblePos, buf, off, n);
              preamblePos += n;
              return n;
            }
            phase = 1;
          }
          case 1 -> {
            int n = content.read(buf, off, len);
            if (n != -1) return n;
            phase = 2;
          }
          case 2 -> {
            int avail = epilogue.length - epiloguePos;
            if (avail > 0) {
              int n = Math.min(avail, len);
              System.arraycopy(epilogue, epiloguePos, buf, off, n);
              epiloguePos += n;
              return n;
            }
            phase = 3;
          }
          default -> { return -1; }
        }
      }
    }

    @Override
    public void close() throws IOException {
      content.close();
    }
  }
}
