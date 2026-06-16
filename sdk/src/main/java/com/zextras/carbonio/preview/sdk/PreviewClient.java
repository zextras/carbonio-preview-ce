// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.zextras.carbonio.preview.sdk.grpc.GetRequest;
import com.zextras.carbonio.preview.sdk.grpc.PreviewChunk;
import com.zextras.carbonio.preview.sdk.grpc.PreviewServiceGrpc;
import com.zextras.carbonio.preview.sdk.grpc.UploadChunk;
import com.zextras.carbonio.preview.sdk.grpc.UploadMetadata;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.health.v1.HealthCheckRequest;
import io.grpc.health.v1.HealthCheckResponse;
import io.grpc.health.v1.HealthGrpc;
import io.grpc.stub.BlockingClientCall;
import io.grpc.stub.StreamObserver;
import java.io.Closeable;
import java.io.IOException;
import java.io.InputStream;
import java.util.List;
import java.util.Objects;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Blocking gRPC client for the Carbonio Preview CE service.
 *
 * <p>Construct via {@link #atURL(String)} for a plaintext channel, or pass your own
 * {@link ManagedChannel} (useful for TLS or in-process testing).
 *
 * <p>All methods are thread-safe; the underlying channel is shared.
 */
public final class PreviewClient implements Closeable {

  private static final int UPLOAD_CHUNK_SIZE = 64 * 1024; // 64 KB

  private final ManagedChannel channel;
  private final PreviewServiceGrpc.PreviewServiceBlockingV2Stub blockingV2Stub;
  private final PreviewServiceGrpc.PreviewServiceStub asyncStub;
  private final HealthGrpc.HealthBlockingStub healthStub;

  /**
   * Creates a client backed by the given channel.
   * The caller is responsible for shutting it down via {@link #close()}.
   */
  public PreviewClient(ManagedChannel channel) {
    this.channel = channel;
    this.blockingV2Stub = PreviewServiceGrpc.newBlockingV2Stub(channel);
    this.asyncStub = PreviewServiceGrpc.newStub(channel);
    this.healthStub = HealthGrpc.newBlockingStub(channel);
  }

  /**
   * Creates a client connected to {@code target} using a plaintext (no TLS) channel.
   *
   * <p>The created {@link ManagedChannel} is owned by this {@code PreviewClient} and will
   * be shut down when {@link #close()} is called. Callers must not use the channel directly.
   *
   * @param target gRPC target string, e.g. {@code "localhost:8080"} or {@code "preview-service:8080"}
   */
  public static PreviewClient atURL(String target) {
    ManagedChannel channel = ManagedChannelBuilder
        .forTarget(target)
        .usePlaintext()
        .build();
    return new PreviewClient(channel);
  }

  // -------------------------------------------------------------------------
  // Downloads
  // -------------------------------------------------------------------------

  public PreviewResponse getPreviewOfImage(Query query) {
    return download(blockingV2Stub.getImagePreview(buildGetRequest(query)));
  }

  public PreviewResponse getThumbnailOfImage(Query query) {
    return download(blockingV2Stub.getImageThumbnail(buildGetRequest(query)));
  }

  public PreviewResponse getPreviewOfPdf(Query query) {
    return download(blockingV2Stub.getPdfPreview(buildGetRequest(query)));
  }

  public PreviewResponse getThumbnailOfPdf(Query query) {
    return download(blockingV2Stub.getPdfThumbnail(buildGetRequest(query)));
  }

  public PreviewResponse getPreviewOfDocument(Query query) {
    return download(blockingV2Stub.getDocumentPreview(buildGetRequest(query)));
  }

  public PreviewResponse getThumbnailOfDocument(Query query) {
    return download(blockingV2Stub.getDocumentThumbnail(buildGetRequest(query)));
  }

  private GetRequest buildGetRequest(Query query) {
    Objects.requireNonNull(query.getFileId(), "fileId is required for GET queries");
    return GetRequest.newBuilder().setParams(query.toProto()).build();
  }

  private PreviewResponse download(BlockingClientCall<?, PreviewChunk> call) {
    try {
      ChunkIteratorInputStream stream = new ChunkIteratorInputStream(call);
      return new PreviewResponse(stream, stream.getLength(), stream.getMimeType());
    } catch (StatusRuntimeException e) {
      throw new PreviewException(e.getStatus(), e);
    }
  }

  // -------------------------------------------------------------------------
  // Uploads (client-streaming -> server-streaming bidi-like)
  // -------------------------------------------------------------------------

  public PreviewResponse postPreviewOfImage(InputStream content, Query query) {
    return upload(content, query, (stub, observer) -> stub.postImagePreview(observer));
  }

  public PreviewResponse postThumbnailOfImage(InputStream content, Query query) {
    return upload(content, query, (stub, observer) -> stub.postImageThumbnail(observer));
  }

  public PreviewResponse postPreviewOfPdf(InputStream content, Query query) {
    return upload(content, query, (stub, observer) -> stub.postPdfPreview(observer));
  }

  public PreviewResponse postThumbnailOfPdf(InputStream content, Query query) {
    return upload(content, query, (stub, observer) -> stub.postPdfThumbnail(observer));
  }

  public PreviewResponse postPreviewOfDocument(InputStream content, Query query) {
    return upload(content, query, (stub, observer) -> stub.postDocumentPreview(observer));
  }

  public PreviewResponse postThumbnailOfDocument(InputStream content, Query query) {
    return upload(content, query, (stub, observer) -> stub.postDocumentThumbnail(observer));
  }

  @FunctionalInterface
  private interface UploadInitiator {
    StreamObserver<UploadChunk> initiate(
        PreviewServiceGrpc.PreviewServiceStub stub,
        StreamObserver<PreviewChunk> responseObserver);
  }

  private PreviewResponse upload(InputStream content, Query query, UploadInitiator initiator) {
    // Collect all response chunks synchronously
    CountDownLatch latch = new CountDownLatch(1);
    AtomicReference<Throwable> error = new AtomicReference<>();
    // CopyOnWriteArrayList for thread safety between async onNext callbacks and post-await read
    List<PreviewChunk> chunks = new CopyOnWriteArrayList<>();

    StreamObserver<PreviewChunk> responseObserver = new StreamObserver<PreviewChunk>() {
      @Override public void onNext(PreviewChunk chunk) { chunks.add(chunk); }
      @Override public void onError(Throwable t) { error.set(t); latch.countDown(); }
      @Override public void onCompleted() { latch.countDown(); }
    };

    StreamObserver<UploadChunk> requestObserver = initiator.initiate(asyncStub, responseObserver);

    // Send metadata frame first
    requestObserver.onNext(UploadChunk.newBuilder()
        .setMetadata(UploadMetadata.newBuilder().setParams(query.toProto()).build())
        .build());

    // Stream content in 64KB chunks; always close the caller's InputStream when done
    try {
      byte[] buf = new byte[UPLOAD_CHUNK_SIZE];
      int n;
      try {
        while ((n = content.read(buf)) != -1) {
          requestObserver.onNext(UploadChunk.newBuilder()
              .setData(com.google.protobuf.ByteString.copyFrom(buf, 0, n))
              .build());
        }
      } finally {
        try { content.close(); } catch (IOException ignored) {}
      }
    } catch (IOException e) {
      requestObserver.onError(e);
      throw new PreviewException(Status.INTERNAL.withDescription("I/O error reading upload content").withCause(e));
    }

    requestObserver.onCompleted();

    try {
      // Wait indefinitely for the real RPC completion — both onCompleted and onError count down.
      // Use a per-call gRPC deadline if a timeout is needed; do NOT rely on an arbitrary local timer.
      latch.await();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      requestObserver.onError(new RuntimeException("upload interrupted", e));
      throw new PreviewException(Status.CANCELLED.withDescription("Upload interrupted").withCause(e));
    }

    Throwable t = error.get();
    if (t != null) {
      if (t instanceof StatusRuntimeException sre) {
        throw new PreviewException(sre.getStatus(), sre);
      }
      throw new PreviewException(Status.INTERNAL.withDescription(t.getMessage()).withCause(t));
    }

    // Wrap the collected chunks as an iterator-backed stream
    ChunkIteratorInputStream stream = new ChunkIteratorInputStream(chunks.iterator());
    return new PreviewResponse(stream, stream.getLength(), stream.getMimeType());
  }

  // -------------------------------------------------------------------------
  // Health
  // -------------------------------------------------------------------------

  /**
   * Queries the gRPC health endpoint for the service named {@code ""} (overall server health).
   *
   * @return {@code true} if the server reports {@code SERVING}; {@code false} on any error or
   *         non-serving status
   */
  public boolean healthReady() {
    try {
      HealthCheckResponse resp = healthStub.check(
          HealthCheckRequest.newBuilder().setService("").build());
      return resp.getStatus() == HealthCheckResponse.ServingStatus.SERVING;
    } catch (Exception e) {
      return false;
    }
  }

  // -------------------------------------------------------------------------
  // Lifecycle
  // -------------------------------------------------------------------------

  /**
   * Shuts down the underlying channel, waiting up to 5 seconds for in-flight calls to complete.
   */
  public void shutdown() throws InterruptedException {
    channel.shutdown().awaitTermination(5, TimeUnit.SECONDS);
  }

  @Override
  public void close() throws IOException {
    try {
      shutdown();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      channel.shutdownNow();
    }
  }
}
