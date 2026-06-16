// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
package com.zextras.carbonio.preview.sdk;

import com.zextras.carbonio.preview.sdk.grpc.GetRequest;
import com.zextras.carbonio.preview.sdk.grpc.PreviewChunk;
import com.zextras.carbonio.preview.sdk.grpc.PreviewChunkStream;
import com.zextras.carbonio.preview.sdk.grpc.PreviewServiceGrpc;
import com.zextras.carbonio.preview.sdk.grpc.UploadChunk;
import com.zextras.carbonio.preview.sdk.grpc.UploadMetadata;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusException;
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

  /**
   * Wraps a live server-streaming call in a {@link PreviewChunkStream} (generated helper) and
   * builds a {@link PreviewResponse} from the typed metadata.
   *
   * <p>Error translation at the SDK boundary:
   * <ul>
   *   <li>{@link IllegalStateException} from the constructor (bad/missing first frame) →
   *       {@link PreviewException} with {@link Status.Code#INTERNAL}.</li>
   *   <li>{@link StatusRuntimeException} from the stub layer (e.g. NOT_FOUND before the stream
   *       even opens) → {@link PreviewException} preserving the gRPC status code.</li>
   *   <li>Mid-stream {@link IOException} thrown by the {@code InputStream} read methods of
   *       {@link PreviewChunkStream} carries a {@link StatusException} as its cause; the
   *       {@link TranslatingInputStream} wrapper re-throws it as
   *       {@code IOException(new PreviewException(status, cause))} so consumers receive a
   *       consistently-typed cause regardless of which read call hits the error.</li>
   * </ul>
   */
  private PreviewResponse download(BlockingClientCall<?, PreviewChunk> call) {
    try {
      PreviewChunkStream stream = new PreviewChunkStream(call);
      InputStream translating = new TranslatingInputStream(stream);
      return new PreviewResponse(
          translating,
          stream.getMetadata().getLength(),
          stream.getMetadata().getMimeType());
    } catch (IllegalStateException e) {
      // The generated PreviewChunkStream constructor throws IllegalStateException for two cases:
      // 1. A gRPC error before any frame (cause = StatusException) — preserve that gRPC status.
      // 2. A genuine protocol violation (no cause or non-gRPC cause) — report as INTERNAL.
      Throwable cause = e.getCause();
      if (cause instanceof StatusException se) {
        throw new PreviewException(se.getStatus(), se);
      }
      throw new PreviewException(
          Status.INTERNAL.withDescription(e.getMessage()).withCause(e));
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

    // Wrap the collected chunks as the generated iterator-backed stream
    PreviewChunkStream stream = new PreviewChunkStream(chunks.iterator());
    return new PreviewResponse(
        stream,
        stream.getMetadata().getLength(),
        stream.getMetadata().getMimeType());
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

  // -------------------------------------------------------------------------
  // Inner helpers
  // -------------------------------------------------------------------------

  /**
   * Wraps a {@link PreviewChunkStream} and translates its mid-stream {@link IOException}s into
   * {@code IOException(PreviewException)} so that the SDK's public error contract is preserved:
   * callers always see a {@link PreviewException} as the cause of any mid-stream gRPC failure.
   *
   * <p>The generated {@link PreviewChunkStream} throws {@link IOException} whose cause is a
   * {@link StatusException} when the underlying gRPC call fails. This wrapper catches that and
   * re-throws as {@code new IOException(new PreviewException(status, statusEx))}, matching the
   * error shape that was previously produced by the hand-written {@code ChunkIteratorInputStream}.
   */
  private static final class TranslatingInputStream extends InputStream {

    private final PreviewChunkStream delegate;

    TranslatingInputStream(PreviewChunkStream delegate) {
      this.delegate = delegate;
    }

    @Override
    public int read() throws IOException {
      try {
        return delegate.read();
      } catch (IOException e) {
        throw translate(e);
      }
    }

    @Override
    public int read(byte[] b, int off, int len) throws IOException {
      try {
        return delegate.read(b, off, len);
      } catch (IOException e) {
        throw translate(e);
      }
    }

    @Override
    public void close() {
      delegate.close();
    }

    private static IOException translate(IOException e) {
      Throwable cause = e.getCause();
      if (cause instanceof StatusException se) {
        return new IOException(new PreviewException(se.getStatus(), se));
      }
      // Plain protocol-violation string (no StatusException cause) — wrap as-is
      return e;
    }
  }
}
