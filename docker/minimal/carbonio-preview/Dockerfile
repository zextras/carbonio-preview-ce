# syntax=docker/dockerfile:1.4
# SPDX-FileCopyrightText: 2025 Zextras <https://www.zextras.com>
# SPDX-License-Identifier: AGPL-3.0-only
#
# Two-stage Go image for carbonio-preview-ce.
# Builder: golang:1.25-bookworm (amd64) + CGO cross-compile for arm64.
# Runtime: debian:bookworm-slim + apt libvips42 + libpdfium.so.
# Multi-arch: TARGETARCH drives pdfium download, CC selection, and Go cross-build.
#
# Cross-compilation strategy (faster than QEMU emulation):
#   amd64 → native gcc + amd64 pdfium
#   arm64 → gcc-aarch64-linux-gnu + arm64 pdfium + multiarch libvips-dev:arm64

# ---------------------------------------------------------------------------
# Stage 1: builder (always runs as amd64 native)
# ---------------------------------------------------------------------------
ARG BUILDPLATFORM
ARG TARGETARCH

FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.25-bookworm AS builder

ARG TARGETARCH

# Install build dependencies.
# crossbuild-essential-arm64 pulls in the full aarch64 toolchain (gcc, binutils,
# libc headers) in one shot — the standard Debian approach for CGO cross-compile.
# libvips-dev:arm64 provides arm64 C headers for govips via Debian multiarch.
# pkg-config-aarch64-linux-gnu provides aarch64-linux-gnu-pkg-config so that
# CGO (via PKG_CONFIG env) resolves arm64 library flags correctly.
RUN set -eux; \
    dpkg --add-architecture arm64; \
    apt-get update; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        gcc \
        crossbuild-essential-arm64 \
        pkg-config \
        pkg-config-aarch64-linux-gnu \
        libvips-dev \
        libvips-dev:arm64 \
        wget \
        ca-certificates; \
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

# Build the Go binary with CGO cross-compilation support.
# For arm64: use aarch64-linux-gnu-gcc as the C compiler (CC).
# GOARCH + GOOS make Go produce the correct target binary.
# CGO_LDFLAGS rpath: at runtime the binary looks for libpdfium.so in
# /usr/lib/carbonio-preview (same path the .deb package uses).
ENV CGO_ENABLED=1
ENV GOPRIVATE=github.com/zextras/*
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) \
        PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig" \
        CGO_LDFLAGS="-Wl,-rpath,/usr/lib/carbonio-preview -L/staging/pdfium/lib" \
        GOOS=linux GOARCH=amd64 \
        go build -ldflags="-s -w" -o /staging/carbonio-preview-bin ./cmd/carbonio-preview/... \
        ;; \
      arm64) \
        PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:/usr/lib/aarch64-linux-gnu/pkgconfig" \
        PKG_CONFIG=aarch64-linux-gnu-pkg-config \
        CC=aarch64-linux-gnu-gcc \
        CGO_LDFLAGS="-Wl,-rpath,/usr/lib/carbonio-preview -L/staging/pdfium/lib" \
        GOOS=linux GOARCH=arm64 \
        go build -ldflags="-s -w" -o /staging/carbonio-preview-bin ./cmd/carbonio-preview/... \
        ;; \
    esac

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
