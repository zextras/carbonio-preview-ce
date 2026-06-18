# syntax=docker/dockerfile:1.25

# Stage 1: runs natively on BUILDPLATFORM (amd64 CI builder) — no QEMU.
# Cross-fetches the Python wheels and the libcairo2 runtime closure for
# TARGETARCH, then stages them so the final image needs zero RUN steps.
FROM --platform=$BUILDPLATFORM docker.io/library/python:3.10-slim AS builder

ARG TARGETARCH
ARG BUILDARCH=amd64

WORKDIR /build

COPY requirements.txt /tmp/requirements.txt

# Cross-install Python deps for the target arch into a staging tree.
# Wheels are only unpacked (no target code runs), so this is QEMU-free.
# Map Docker's TARGETARCH (amd64/arm64) to the manylinux wheel arch tag
# (x86_64/aarch64). Pure-python wheels are picked up via the implicit
# 'any' platform that pip always considers alongside --platform.
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) WHEELARCH=x86_64 ;; \
        arm64) WHEELARCH=aarch64 ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    pip install --no-cache-dir --no-compile \
        --target=/staging/opt/zextras/preview/site-packages \
        --platform manylinux2014_${WHEELARCH} \
        --platform manylinux_2_17_${WHEELARCH} \
        --platform manylinux_2_28_${WHEELARCH} \
        --python-version 310 --implementation cp --abi cp310 \
        --only-binary=:all: \
        -r /tmp/requirements.txt

# Cross-fetch libcairo2 + its runtime .so closure for the target arch using
# apt-get download + dpkg -x (no postinst, no target execution).
RUN set -eux; \
    if [ "${TARGETARCH}" != "${BUILDARCH}" ]; then \
        dpkg --add-architecture ${TARGETARCH}; \
    fi; \
    apt-get update; \
    mkdir -p /tmp/debs /staging/usr/local/lib; \
    cd /tmp/debs; \
    apt-get download \
        libcairo2:${TARGETARCH} \
        libpixman-1-0:${TARGETARCH} \
        libfontconfig1:${TARGETARCH} \
        libfreetype6:${TARGETARCH} \
        libpng16-16t64:${TARGETARCH} \
        libxcb-render0:${TARGETARCH} \
        libxcb-shm0:${TARGETARCH} \
        libxcb1:${TARGETARCH} \
        libxext6:${TARGETARCH} \
        libxrender1:${TARGETARCH} \
        libx11-6:${TARGETARCH} \
        libxau6:${TARGETARCH} \
        libxdmcp6:${TARGETARCH} \
        libbrotli1:${TARGETARCH} \
        libexpat1:${TARGETARCH}; \
    for deb in /tmp/debs/*.deb; do dpkg -x "$deb" /staging/cairo-root; done; \
    find /staging/cairo-root -name '*.so*' -exec cp -Pv {} /staging/usr/local/lib/ \; ; \
    rm -rf /tmp/debs /staging/cairo-root /var/lib/apt/lists/*

COPY app /staging/opt/zextras/preview/app
COPY package/preview/messages.ini /staging/etc/carbonio/preview/messages.ini

# Stage 2: final image — TARGETPLATFORM, zero RUN steps (no QEMU needed).
FROM --platform=$TARGETPLATFORM docker.io/library/python:3.10-slim

COPY --from=builder /staging/opt/zextras/preview /opt/zextras/preview
COPY --from=builder /staging/etc/carbonio/preview /etc/carbonio/preview
COPY --from=builder /staging/usr/local/lib /usr/local/lib

ENV LD_LIBRARY_PATH="/usr/local/lib"
ENV PYTHONPATH="/opt/zextras/preview/app:/opt/zextras/preview/site-packages:/opt/zextras/common/lib/python3.10/dist-packages:/opt/zextras/common/lib/python3.10/site-packages:/opt/zextras/common/lib64/python3.10/dist-packages:/opt/zextras/common/lib64/python3.10/site-packages:/opt/zextras/common/local/lib/python3.10/dist-packages:/opt/zextras/common/local/lib/python3.10/site-packages:/opt/zextras/common/local/lib64/python3.10/dist-packages:/opt/zextras/common/local/lib64/python3.10/site-packages:"

WORKDIR /opt/zextras/preview

CMD sh -c 'echo "# Carbonio Preview service configuration\n\
[carbonio.preview]\n\
name = preview\n\
default_host = ${PREVIEW_HOST}\n\
default_port = ${PREVIEW_PORT}\n\
timeout_in_seconds = 30\n\
docs-timeout = 15\n\
workers = 2\n\
image_name = image\n\
health_name = health\n\
pdf_name = pdf\n\
document_name = document\n\
enable_document_preview = true\n\
enable_document_thumbnail = false\n\
\n\
# Log configuration\n\
[log]\n\
format = [%(asctime)s] %(levelname)s [%(name)s.%(funcName)s:%(lineno)d] %(message)s\n\
level = warning\n\
path = /var/log/carbonio/preview/\n\
\n\
# Image constants\n\
[image_constants]\n\
minimum_resolution = 80\n\
\n\
# Carbonio Storages configuration\n\
[carbonio.storages]\n\
name = slimstore\n\
download_api = download\n\
health_check = health/live\n\
default_protocol = http\n\
default_host = ${STORAGES_HOST}\n\
default_port = ${STORAGES_PORT}\n\
\n\
# Carbonio Docs-Editor configuration\n\
[carbonio.docs-editor]\n\
default_protocol = http\n\
default_host = ${DOCS_EDITOR_HOST}\n\
default_port = ${DOCS_EDITOR_PORT}\n\
service_endpoint = services/docs/editor\n\
convert_api = cool/convert-to" > /etc/carbonio/preview/config.ini && \
mkdir -p /var/log/carbonio/preview/ && \
python -m gunicorn app.controller:app --config app/gunicorn.conf.py'
