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
import java.time.Duration;

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
 */
public final class PreviewClient implements Closeable {

  private static final String MULTIPART_BOUNDARY = "PreviewClientBoundary1234567890";

  private final String baseUrl;
  private final HttpClient http;

  /**
   * Per-request timeout applied ONLY to {@link #generateVideoPreview(Query, String)}.
   * {@code null} means no explicit timeout (the default for the view-time / image client built
   * via {@link #atURL(String)}). The generate-capable client built via
   * {@link #atURL(String, Duration)} carries a (hardcoded by the caller) ceiling such as
   * {@code Duration.ofMinutes(15)} so a single generate call cannot hang forever.
   */
  private final Duration requestTimeout;

  /**
   * Creates a client connected to {@code baseUrl} with no per-request timeout.
   *
   * @param baseUrl base URL of the service, e.g. {@code "http://localhost:20003"}.
   *                Must NOT have a trailing slash.
   * @param http    the {@link HttpClient} to use
   */
  public PreviewClient(String baseUrl, HttpClient http) {
    this(baseUrl, http, null);
  }

  /**
   * Creates a client connected to {@code baseUrl} with an optional per-request timeout that is
   * applied to {@link #generateVideoPreview(Query, String)}.
   *
   * @param baseUrl        base URL of the service, e.g. {@code "http://localhost:20003"}.
   *                       Must NOT have a trailing slash.
   * @param http           the {@link HttpClient} to use
   * @param requestTimeout per-request timeout for the generate call, or {@code null} for none
   */
  public PreviewClient(String baseUrl, HttpClient http, Duration requestTimeout) {
    // Normalise: strip trailing slash
    this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
    this.http = http;
    this.requestTimeout = requestTimeout;
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

  /**
   * Creates a generate-capable client backed by a default HTTP/1.1 {@link HttpClient}, applying
   * {@code requestTimeout} as the per-request timeout for {@link #generateVideoPreview(Query, String)}.
   *
   * <p>WSC builds this variant with a hardcoded {@code Duration.ofMinutes(15)} so a generate call
   * (which preview normally answers within its own ~30s internal limit) cannot hang indefinitely
   * under load/latency. The timeout does NOT apply to the GET download / multipart POST methods.
   *
   * @param baseUrl        base URL, e.g. {@code "http://localhost:20003"}
   * @param requestTimeout per-request timeout applied to the generate call (never {@code null} here)
   */
  public static PreviewClient atURL(String baseUrl, Duration requestTimeout) {
    return new PreviewClient(baseUrl,
        HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1).build(),
        requestTimeout);
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
  // Video generation (server-side first-frame extract + store)
  // -------------------------------------------------------------------------

  /**
   * Triggers server-side generation of a video's first-frame preview. The server streams the
   * source video from storage, extracts the first frame, JPEG-encodes it (quality 90, full
   * resolution), and stores it at the caller-supplied {@code targetNodeId}, echoing that id back.
   *
   * <p>Synchronous from the caller's view: it returns only once the frame has been stored. The
   * server caps generation on a DEDICATED video semaphore and returns HTTP 429 (try-acquire,
   * no waiting) when full; that surfaces as a {@link PreviewException} with
   * {@code getHttpStatus() == 429}. Callers MUST treat 429 as transient backpressure — same as
   * 503 (preview down), 504 (deadline) and other 5xx — leaving the work pending and retrying at
   * the next trigger or sweep. A 422 means ffmpeg cannot decode the source (e.g. AV1 / corrupt)
   * and is terminal.
   *
   * <p>HTTP contract (must match the CE/Advanced generate endpoint exactly):
   * {@code POST /preview/video/generate/{fileId}/{version}/?service_type=<chats|files>&target=<targetNodeId>}
   * with header {@code FileOwnerId: <ownerId>}. Response 200 body
   * {@code {"preview_id":"<targetNodeId>"}}.
   *
   * @param query        must carry fileId (source node), version, serviceType and ownerId.
   * @param targetNodeId the UUID (minted by the caller / WSC) under which the frame is stored.
   * @return the storage node id of the stored first-frame image (equal to {@code targetNodeId}).
   * @throws PreviewException with {@link PreviewException#getHttpStatus()} carrying the server
   *                          status on any non-200 response (429 = busy/transient).
   */
  public String generateVideoPreview(Query query, String targetNodeId) {
    requireFileId(query);
    String url = baseUrl
        + "/preview/video/generate/" + enc(query.getFileId())
        + "/" + query.getVersion() + "/"
        + "?service_type=" + enc(query.getServiceType())
        + "&target=" + enc(targetNodeId);

    HttpRequest.Builder b = HttpRequest.newBuilder()
        .uri(URI.create(url))
        .header("Content-Type", "application/json")
        .POST(BodyPublishers.noBody());
    if (requestTimeout != null) {
      b.timeout(requestTimeout);
    }
    applyOwner(b, query);

    HttpResponse<String> response;
    try {
      response = http.send(b.build(), BodyHandlers.ofString());
    } catch (IOException e) {
      throw new PreviewException(0, "I/O error sending generate request to " + url, e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new PreviewException(0, "Generate request interrupted", e);
    }

    int status = response.statusCode();
    if (status != 200) {
      throw new PreviewException(status,
          "Server returned HTTP " + status + " for " + url);
    }
    return parsePreviewId(response.body());
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
   * Extracts the {@code preview_id} field from the generate endpoint's JSON response body
   * ({@code {"preview_id":"<id>"}}). Uses a minimal dependency-free scan to avoid coupling the
   * generate path to the Jackson runtime and to keep this wrapper byte-identical to the CE SDK.
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
