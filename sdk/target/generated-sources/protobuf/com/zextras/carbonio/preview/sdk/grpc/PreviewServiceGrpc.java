package com.zextras.carbonio.preview.sdk.grpc;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 */
@io.grpc.stub.annotations.GrpcGenerated
public final class PreviewServiceGrpc {

  private PreviewServiceGrpc() {}

  public static final java.lang.String SERVICE_NAME = "preview.PreviewService";

  // Static method descriptors that strictly reflect the proto.
  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetImagePreviewMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetImagePreview",
      requestType = com.zextras.carbonio.preview.sdk.grpc.GetRequest.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetImagePreviewMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetImagePreviewMethod;
    if ((getGetImagePreviewMethod = PreviewServiceGrpc.getGetImagePreviewMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getGetImagePreviewMethod = PreviewServiceGrpc.getGetImagePreviewMethod) == null) {
          PreviewServiceGrpc.getGetImagePreviewMethod = getGetImagePreviewMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetImagePreview"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.GetRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("GetImagePreview"))
              .build();
        }
      }
    }
    return getGetImagePreviewMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetImageThumbnailMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetImageThumbnail",
      requestType = com.zextras.carbonio.preview.sdk.grpc.GetRequest.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetImageThumbnailMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetImageThumbnailMethod;
    if ((getGetImageThumbnailMethod = PreviewServiceGrpc.getGetImageThumbnailMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getGetImageThumbnailMethod = PreviewServiceGrpc.getGetImageThumbnailMethod) == null) {
          PreviewServiceGrpc.getGetImageThumbnailMethod = getGetImageThumbnailMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetImageThumbnail"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.GetRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("GetImageThumbnail"))
              .build();
        }
      }
    }
    return getGetImageThumbnailMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetPdfPreviewMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetPdfPreview",
      requestType = com.zextras.carbonio.preview.sdk.grpc.GetRequest.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetPdfPreviewMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetPdfPreviewMethod;
    if ((getGetPdfPreviewMethod = PreviewServiceGrpc.getGetPdfPreviewMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getGetPdfPreviewMethod = PreviewServiceGrpc.getGetPdfPreviewMethod) == null) {
          PreviewServiceGrpc.getGetPdfPreviewMethod = getGetPdfPreviewMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetPdfPreview"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.GetRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("GetPdfPreview"))
              .build();
        }
      }
    }
    return getGetPdfPreviewMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetPdfThumbnailMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetPdfThumbnail",
      requestType = com.zextras.carbonio.preview.sdk.grpc.GetRequest.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetPdfThumbnailMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetPdfThumbnailMethod;
    if ((getGetPdfThumbnailMethod = PreviewServiceGrpc.getGetPdfThumbnailMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getGetPdfThumbnailMethod = PreviewServiceGrpc.getGetPdfThumbnailMethod) == null) {
          PreviewServiceGrpc.getGetPdfThumbnailMethod = getGetPdfThumbnailMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetPdfThumbnail"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.GetRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("GetPdfThumbnail"))
              .build();
        }
      }
    }
    return getGetPdfThumbnailMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetDocumentPreviewMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetDocumentPreview",
      requestType = com.zextras.carbonio.preview.sdk.grpc.GetRequest.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetDocumentPreviewMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetDocumentPreviewMethod;
    if ((getGetDocumentPreviewMethod = PreviewServiceGrpc.getGetDocumentPreviewMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getGetDocumentPreviewMethod = PreviewServiceGrpc.getGetDocumentPreviewMethod) == null) {
          PreviewServiceGrpc.getGetDocumentPreviewMethod = getGetDocumentPreviewMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetDocumentPreview"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.GetRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("GetDocumentPreview"))
              .build();
        }
      }
    }
    return getGetDocumentPreviewMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetDocumentThumbnailMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetDocumentThumbnail",
      requestType = com.zextras.carbonio.preview.sdk.grpc.GetRequest.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetDocumentThumbnailMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getGetDocumentThumbnailMethod;
    if ((getGetDocumentThumbnailMethod = PreviewServiceGrpc.getGetDocumentThumbnailMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getGetDocumentThumbnailMethod = PreviewServiceGrpc.getGetDocumentThumbnailMethod) == null) {
          PreviewServiceGrpc.getGetDocumentThumbnailMethod = getGetDocumentThumbnailMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.GetRequest, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.SERVER_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetDocumentThumbnail"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.GetRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("GetDocumentThumbnail"))
              .build();
        }
      }
    }
    return getGetDocumentThumbnailMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostImagePreviewMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "PostImagePreview",
      requestType = com.zextras.carbonio.preview.sdk.grpc.UploadChunk.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostImagePreviewMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostImagePreviewMethod;
    if ((getPostImagePreviewMethod = PreviewServiceGrpc.getPostImagePreviewMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getPostImagePreviewMethod = PreviewServiceGrpc.getPostImagePreviewMethod) == null) {
          PreviewServiceGrpc.getPostImagePreviewMethod = getPostImagePreviewMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "PostImagePreview"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.UploadChunk.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("PostImagePreview"))
              .build();
        }
      }
    }
    return getPostImagePreviewMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostImageThumbnailMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "PostImageThumbnail",
      requestType = com.zextras.carbonio.preview.sdk.grpc.UploadChunk.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostImageThumbnailMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostImageThumbnailMethod;
    if ((getPostImageThumbnailMethod = PreviewServiceGrpc.getPostImageThumbnailMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getPostImageThumbnailMethod = PreviewServiceGrpc.getPostImageThumbnailMethod) == null) {
          PreviewServiceGrpc.getPostImageThumbnailMethod = getPostImageThumbnailMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "PostImageThumbnail"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.UploadChunk.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("PostImageThumbnail"))
              .build();
        }
      }
    }
    return getPostImageThumbnailMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostPdfPreviewMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "PostPdfPreview",
      requestType = com.zextras.carbonio.preview.sdk.grpc.UploadChunk.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostPdfPreviewMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostPdfPreviewMethod;
    if ((getPostPdfPreviewMethod = PreviewServiceGrpc.getPostPdfPreviewMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getPostPdfPreviewMethod = PreviewServiceGrpc.getPostPdfPreviewMethod) == null) {
          PreviewServiceGrpc.getPostPdfPreviewMethod = getPostPdfPreviewMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "PostPdfPreview"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.UploadChunk.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("PostPdfPreview"))
              .build();
        }
      }
    }
    return getPostPdfPreviewMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostPdfThumbnailMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "PostPdfThumbnail",
      requestType = com.zextras.carbonio.preview.sdk.grpc.UploadChunk.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostPdfThumbnailMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostPdfThumbnailMethod;
    if ((getPostPdfThumbnailMethod = PreviewServiceGrpc.getPostPdfThumbnailMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getPostPdfThumbnailMethod = PreviewServiceGrpc.getPostPdfThumbnailMethod) == null) {
          PreviewServiceGrpc.getPostPdfThumbnailMethod = getPostPdfThumbnailMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "PostPdfThumbnail"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.UploadChunk.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("PostPdfThumbnail"))
              .build();
        }
      }
    }
    return getPostPdfThumbnailMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostDocumentPreviewMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "PostDocumentPreview",
      requestType = com.zextras.carbonio.preview.sdk.grpc.UploadChunk.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostDocumentPreviewMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostDocumentPreviewMethod;
    if ((getPostDocumentPreviewMethod = PreviewServiceGrpc.getPostDocumentPreviewMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getPostDocumentPreviewMethod = PreviewServiceGrpc.getPostDocumentPreviewMethod) == null) {
          PreviewServiceGrpc.getPostDocumentPreviewMethod = getPostDocumentPreviewMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "PostDocumentPreview"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.UploadChunk.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("PostDocumentPreview"))
              .build();
        }
      }
    }
    return getPostDocumentPreviewMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostDocumentThumbnailMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "PostDocumentThumbnail",
      requestType = com.zextras.carbonio.preview.sdk.grpc.UploadChunk.class,
      responseType = com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
      com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostDocumentThumbnailMethod() {
    io.grpc.MethodDescriptor<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPostDocumentThumbnailMethod;
    if ((getPostDocumentThumbnailMethod = PreviewServiceGrpc.getPostDocumentThumbnailMethod) == null) {
      synchronized (PreviewServiceGrpc.class) {
        if ((getPostDocumentThumbnailMethod = PreviewServiceGrpc.getPostDocumentThumbnailMethod) == null) {
          PreviewServiceGrpc.getPostDocumentThumbnailMethod = getPostDocumentThumbnailMethod =
              io.grpc.MethodDescriptor.<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "PostDocumentThumbnail"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.UploadChunk.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.zextras.carbonio.preview.sdk.grpc.PreviewChunk.getDefaultInstance()))
              .setSchemaDescriptor(new PreviewServiceMethodDescriptorSupplier("PostDocumentThumbnail"))
              .build();
        }
      }
    }
    return getPostDocumentThumbnailMethod;
  }

  /**
   * Creates a new async stub that supports all call types for the service
   */
  public static PreviewServiceStub newStub(io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<PreviewServiceStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<PreviewServiceStub>() {
        @java.lang.Override
        public PreviewServiceStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new PreviewServiceStub(channel, callOptions);
        }
      };
    return PreviewServiceStub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports all types of calls on the service
   */
  public static PreviewServiceBlockingV2Stub newBlockingV2Stub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<PreviewServiceBlockingV2Stub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<PreviewServiceBlockingV2Stub>() {
        @java.lang.Override
        public PreviewServiceBlockingV2Stub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new PreviewServiceBlockingV2Stub(channel, callOptions);
        }
      };
    return PreviewServiceBlockingV2Stub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports unary and streaming output calls on the service
   */
  public static PreviewServiceBlockingStub newBlockingStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<PreviewServiceBlockingStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<PreviewServiceBlockingStub>() {
        @java.lang.Override
        public PreviewServiceBlockingStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new PreviewServiceBlockingStub(channel, callOptions);
        }
      };
    return PreviewServiceBlockingStub.newStub(factory, channel);
  }

  /**
   * Creates a new ListenableFuture-style stub that supports unary calls on the service
   */
  public static PreviewServiceFutureStub newFutureStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<PreviewServiceFutureStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<PreviewServiceFutureStub>() {
        @java.lang.Override
        public PreviewServiceFutureStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new PreviewServiceFutureStub(channel, callOptions);
        }
      };
    return PreviewServiceFutureStub.newStub(factory, channel);
  }

  /**
   */
  public interface AsyncService {

    /**
     * <pre>
     * ---- Downloads: server-streaming, metadata ALWAYS first ----
     * </pre>
     */
    default void getImagePreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetImagePreviewMethod(), responseObserver);
    }

    /**
     */
    default void getImageThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetImageThumbnailMethod(), responseObserver);
    }

    /**
     */
    default void getPdfPreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetPdfPreviewMethod(), responseObserver);
    }

    /**
     */
    default void getPdfThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetPdfThumbnailMethod(), responseObserver);
    }

    /**
     */
    default void getDocumentPreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetDocumentPreviewMethod(), responseObserver);
    }

    /**
     */
    default void getDocumentThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetDocumentThumbnailMethod(), responseObserver);
    }

    /**
     * <pre>
     * ---- Uploads: client-streaming in, server-streaming out (mailbox only) ----
     * </pre>
     */
    default io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postImagePreview(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getPostImagePreviewMethod(), responseObserver);
    }

    /**
     */
    default io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postImageThumbnail(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getPostImageThumbnailMethod(), responseObserver);
    }

    /**
     */
    default io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postPdfPreview(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getPostPdfPreviewMethod(), responseObserver);
    }

    /**
     */
    default io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postPdfThumbnail(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getPostPdfThumbnailMethod(), responseObserver);
    }

    /**
     */
    default io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postDocumentPreview(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getPostDocumentPreviewMethod(), responseObserver);
    }

    /**
     */
    default io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postDocumentThumbnail(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getPostDocumentThumbnailMethod(), responseObserver);
    }
  }

  /**
   * Base class for the server implementation of the service PreviewService.
   */
  public static abstract class PreviewServiceImplBase
      implements io.grpc.BindableService, AsyncService {

    @java.lang.Override public final io.grpc.ServerServiceDefinition bindService() {
      return PreviewServiceGrpc.bindService(this);
    }
  }

  /**
   * A stub to allow clients to do asynchronous rpc calls to service PreviewService.
   */
  public static final class PreviewServiceStub
      extends io.grpc.stub.AbstractAsyncStub<PreviewServiceStub> {
    private PreviewServiceStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected PreviewServiceStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new PreviewServiceStub(channel, callOptions);
    }

    /**
     * <pre>
     * ---- Downloads: server-streaming, metadata ALWAYS first ----
     * </pre>
     */
    public void getImagePreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ClientCalls.asyncServerStreamingCall(
          getChannel().newCall(getGetImagePreviewMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void getImageThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ClientCalls.asyncServerStreamingCall(
          getChannel().newCall(getGetImageThumbnailMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void getPdfPreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ClientCalls.asyncServerStreamingCall(
          getChannel().newCall(getGetPdfPreviewMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void getPdfThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ClientCalls.asyncServerStreamingCall(
          getChannel().newCall(getGetPdfThumbnailMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void getDocumentPreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ClientCalls.asyncServerStreamingCall(
          getChannel().newCall(getGetDocumentPreviewMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void getDocumentThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request,
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      io.grpc.stub.ClientCalls.asyncServerStreamingCall(
          getChannel().newCall(getGetDocumentThumbnailMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     * <pre>
     * ---- Uploads: client-streaming in, server-streaming out (mailbox only) ----
     * </pre>
     */
    public io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postImagePreview(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getPostImagePreviewMethod(), getCallOptions()), responseObserver);
    }

    /**
     */
    public io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postImageThumbnail(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getPostImageThumbnailMethod(), getCallOptions()), responseObserver);
    }

    /**
     */
    public io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postPdfPreview(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getPostPdfPreviewMethod(), getCallOptions()), responseObserver);
    }

    /**
     */
    public io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postPdfThumbnail(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getPostPdfThumbnailMethod(), getCallOptions()), responseObserver);
    }

    /**
     */
    public io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postDocumentPreview(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getPostDocumentPreviewMethod(), getCallOptions()), responseObserver);
    }

    /**
     */
    public io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.UploadChunk> postDocumentThumbnail(
        io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getPostDocumentThumbnailMethod(), getCallOptions()), responseObserver);
    }
  }

  /**
   * A stub to allow clients to do synchronous rpc calls to service PreviewService.
   */
  public static final class PreviewServiceBlockingV2Stub
      extends io.grpc.stub.AbstractBlockingStub<PreviewServiceBlockingV2Stub> {
    private PreviewServiceBlockingV2Stub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected PreviewServiceBlockingV2Stub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new PreviewServiceBlockingV2Stub(channel, callOptions);
    }

    /**
     * <pre>
     * ---- Downloads: server-streaming, metadata ALWAYS first ----
     * </pre>
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<?, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        getImagePreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingV2ServerStreamingCall(
          getChannel(), getGetImagePreviewMethod(), getCallOptions(), request);
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<?, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        getImageThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingV2ServerStreamingCall(
          getChannel(), getGetImageThumbnailMethod(), getCallOptions(), request);
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<?, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        getPdfPreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingV2ServerStreamingCall(
          getChannel(), getGetPdfPreviewMethod(), getCallOptions(), request);
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<?, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        getPdfThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingV2ServerStreamingCall(
          getChannel(), getGetPdfThumbnailMethod(), getCallOptions(), request);
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<?, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        getDocumentPreview(com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingV2ServerStreamingCall(
          getChannel(), getGetDocumentPreviewMethod(), getCallOptions(), request);
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<?, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        getDocumentThumbnail(com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingV2ServerStreamingCall(
          getChannel(), getGetDocumentThumbnailMethod(), getCallOptions(), request);
    }

    /**
     * <pre>
     * ---- Uploads: client-streaming in, server-streaming out (mailbox only) ----
     * </pre>
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        postImagePreview() {
      return io.grpc.stub.ClientCalls.blockingBidiStreamingCall(
          getChannel(), getPostImagePreviewMethod(), getCallOptions());
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        postImageThumbnail() {
      return io.grpc.stub.ClientCalls.blockingBidiStreamingCall(
          getChannel(), getPostImageThumbnailMethod(), getCallOptions());
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        postPdfPreview() {
      return io.grpc.stub.ClientCalls.blockingBidiStreamingCall(
          getChannel(), getPostPdfPreviewMethod(), getCallOptions());
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        postPdfThumbnail() {
      return io.grpc.stub.ClientCalls.blockingBidiStreamingCall(
          getChannel(), getPostPdfThumbnailMethod(), getCallOptions());
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        postDocumentPreview() {
      return io.grpc.stub.ClientCalls.blockingBidiStreamingCall(
          getChannel(), getPostDocumentPreviewMethod(), getCallOptions());
    }

    /**
     */
    @io.grpc.ExperimentalApi("https://github.com/grpc/grpc-java/issues/10918")
    public io.grpc.stub.BlockingClientCall<com.zextras.carbonio.preview.sdk.grpc.UploadChunk, com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>
        postDocumentThumbnail() {
      return io.grpc.stub.ClientCalls.blockingBidiStreamingCall(
          getChannel(), getPostDocumentThumbnailMethod(), getCallOptions());
    }
  }

  /**
   * A stub to allow clients to do limited synchronous rpc calls to service PreviewService.
   */
  public static final class PreviewServiceBlockingStub
      extends io.grpc.stub.AbstractBlockingStub<PreviewServiceBlockingStub> {
    private PreviewServiceBlockingStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected PreviewServiceBlockingStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new PreviewServiceBlockingStub(channel, callOptions);
    }

    /**
     * <pre>
     * ---- Downloads: server-streaming, metadata ALWAYS first ----
     * </pre>
     */
    public java.util.Iterator<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getImagePreview(
        com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingServerStreamingCall(
          getChannel(), getGetImagePreviewMethod(), getCallOptions(), request);
    }

    /**
     */
    public java.util.Iterator<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getImageThumbnail(
        com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingServerStreamingCall(
          getChannel(), getGetImageThumbnailMethod(), getCallOptions(), request);
    }

    /**
     */
    public java.util.Iterator<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPdfPreview(
        com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingServerStreamingCall(
          getChannel(), getGetPdfPreviewMethod(), getCallOptions(), request);
    }

    /**
     */
    public java.util.Iterator<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getPdfThumbnail(
        com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingServerStreamingCall(
          getChannel(), getGetPdfThumbnailMethod(), getCallOptions(), request);
    }

    /**
     */
    public java.util.Iterator<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getDocumentPreview(
        com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingServerStreamingCall(
          getChannel(), getGetDocumentPreviewMethod(), getCallOptions(), request);
    }

    /**
     */
    public java.util.Iterator<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk> getDocumentThumbnail(
        com.zextras.carbonio.preview.sdk.grpc.GetRequest request) {
      return io.grpc.stub.ClientCalls.blockingServerStreamingCall(
          getChannel(), getGetDocumentThumbnailMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do ListenableFuture-style rpc calls to service PreviewService.
   */
  public static final class PreviewServiceFutureStub
      extends io.grpc.stub.AbstractFutureStub<PreviewServiceFutureStub> {
    private PreviewServiceFutureStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected PreviewServiceFutureStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new PreviewServiceFutureStub(channel, callOptions);
    }
  }

  private static final int METHODID_GET_IMAGE_PREVIEW = 0;
  private static final int METHODID_GET_IMAGE_THUMBNAIL = 1;
  private static final int METHODID_GET_PDF_PREVIEW = 2;
  private static final int METHODID_GET_PDF_THUMBNAIL = 3;
  private static final int METHODID_GET_DOCUMENT_PREVIEW = 4;
  private static final int METHODID_GET_DOCUMENT_THUMBNAIL = 5;
  private static final int METHODID_POST_IMAGE_PREVIEW = 6;
  private static final int METHODID_POST_IMAGE_THUMBNAIL = 7;
  private static final int METHODID_POST_PDF_PREVIEW = 8;
  private static final int METHODID_POST_PDF_THUMBNAIL = 9;
  private static final int METHODID_POST_DOCUMENT_PREVIEW = 10;
  private static final int METHODID_POST_DOCUMENT_THUMBNAIL = 11;

  private static final class MethodHandlers<Req, Resp> implements
      io.grpc.stub.ServerCalls.UnaryMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ServerStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ClientStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.BidiStreamingMethod<Req, Resp> {
    private final AsyncService serviceImpl;
    private final int methodId;

    MethodHandlers(AsyncService serviceImpl, int methodId) {
      this.serviceImpl = serviceImpl;
      this.methodId = methodId;
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public void invoke(Req request, io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        case METHODID_GET_IMAGE_PREVIEW:
          serviceImpl.getImagePreview((com.zextras.carbonio.preview.sdk.grpc.GetRequest) request,
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
          break;
        case METHODID_GET_IMAGE_THUMBNAIL:
          serviceImpl.getImageThumbnail((com.zextras.carbonio.preview.sdk.grpc.GetRequest) request,
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
          break;
        case METHODID_GET_PDF_PREVIEW:
          serviceImpl.getPdfPreview((com.zextras.carbonio.preview.sdk.grpc.GetRequest) request,
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
          break;
        case METHODID_GET_PDF_THUMBNAIL:
          serviceImpl.getPdfThumbnail((com.zextras.carbonio.preview.sdk.grpc.GetRequest) request,
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
          break;
        case METHODID_GET_DOCUMENT_PREVIEW:
          serviceImpl.getDocumentPreview((com.zextras.carbonio.preview.sdk.grpc.GetRequest) request,
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
          break;
        case METHODID_GET_DOCUMENT_THUMBNAIL:
          serviceImpl.getDocumentThumbnail((com.zextras.carbonio.preview.sdk.grpc.GetRequest) request,
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
          break;
        default:
          throw new AssertionError();
      }
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public io.grpc.stub.StreamObserver<Req> invoke(
        io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        case METHODID_POST_IMAGE_PREVIEW:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.postImagePreview(
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
        case METHODID_POST_IMAGE_THUMBNAIL:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.postImageThumbnail(
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
        case METHODID_POST_PDF_PREVIEW:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.postPdfPreview(
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
        case METHODID_POST_PDF_THUMBNAIL:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.postPdfThumbnail(
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
        case METHODID_POST_DOCUMENT_PREVIEW:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.postDocumentPreview(
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
        case METHODID_POST_DOCUMENT_THUMBNAIL:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.postDocumentThumbnail(
              (io.grpc.stub.StreamObserver<com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>) responseObserver);
        default:
          throw new AssertionError();
      }
    }
  }

  public static final io.grpc.ServerServiceDefinition bindService(AsyncService service) {
    return io.grpc.ServerServiceDefinition.builder(getServiceDescriptor())
        .addMethod(
          getGetImagePreviewMethod(),
          io.grpc.stub.ServerCalls.asyncServerStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.GetRequest,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_GET_IMAGE_PREVIEW)))
        .addMethod(
          getGetImageThumbnailMethod(),
          io.grpc.stub.ServerCalls.asyncServerStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.GetRequest,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_GET_IMAGE_THUMBNAIL)))
        .addMethod(
          getGetPdfPreviewMethod(),
          io.grpc.stub.ServerCalls.asyncServerStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.GetRequest,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_GET_PDF_PREVIEW)))
        .addMethod(
          getGetPdfThumbnailMethod(),
          io.grpc.stub.ServerCalls.asyncServerStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.GetRequest,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_GET_PDF_THUMBNAIL)))
        .addMethod(
          getGetDocumentPreviewMethod(),
          io.grpc.stub.ServerCalls.asyncServerStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.GetRequest,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_GET_DOCUMENT_PREVIEW)))
        .addMethod(
          getGetDocumentThumbnailMethod(),
          io.grpc.stub.ServerCalls.asyncServerStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.GetRequest,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_GET_DOCUMENT_THUMBNAIL)))
        .addMethod(
          getPostImagePreviewMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_POST_IMAGE_PREVIEW)))
        .addMethod(
          getPostImageThumbnailMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_POST_IMAGE_THUMBNAIL)))
        .addMethod(
          getPostPdfPreviewMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_POST_PDF_PREVIEW)))
        .addMethod(
          getPostPdfThumbnailMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_POST_PDF_THUMBNAIL)))
        .addMethod(
          getPostDocumentPreviewMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_POST_DOCUMENT_PREVIEW)))
        .addMethod(
          getPostDocumentThumbnailMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.zextras.carbonio.preview.sdk.grpc.UploadChunk,
              com.zextras.carbonio.preview.sdk.grpc.PreviewChunk>(
                service, METHODID_POST_DOCUMENT_THUMBNAIL)))
        .build();
  }

  private static abstract class PreviewServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoFileDescriptorSupplier, io.grpc.protobuf.ProtoServiceDescriptorSupplier {
    PreviewServiceBaseDescriptorSupplier() {}

    @java.lang.Override
    public com.google.protobuf.Descriptors.FileDescriptor getFileDescriptor() {
      return com.zextras.carbonio.preview.sdk.grpc.Preview.getDescriptor();
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.ServiceDescriptor getServiceDescriptor() {
      return getFileDescriptor().findServiceByName("PreviewService");
    }
  }

  private static final class PreviewServiceFileDescriptorSupplier
      extends PreviewServiceBaseDescriptorSupplier {
    PreviewServiceFileDescriptorSupplier() {}
  }

  private static final class PreviewServiceMethodDescriptorSupplier
      extends PreviewServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoMethodDescriptorSupplier {
    private final java.lang.String methodName;

    PreviewServiceMethodDescriptorSupplier(java.lang.String methodName) {
      this.methodName = methodName;
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.MethodDescriptor getMethodDescriptor() {
      return getServiceDescriptor().findMethodByName(methodName);
    }
  }

  private static volatile io.grpc.ServiceDescriptor serviceDescriptor;

  public static io.grpc.ServiceDescriptor getServiceDescriptor() {
    io.grpc.ServiceDescriptor result = serviceDescriptor;
    if (result == null) {
      synchronized (PreviewServiceGrpc.class) {
        result = serviceDescriptor;
        if (result == null) {
          serviceDescriptor = result = io.grpc.ServiceDescriptor.newBuilder(SERVICE_NAME)
              .setSchemaDescriptor(new PreviewServiceFileDescriptorSupplier())
              .addMethod(getGetImagePreviewMethod())
              .addMethod(getGetImageThumbnailMethod())
              .addMethod(getGetPdfPreviewMethod())
              .addMethod(getGetPdfThumbnailMethod())
              .addMethod(getGetDocumentPreviewMethod())
              .addMethod(getGetDocumentThumbnailMethod())
              .addMethod(getPostImagePreviewMethod())
              .addMethod(getPostImageThumbnailMethod())
              .addMethod(getPostPdfPreviewMethod())
              .addMethod(getPostPdfThumbnailMethod())
              .addMethod(getPostDocumentPreviewMethod())
              .addMethod(getPostDocumentThumbnailMethod())
              .build();
        }
      }
    }
    return result;
  }
}
