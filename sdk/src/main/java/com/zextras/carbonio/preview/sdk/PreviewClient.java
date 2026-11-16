// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.zextras.carbonio.preview.sdk.rest.ApiClient;
import com.zextras.carbonio.preview.sdk.rest.ApiException;
import com.zextras.carbonio.preview.sdk.rest.api.HealthApi;
import com.zextras.carbonio.preview.sdk.rest.api.ThumbnailApi;
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
import java.time.Duration;
import java.util.OptionalInt;
import java.util.UUID;

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
 * <h3>Methods</h3>
 * <ul>
 *   <li>{@link #getThumbnail(Query)} — retrieve a thumbnail (200, or 202 while it is generated)</li>
 *   <li>{@link #getPreview(Query)} — retrieve a preview streamed from the server</li>
 *   <li>{@link #postThumbnail(InputStream, Query)} — thumbnail of an uploaded file</li>
 *   <li>{@link #postPreview(InputStream, Query)} — preview of an uploaded file</li>
 *   <li>{@link #deleteThumbnails(String)} — delete every stored thumbnail of a file</li>
 * </ul>
 */
public final class PreviewClient implements Closeable {

  private static final String MULTIPART_BOUNDARY = "PreviewClientBoundary1234567890";

  private final String baseUrl;
  private final HttpClient blobHttpClient;
  private final ThumbnailApi thumbnailApi;
  private final HealthApi healthApi;

  /**
   * Creates a client connected to {@code baseUrl}, using {@code http} for every request with no
   * per-request timeout. Kept for back-compat / TLS customisation; prefer {@link #builder(String)}
   * to control timeouts per call class.
   *
   * @param baseUrl base URL of the service, e.g. {@code "http://localhost:20003"}. Must NOT have a
   *     trailing slash.
   * @param http    the {@link HttpClient} to use
   */
  public PreviewClient(String baseUrl, HttpClient http) {
    // Back-compat: the injected client backs the blob path; the non-blob path (delete/health) uses a
    // default generated ApiClient with no timeouts. Prefer builder(String) for timeout control.
    this(normalize(baseUrl), http, defaultApiClient(normalize(baseUrl)));
  }

  private PreviewClient(String baseUrl, HttpClient blobHttpClient, ApiClient apiClient) {
    this.baseUrl = baseUrl;
    this.blobHttpClient = blobHttpClient;
    this.thumbnailApi = new ThumbnailApi(apiClient);
    this.healthApi = new HealthApi(apiClient);
  }

  private static String normalize(String baseUrl) {
    return baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
  }

  private static ApiClient defaultApiClient(String baseUrl) {
    ApiClient apiClient = new ApiClient();
    apiClient.updateBaseUri(baseUrl);
    return apiClient;
  }

  /**
   * Creates a client backed by a default {@link HttpClient} (HTTP/1.1, no timeouts).
   *
   * <p>HTTP/1.1 is used explicitly because the Go preview service speaks HTTP/1.1, and because
   * {@link java.net.http.HttpClient} with HTTP/2 + chunked streaming (unknown content-length)
   * triggers RST_STREAM cancellation on some server stacks.
   *
   * @param baseUrl base URL, e.g. {@code "http://localhost:20003"}
   */
  public static PreviewClient atURL(String baseUrl) {
    return builder(baseUrl).build();
  }

  /**
   * Fluent builder exposing timeout knobs per call class, mirroring the generated {@code ApiClient}
   * (no SDK defaults — nothing is set unless the caller sets it):
   *
   * <ul>
   *   <li>non-blob ops (delete/health) carry {@link Builder#apiConnectTimeout} + {@link
   *       Builder#apiReadTimeout};
   *   <li>blob ops (preview + thumbnail GET, and the multipart uploads)
   *       carry only {@link Builder#blobConnectTimeout}. There is deliberately no blob read/request
   *       timeout: a request timeout is a single absolute deadline over the whole exchange —
   *       harmless on a streamed download but fatal on a large upload — so blob transfers always run
   *       to completion; the connect timeout is the only guard.
   * </ul>
   */
  public static Builder builder(String baseUrl) {
    return new Builder(baseUrl);
  }

  /** See {@link #builder(String)}. */
  public static final class Builder {
    private final String baseUrl;
    private Duration apiConnectTimeout;
    private Duration apiReadTimeout;
    private Duration blobConnectTimeout;

    private Builder(String baseUrl) {
      this.baseUrl = baseUrl;
    }

    /** Connect timeout for non-blob (delete/health) ops; {@code null} = none (default). */
    public Builder apiConnectTimeout(Duration timeout) {
      this.apiConnectTimeout = timeout;
      return this;
    }

    /** Request/read timeout for non-blob (delete/health) ops; {@code null} = none (default). */
    public Builder apiReadTimeout(Duration timeout) {
      this.apiReadTimeout = timeout;
      return this;
    }

    /** Connect timeout for blob (streamed preview/thumbnail/upload) ops; {@code null} = none. */
    public Builder blobConnectTimeout(Duration timeout) {
      this.blobConnectTimeout = timeout;
      return this;
    }

    public PreviewClient build() {
      String normalized = normalize(baseUrl);
      ApiClient apiClient = new ApiClient();
      apiClient.updateBaseUri(normalized);
      // Must be set before constructing the *Api: their constructors snapshot the ApiClient's
      // timeouts into final fields, so setting them afterwards would be a silent no-op.
      if (apiConnectTimeout != null) {
        apiClient.setConnectTimeout(apiConnectTimeout);
      }
      if (apiReadTimeout != null) {
        apiClient.setReadTimeout(apiReadTimeout);
      }
      return new PreviewClient(normalized, http1Client(blobConnectTimeout), apiClient);
    }
  }

  private static HttpClient http1Client(Duration connectTimeout) {
    HttpClient.Builder builder = HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1);
    if (connectTimeout != null) {
      builder.connectTimeout(connectTimeout);
    }
    return builder.build();
  }

  // -------------------------------------------------------------------------
  // GET downloads
  // -------------------------------------------------------------------------

  /**
   * Retrieves a thumbnail.
   *
   * <p>HTTP contract:
   * {@code GET /thumbnail/{id}/{version}/{size}?service_type=&mime=&digest=&lang=} with optional
   * header {@code FileOwnerId} when {@code query.getOwnerId()} is set.
   *
   * @param query must carry fileId, version, size, serviceType and mime; digest, lang and ownerId
   *              are optional.
   * @return a streaming {@link PreviewResponse}: status 200 carries the thumbnail; status 202
   *         ({@link PreviewResponse#isGenerating()}) means it is still being generated, with the
   *         suggested delay in {@link PreviewResponse#getRetryAfterSeconds()} and an optional
   *         interim image as body.
   * @throws PreviewException on any other HTTP status.
   * @throws IllegalArgumentException when fileId, size or mime is missing.
   */
  public PreviewResponse getThumbnail(Query query) {
    requireFileId(query);
    requireSize(query);
    requireMime(query);
    String url = baseUrl
        + "/thumbnail/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + "/" + query.getSize().value()
        + buildQueryString(query, true);
    return doGet(query, url);
  }

  /**
   * Retrieves a preview, streamed from the server.
   *
   * <p>HTTP contract: {@code GET /preview/{id}/{version}?service_type=&mime=&lang=} with optional
   * header {@code FileOwnerId} when {@code query.getOwnerId()} is set.
   *
   * @param query must carry fileId, version, serviceType and mime; lang and ownerId are optional.
   * @return a streaming {@link PreviewResponse} whose MIME type is the one of the served preview.
   * @throws PreviewException on any non-200 HTTP response.
   * @throws IllegalArgumentException when fileId or mime is missing.
   */
  public PreviewResponse getPreview(Query query) {
    requireFileId(query);
    requireMime(query);
    String url = baseUrl
        + "/preview/" + enc(query.getFileId())
        + "/" + query.getVersion()
        + buildQueryString(query, false);
    return doGet(query, url);
  }

  /**
   * Deletes every stored thumbnail (all versions, both sizes) of a file. Idempotent.
   *
   * @param fileId UUID of the file.
   * @throws PreviewException on any non-2xx HTTP response.
   * @throws IllegalArgumentException when fileId is null or empty.
   */
  public void deleteThumbnails(String fileId) {
    if (fileId == null || fileId.isEmpty()) {
      throw new IllegalArgumentException("fileId is required");
    }
    try {
      thumbnailApi.deleteThumbnails(UUID.fromString(fileId));
    } catch (ApiException e) {
      throw new PreviewException(e.getCode(), "deleteThumbnails failed: " + e.getMessage(), e);
    }
  }

  // -------------------------------------------------------------------------
  // POST uploads (multipart/form-data)
  // -------------------------------------------------------------------------

  /**
   * Generates a thumbnail of an uploaded file.
   *
   * <p>HTTP contract: {@code POST /thumbnail/{size}?mime=&lang=}, multipart field {@code file}.
   *
   * @throws IllegalArgumentException when size or mime is missing.
   */
  public PreviewResponse postThumbnail(InputStream content, Query query) {
    requireSize(query);
    requireMime(query);
    String url = baseUrl + "/thumbnail/" + query.getSize().value() + buildUploadQueryString(query);
    return doPost(query, url, content);
  }

  /**
   * Generates a preview of an uploaded file.
   *
   * <p>HTTP contract: {@code POST /preview?mime=&lang=}, multipart field {@code file}.
   *
   * @throws IllegalArgumentException when mime is missing.
   */
  public PreviewResponse postPreview(InputStream content, Query query) {
    requireMime(query);
    String url = baseUrl + "/preview" + buildUploadQueryString(query);
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
      healthApi.getHealthReady();
      return true;
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
      response = blobHttpClient.send(request, BodyHandlers.ofInputStream());
    } catch (IOException e) {
      throw new PreviewException(0, "I/O error sending request to " + request.uri(), e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new PreviewException(0, "Request interrupted", e);
    }

    int status = response.statusCode();
    if (status == 200 || status == 202) {
      String contentType = response.headers().firstValue("Content-Type").orElse("");
      String mimeType = contentType.contains(";")
          ? contentType.substring(0, contentType.indexOf(';')).trim()
          : contentType.trim();
      long contentLength = response.headers().firstValueAsLong("Content-Length").orElse(-1L);
      return new PreviewResponse(
          response.body(), contentLength, mimeType, status, retryAfter(response));
    }

    // Other statuses: consume and discard the error body to release the connection
    try {
      response.body().close();
    } catch (IOException ignored) { /* best-effort */ }
    throw new PreviewException(status, "Server returned HTTP " + status + " for " + request.uri());
  }

  private static OptionalInt retryAfter(HttpResponse<?> response) {
    return response.headers().firstValue("Retry-After").map(v -> {
      try {
        return OptionalInt.of(Integer.parseInt(v.trim()));
      } catch (NumberFormatException e) {
        return OptionalInt.empty();
      }
    }).orElse(OptionalInt.empty());
  }

  // -------------------------------------------------------------------------
  // Query-string builders
  // -------------------------------------------------------------------------

  private static String buildQueryString(Query q, boolean withDigest) {
    StringBuilder sb = new StringBuilder("?");
    appendOptional(sb, "service_type", q.getServiceType());
    appendOptional(sb, "mime", q.getMime());
    if (withDigest) appendOptional(sb, "digest", q.getDigest());
    appendOptional(sb, "lang", q.getLang());
    return sb.toString();
  }

  private static String buildUploadQueryString(Query q) {
    StringBuilder sb = new StringBuilder("?");
    appendOptional(sb, "mime", q.getMime());
    appendOptional(sb, "lang", q.getLang());
    return sb.toString();
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

  private static void requireSize(Query query) {
    if (query.getSize() == null) {
      throw new IllegalArgumentException("size is required for thumbnail requests");
    }
  }

  private static void requireMime(Query query) {
    if (query.getMime() == null || query.getMime().isEmpty()) {
      throw new IllegalArgumentException("mime is required");
    }
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
