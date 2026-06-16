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
import java.util.Arrays;
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

  // ---- Error mapping ----

  @Test
  void getPreviewOfImage_notFoundThrowsPreviewException() {
    Query q = new QueryBuilder().fileId("NOTFOUND").serviceType("files").build();
    PreviewException ex = assertThrows(PreviewException.class, () -> client.getPreviewOfImage(q));
    assertEquals(Status.Code.NOT_FOUND, ex.getCode());
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

  // ---- healthReady ----

  @Test
  void healthReady_returnsTrue_whenServerServing() {
    assertTrue(client.healthReady());
  }

  // =========================================================================
  // Fake implementations
  // =========================================================================

  /** Streams metadata+content for all download RPCs; returns NOT_FOUND for fileId="NOTFOUND". */
  private class FakePreviewService extends PreviewServiceGrpc.PreviewServiceImplBase {

    private void streamDownload(GetRequest req, StreamObserver<PreviewChunk> obs) {
      if ("NOTFOUND".equals(req.getParams().getFileId())) {
        obs.onError(Status.NOT_FOUND.withDescription("not found").asRuntimeException());
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
    private StreamObserver<UploadChunk> handleUpload(StreamObserver<PreviewChunk> responseObserver) {
      return new StreamObserver<UploadChunk>() {
        final java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();
        boolean metaReceived = false;

        @Override
        public void onNext(UploadChunk chunk) {
          if (chunk.getPayloadCase() == UploadChunk.PayloadCase.METADATA) {
            metaReceived = true;
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

    @Override
    public StreamObserver<UploadChunk> postImagePreview(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs);
    }

    @Override
    public StreamObserver<UploadChunk> postImageThumbnail(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs);
    }

    @Override
    public StreamObserver<UploadChunk> postPdfPreview(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs);
    }

    @Override
    public StreamObserver<UploadChunk> postPdfThumbnail(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs);
    }

    @Override
    public StreamObserver<UploadChunk> postDocumentPreview(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs);
    }

    @Override
    public StreamObserver<UploadChunk> postDocumentThumbnail(StreamObserver<PreviewChunk> obs) {
      return handleUpload(obs);
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
}
