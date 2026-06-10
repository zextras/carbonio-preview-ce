# SPDX-FileCopyrightText: 2025 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

FROM docker.io/library/alpine:3.23.2 AS prep
WORKDIR /staging

COPY ../../. .

RUN mkdir -p staging && \
    cp -r package staging/ && \
    cp -r cmd staging/ && \
    cp -r config staging/ && \
    cp -r render staging/ && \
    cp -r server staging/ && \
    cp -r storage staging/ && \
    cp go.mod staging/ && \
    cp go.sum staging/ && \
    { [ -f README.md ] && cp README.md staging/ || true; }

# Stage Ubuntu 20.04 (Focal)
FROM docker.io/m0rf30/yap-ubuntu-focal:1.44 AS ubuntu-focal-builder
WORKDIR /tmp/staging
COPY --from=prep /staging/staging .
RUN yap build ubuntu-focal /tmp/staging/package

# Stage Ubuntu 22.04 (Jammy)
FROM docker.io/m0rf30/yap-ubuntu-jammy:1.44 AS ubuntu-jammy-builder
WORKDIR /tmp/staging
COPY --from=prep /staging/staging .
RUN yap build ubuntu-jammy /tmp/staging/package

# Stage Ubuntu 24.04 (Noble)
FROM docker.io/m0rf30/yap-ubuntu-noble:1.44 AS ubuntu-noble-builder
WORKDIR /tmp/staging
COPY --from=prep /staging/staging .
RUN yap build ubuntu-noble /tmp/staging/package

# Stage RHEL/Rocky 8
FROM docker.io/m0rf30/yap-rocky-8:1.46 AS rocky-8-builder
WORKDIR /tmp/staging
COPY --from=prep /staging/staging .
RUN yap build rocky-8 /tmp/staging/package

# Stage RHEL/Rocky 9
FROM docker.io/m0rf30/yap-rocky-9:1.44 AS rocky-9-builder
WORKDIR /tmp/staging
COPY --from=prep /staging/staging .
RUN yap build rocky-9 /tmp/staging/package

# Final stage
FROM docker.io/library/alpine:3.23.2 AS final
WORKDIR /output

COPY --from=ubuntu-focal-builder /tmp/staging/artifacts ./ubuntu-focal/
COPY --from=ubuntu-jammy-builder /tmp/staging/artifacts ./ubuntu-jammy/
COPY --from=ubuntu-noble-builder /tmp/staging/artifacts ./ubuntu-noble/
COPY --from=rocky-8-builder /tmp/staging/artifacts ./rocky-8/
COPY --from=rocky-9-builder /tmp/staging/artifacts ./rocky-9/

CMD ["echo", "Packages built successfully."]