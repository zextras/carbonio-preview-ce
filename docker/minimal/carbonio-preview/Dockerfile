# syntax=docker/dockerfile:1.4
# SPDX-FileCopyrightText: 2025 Zextras <https://www.zextras.com>
# SPDX-License-Identifier: AGPL-3.0-only
#
# Two-stage Go image for carbonio-preview-ce.
# Builder: golang:1.25-bookworm running on TARGETPLATFORM (native per arch).
# Runtime: debian:bookworm-slim + apt libvips42 + libpdfium.so.
# Multi-arch: TARGETARCH drives the pdfium download; the builder runs natively
# under QEMU for arm64 so CGO links against the correct arch libraries.

# ---------------------------------------------------------------------------
# Stage 1: builder (runs natively on each target platform via QEMU for arm64)
# ---------------------------------------------------------------------------
ARG TARGETPLATFORM
ARG TARGETARCH

FROM --platform=${TARGETPLATFORM} golang:1.25-bookworm AS builder

ARG TARGETARCH

# Install build dependencies.
# libvips-dev brings vips.pc + all CGO headers needed by render/vips.go.
# gcc + pkg-config are required by CGO.
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        gcc \
        pkg-config \
        libvips-dev \
        wget \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Download pdfium for the TARGET architecture.
# bblanchon release naming: x64 for amd64, arm64 for arm64.
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
        amd64) PDFARCH=x64 ;; \
        arm64) PDFARCH=arm64 ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /staging/pdfium; \
    wget -q \
      "https://github.com/bblanchon/pdfium-binaries/releases/download/chromium%2F7881/pdfium-linux-${PDFARCH}.tgz" \
      -O /tmp/pdfium.tgz; \
    tar -xzf /tmp/pdfium.tgz -C /staging/pdfium; \
    rm /tmp/pdfium.tgz

# Synthesize pdfium.pc so that go-pdfium CGO can find the library.
RUN mkdir -p /usr/local/lib/pkgconfig && \
    printf 'prefix=/staging/pdfium\nlibdir=/staging/pdfium/lib\nincludedir=/staging/pdfium/include\nName: PDFium\nDescription: PDFium library\nVersion: 7881\nLibs: -L/staging/pdfium/lib -lpdfium\nCflags: -I/staging/pdfium/include\n' \
        > /usr/local/lib/pkgconfig/pdfium.pc

WORKDIR /src

# Copy Go module files first so the layer is cached unless deps change.
COPY go.mod go.sum ./

# Download all modules before copying source.
ENV GOPRIVATE=github.com/zextras/*
ENV GONOSUMCHECK=github.com/zextras/*
RUN go mod download

# Copy source tree.
COPY cmd/    ./cmd/
COPY config/ ./config/
COPY render/ ./render/
COPY server/ ./server/
COPY storage/ ./storage/

# Build the Go binary.
# CGO_LDFLAGS rpath: at runtime the binary looks for libpdfium.so in
# /usr/lib/carbonio-preview (same path the .deb package uses).
ENV CGO_ENABLED=1
ENV GOPRIVATE=github.com/zextras/*
ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/aarch64-linux-gnu/pkgconfig
ENV CGO_LDFLAGS="-Wl,-rpath,/usr/lib/carbonio-preview -L/staging/pdfium/lib"
RUN go build \
      -ldflags="-s -w" \
      -o /staging/carbonio-preview-bin \
      ./cmd/carbonio-preview/...

# ---------------------------------------------------------------------------
# Stage 2: runtime
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

# Install runtime: libvips42 pulls in the full vips + glib runtime closure.
# libstdc++6 is needed for C++ symbols from pdfium and vips-cpp.
# ca-certificates is needed for any outbound HTTPS the binary makes.
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        libvips42 \
        libstdc++6 \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Copy the compiled binary.
COPY --from=builder /staging/carbonio-preview-bin /usr/bin/carbonio-preview-bin

# Copy libpdfium.so to the rpath directory.
COPY --from=builder /staging/pdfium/lib/libpdfium.so \
                    /usr/lib/carbonio-preview/libpdfium.so

# ldconfig so the dynamic linker picks up /usr/lib/carbonio-preview.
RUN ldconfig

# Create directories expected by the service.
RUN mkdir -p /etc/carbonio/preview /var/log/carbonio/preview

# The service binds on 0.0.0.0:10000 by default (overridable via PREVIEW_HOST/PREVIEW_PORT).
EXPOSE 10000

# Run as root (matching legacy behaviour; no USER added).
WORKDIR /opt/zextras/preview

ENTRYPOINT ["/usr/bin/carbonio-preview-bin"]
