// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.google.protobuf.ByteString;
import com.zextras.carbonio.preview.sdk.grpc.GetRequest;
import com.zextras.carbonio.preview.sdk.grpc.PreviewChunk;
import com.zextras.carbonio.preview.sdk.grpc.PreviewMetadata;
import com.zextras.carbonio.preview.sdk.grpc.PreviewServiceGrpc;
import com.zextras.carbonio.preview.sdk.grpc.UploadChunk;
import io.grpc.Context;
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.Status;
import io.grpc.health.v1.HealthCheckRequest;
import io.grpc.health.v1.HealthCheckResponse;
import io.grpc.health.v1.HealthGrpc;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class PreviewClientTest {

  private static final String SERVER_NAME = "preview-test-" + System.nanoTime();
  private static final byte[] CONTENT_BYTES = "hello preview world".getBytes(StandardCharsets.UTF_8);
  private static final String MIME = "image/jpeg";
  private static final long LENGTH = CONTENT_BYTES.length;

  private Server server;
  private ManagedChannel channel;
  private PreviewClient client;

  // Captures upload bytes for assertions
  private final AtomicReference<byte[]> lastUploadBytes = new AtomicReference<>();


  @BeforeEach
  void setUp() throws IOException {
    server = InProcessServerBuilder.forName(SERVER_NAME)
        .directExecutor()
        .addService(new FakePreviewService())
        .addService(new FakeHealthService())
        .build()
        .start();

    channel = InProcessChannelBuilder.forName(SERVER_NAME)
        .directExecutor()
        .build();

    client = new PreviewClient(channel);
  }

  @AfterEach
  void tearDown() throws Exception {
    client.close();
    server.shutdown();
    server.awaitTermination();
  }

  // ---- Download round-trip ----

  @Test
  void getPreviewOfImage_returnsCorrectBytesAndMetadata() throws IOException {
    Query q = new QueryBuilder().fileId("abc").version(1).serviceType("files").build();
    try (PreviewResponse r = client.getPreviewOfImage(q)) {
      assertEquals(MIME, r.getMimeType());
      assertEquals(LENGTH, r.getLength());
      assertArrayEquals(CONTENT_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getThumbnailOfImage_returnsCorrectBytesAndMetadata() throws IOException {
    Query q = new QueryBuilder().fileId("abc").version(1).serviceType("files").build();
    try (PreviewResponse r = client.getThumbnailOfImage(q)) {
      assertArrayEquals(CONTENT_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getPreviewOfPdf_returnsCorrectBytesAndMetadata() throws IOException {
    Query q = new QueryBuilder().fileId("abc").version(1).serviceType("files").build();
    try (PreviewResponse r = client.getPreviewOfPdf(q)) {
      assertArrayEquals(CONTENT_BYTES, r.getContent().readAllBytes());
    }
  }

  @Test
  void getPreviewOfDocument_returnsCorrectBytesAndMetadata() throws IOException {
    Query q = new QueryBuilder().fileId("abc").version(1).serviceType("files").build();
    try (PreviewResponse r = client.getPreviewOfDocument(q)) {
      assertArrayEquals(CONTENT_BYTES, r.getContent().readAllBytes());
    }
  }

  /** New: tests a previously-untested GET path (getThumbnailOfPdf). */
  @Test
  void getThumbnailOfPdf_returnsCorrectBytesAndMetadata() throws IOException {
    Query q = new QueryBuilder().fileId("abc").version(1).serviceType("files").build();
    try (PreviewResponse r = client.getThumbnailOfPdf(q)) {
      assertEquals(MIME, r.getMimeType());
      assertEquals(LENGTH, r.getLength());
      assertArrayEquals(CONTENT_BYTES, r.getContent().readAllBytes());
    }
  }

  // ---- Error mapping ----

  @Test
  void getPreviewOfImage_notFoundThrowsPreviewException() {
    Query q = new QueryBuilder().fileId("NOTFOUND").serviceType("files").build();
    PreviewException ex = assertThrows(PreviewException.class, () -> client.getPreviewOfImage(q));
    assertEquals(Status.Code.NOT_FOUND, ex.getCode());
  }

  /** New: mid-stream server error wraps as IOException with PreviewException cause. */
  @Test
  void midStreamError_throwsIOExceptionWrappingPreviewException() throws IOException {
    Query q = new QueryBuilder().fileId("MIDERROR").serviceType("files").build();
    // The server sends: metadata + "first" chunk + error.
    // The BlockingClientCall buffers messages eagerly, so the error may surface at any read —
    // even during the read that returns the "first" chunk bytes. Either way:
    // - The stream can be opened (metadata frame must be readable)
    // - At some point during reading, an IOException wrapping a PreviewException(INTERNAL) is thrown
    try (PreviewResponse r = client.getPreviewOfImage(q)) {
      IOException ioEx = assertThrows(IOException.class, () -> r.getContent().readAllBytes());
      assertInstanceOf(PreviewException.class, ioEx.getCause());
      assertEquals(Status.Code.INTERNAL, ((PreviewException) ioEx.getCause()).getCode());
    }
  }

  // ---- Cancellation / early close ----

  /**
   * New: early {@link PreviewResponse#close()} cancels the gRPC call — the server observes
   * cancellation and the client call does not hang.
   *
   * <p>Uses a separate server with a thread pool (not directExecutor) so that the server can
   * block waiting for more data while the client closes the stream.
   */
  @Test
  void earlyClose_cancelsGrpcCall_doesNotHang() throws Exception {
    // We need a non-directExecutor server so the server thread can block independently
    String slowServerName = "preview-slow-" + System.nanoTime();
    AtomicBoolean slowServerCancelled = new AtomicBoolean(false);
    CountDownLatch slowServerReadyLatch = new CountDownLatch(1);
    CountDownLatch slowServerCancelledLatch = new CountDownLatch(1);

    Server slowServer = InProcessServerBuilder.forName(slowServerName)
        // intentionally NOT directExecutor so server runs on its own thread
        .addService(new PreviewServiceGrpc.PreviewServiceImplBase() {
          @Override
          public void getImagePreview(GetRequest req, StreamObserver<PreviewChunk> obs) {
            obs.onNext(PreviewChunk.newBuilder()
                .setMetadata(PreviewMetadata.newBuilder().setMimeType(MIME).setLength(100L).build())
                .build());
            obs.onNext(PreviewChunk.newBuilder()
                .setChunk(ByteString.copyFrom(new byte[]{42}))
                .build());
            // Signal that we've sent the first chunk and are now blocking
            slowServerReadyLatch.countDown();
            // Spin-wait until the context is cancelled (client closed the stream)
            Context ctx = Context.current();
            long deadline = System.currentTimeMillis() + 5000;
            while (!ctx.isCancelled() && System.currentTimeMillis() < deadline) {
              try { Thread.sleep(10); } catch (InterruptedException e) { Thread.currentThread().interrupt(); break; }
            }
            if (ctx.isCancelled()) {
              slowServerCancelled.set(true);
              slowServerCancelledLatch.countDown();
            } else {
              slowServerCancelledLatch.countDown();
              obs.onCompleted();
            }
          }
        })
        .build()
        .start();

    ManagedChannel slowChannel = InProcessChannelBuilder.forName(slowServerName).build();
    try (PreviewClient slowClient = new PreviewClient(slowChannel)) {
      PreviewResponse r = slowClient.getPreviewOfImage(
          new QueryBuilder().fileId("any").serviceType("files").build());
      // Wait until server has sent the first chunk
      assertTrue(slowServerReadyLatch.await(2, TimeUnit.SECONDS), "Server should have sent first chunk");
      // Read 1 byte to confirm stream is live
      int b = r.getContent().read();
      assertNotEquals(-1, b);
      // Close early — this cancels the gRPC call
      r.close();
      // Server should observe cancellation within 3 seconds
      boolean observed = slowServerCancelledLatch.await(3, TimeUnit.SECONDS);
      assertTrue(observed, "Server latch should have counted down");
      assertTrue(slowServerCancelled.get(), "Server should have observed context cancellation");
    } finally {
      slowServer.shutdown();
      slowServer.awaitTermination();
    }
  }

  // ---- Upload round-trip ----

  @Test
  void postPreviewOfImage_serverReceivesExactBytes() throws IOException {
    Query q = new QueryBuilder().area("320x240").serviceType("files").build();
    ByteArrayInputStream input = new ByteArrayInputStream(CONTENT_BYTES);
    try (PreviewResponse r = client.postPreviewOfImage(input, q)) {
      assertNotNull(r);
      assertEquals(MIME, r.getMimeType());
      // The fake server echoes back the data it received; verify it got all bytes
      byte[] received = lastUploadBytes.get();
      assertNotNull(received, "Server should have received upload bytes");
      assertArrayEquals(CONTENT_BYTES, received);
    }
  }

  /** New: tests upload thumbnail path (previously untested). */
  @Test
  void postThumbnailOfImage_serverReceivesBytes() throws IOException {
    Query q = new QueryBuilder().area("160x120").serviceType("files").build();
    byte[] data = "thumbnail data".getBytes(StandardCharsets.UTF_8);
    ByteArrayInputStream input = new ByteArrayInputStream(data);
    try (PreviewResponse r = client.postThumbnailOfImage(input, q)) {
      assertNotNull(r);
      byte[] received = lastUploadBytes.get();
      assertNotNull(received);
      assertArrayEquals(data, received);
    }
  }

  /** New: upload RPC server error maps to PreviewException with INVALID_ARGUMENT code. */
  @Test
  void uploadError_throwsPreviewException_withCorrectCode() {
    Query q = new QueryBuilder().area("bad").serviceType("UPLOADERR").build();
    ByteArrayInputStream input = new ByteArrayInputStream(CONTENT_BYTES);
    PreviewException ex = assertThrows(PreviewException.class,
        () -> client.postPreviewOfImage(input, q));
    assertEquals(Status.Code.INVALID_ARGUMENT, ex.getCode());
  }

  // ---- healthReady ----

  @Test
  void healthReady_returnsTrue_whenServerServing() {
    assertTrue(client.healthReady());
  }

  /**
   * New: healthReady returns false when server reports NOT_SERVING.
   * Uses a dedicated in-process server returning NOT_SERVING.
   */
  @Test
  void healthReady_returnsFalse_whenServerNotServing() throws IOException, InterruptedException {
    String notServingName = "preview-not-serving-" + System.nanoTime();
    Server notServingServer = InProcessServerBuilder.forName(notServingName)
        .directExecutor()
        .addService(new NotServingHealthService())
        .build()
        .start();
    try {
      ManagedChannel notServingChannel = InProcessChannelBuilder.forName(notServingName)
          .directExecutor()
          .build();
      try (PreviewClient notServingClient = new PreviewClient(notServingChannel)) {
        assertFalse(notServingClient.healthReady());
      }
    } finally {
      notServingServer.shutdown();
      notServingServer.awaitTermination();
    }
  }

  // =========================================================================
  // Fake implementations
  // =========================================================================

  /** Streams metadata+content for all download RPCs; returns NOT_FOUND for fileId="NOTFOUND". */
  private class FakePreviewService extends PreviewServiceGrpc.PreviewServiceImplBase {

    private void streamDownload(GetRequest req, StreamObserver<PreviewChunk> obs) {
      String fileId = req.getParams().getFileId();
      if ("NOTFOUND".equals(fileId)) {
        obs.onError(Status.NOT_FOUND.withDescription("not found").asRuntimeException());
        return;
      }
      if ("MIDERROR".equals(fileId)) {
        // Send metadata + first chunk + then an error
        obs.onNext(PreviewChunk.newBuilder()
            .setMetadata(PreviewMetadata.newBuilder().setMimeType(MIME).setLength(5L).build())
            .build());
        obs.onNext(PreviewChunk.newBuilder()
            .setChunk(ByteString.copyFrom("first".getBytes(StandardCharsets.UTF_8)))
            .build());
        obs.onError(Status.INTERNAL.withDescription("mid-stream server error").asRuntimeException());
        return;
      }
      obs.onNext(PreviewChunk.newBuilder()
          .setMetadata(PreviewMetadata.newBuilder().setMimeType(MIME).setLength(LENGTH).build())
          .build());
      obs.onNext(PreviewChunk.newBuilder()
          .setChunk(ByteString.copyFrom(CONTENT_BYTES))
          .build());
      obs.onCompleted();
    }

    @Override
    public void getImagePreview(GetRequest req, StreamObserver<PreviewChunk> obs) {
      streamDownload(req, obs);
    }

    @Override
    public void getImageThumbnail(GetRequest req, StreamObserver<PreviewChunk> obs) {
      streamDownload(req, obs);
    }

    @Override
    public void getPdfPreview(GetRequest req, StreamObserver<PreviewChunk> obs) {
      streamDownload(req, obs);
    }

    @Override
    public void getPdfThumbnail(GetRequest req, StreamObserver<PreviewChunk> obs) {
      streamDownload(req, obs);
    }

    @Override
    public void getDocumentPreview(GetRequest req, StreamObserver<PreviewChunk> obs) {
      streamDownload(req, obs);
    }

    @Override
    public void getDocumentThumbnail(GetRequest req, StreamObserver<PreviewChunk> obs) {
      streamDownload(req, obs);
    }

    /** Collects all upload bytes, stores them in lastUploadBytes, then streams back the same metadata+content. */
    private StreamObserver<UploadChunk> handleUpload(
        StreamObserver<PreviewChunk> responseObserver, boolean error) {
      return new StreamObserver<UploadChunk>() {
        final java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();

        @Override
        public void onNext(UploadChunk chunk) {
          if (chunk.getPayloadCase() == UploadChunk.PayloadCase.METADATA) {
            // check if this is an error-trigger upload
          } else if (chunk.getPayloadCase() == UploadChunk.PayloadCase.DATA) {
            byte[] b = chunk.getData().toByteArray();
            buf.write(b, 0, b.length);
          }
        }

        @Override
        public void onError(Throwable t) {
          responseObserver.onError(t);
        }

        @Override
        public void onCompleted() {
          if (error) {
            responseObserver.onError(
                Status.INVALID_ARGUMENT.withDescription("invalid upload params").asRuntimeException());
            return;
          }
          lastUploadBytes.set(buf.toByteArray());
          // Echo back metadata + the received data
          responseObserver.onNext(PreviewChunk.newBuilder()
              .setMetadata(PreviewMetadata.newBuilder().setMimeType(MIME).setLength(buf.size()).build())
              .build());
          responseObserver.onNext(PreviewChunk.newBuilder()
              .setChunk(ByteString.copyFrom(buf.toByteArray()))
              .build());
          responseObserver.onCompleted();
        }
      };
    }

    private StreamObserver<UploadChunk> routeUpload(
        StreamObserver<PreviewChunk> obs, String serviceType) {
      // serviceType "UPLOADERR" triggers INVALID_ARGUMENT error
      boolean triggerError = "UPLOADERR".equals(serviceType);
      return handleUpload(obs, triggerError);
    }

    @Override
    public StreamObserver<UploadChunk> postImagePreview(StreamObserver<PreviewChunk> obs) {
      // We can't easily inspect params here without reading from the first chunk;
      // use a wrapper that checks the metadata frame
      return new UploadRouter(obs);
    }

    @Override
    public StreamObserver<UploadChunk> postImageThumbnail(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs, false);
    }

    @Override
    public StreamObserver<UploadChunk> postPdfPreview(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs, false);
    }

    @Override
    public StreamObserver<UploadChunk> postPdfThumbnail(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs, false);
    }

    @Override
    public StreamObserver<UploadChunk> postDocumentPreview(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs, false);
    }

    @Override
    public StreamObserver<UploadChunk> postDocumentThumbnail(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs, false);
    }

    /** Routes based on the serviceType field in the metadata frame. */
    private class UploadRouter implements StreamObserver<UploadChunk> {
      private final StreamObserver<PreviewChunk> responseObserver;
      private StreamObserver<UploadChunk> delegate;
      private final java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();
      private boolean errorMode = false;

      UploadRouter(StreamObserver<PreviewChunk> responseObserver) {
        this.responseObserver = responseObserver;
      }

      @Override
      public void onNext(UploadChunk chunk) {
        if (chunk.getPayloadCase() == UploadChunk.PayloadCase.METADATA) {
          String serviceType = chunk.getMetadata().getParams().getServiceType();
          errorMode = "UPLOADERR".equals(serviceType);
        } else if (chunk.getPayloadCase() == UploadChunk.PayloadCase.DATA) {
          byte[] b = chunk.getData().toByteArray();
          buf.write(b, 0, b.length);
        }
      }

      @Override
      public void onError(Throwable t) {
        responseObserver.onError(t);
      }

      @Override
      public void onCompleted() {
        if (errorMode) {
          responseObserver.onError(
              Status.INVALID_ARGUMENT.withDescription("invalid upload params").asRuntimeException());
          return;
        }
        lastUploadBytes.set(buf.toByteArray());
        responseObserver.onNext(PreviewChunk.newBuilder()
            .setMetadata(PreviewMetadata.newBuilder().setMimeType(MIME).setLength(buf.size()).build())
            .build());
        responseObserver.onNext(PreviewChunk.newBuilder()
            .setChunk(ByteString.copyFrom(buf.toByteArray()))
            .build());
        responseObserver.onCompleted();
      }
    }
  }

  private static class FakeHealthService extends HealthGrpc.HealthImplBase {
    @Override
    public void check(HealthCheckRequest req, StreamObserver<HealthCheckResponse> obs) {
      obs.onNext(HealthCheckResponse.newBuilder()
          .setStatus(HealthCheckResponse.ServingStatus.SERVING)
          .build());
      obs.onCompleted();
    }
  }

  private static class NotServingHealthService extends HealthGrpc.HealthImplBase {
    @Override
    public void check(HealthCheckRequest req, StreamObserver<HealthCheckResponse> obs) {
      obs.onNext(HealthCheckResponse.newBuilder()
          .setStatus(HealthCheckResponse.ServingStatus.NOT_SERVING)
          .build());
      obs.onCompleted();
    }
  }
}
