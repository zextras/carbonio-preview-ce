<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Run preview locally with Docker

This minimal setup includes all necessary dependencies without mocks (with Consul + Storages being the only exceptions).

Steps:
    1. `cd docker/minimal`
    2. `docker compose up --build`
    3. Browse Carbonio on `http://docker.carbonio.localhost`, backends are exposed on various ports (see docker-compose.yaml)
    4. Login using `user@carbonio.localhost`/`assext`

Possible configs for preview:
  - PREVIEW_HOST
  - PREVIEW_PORT
  - STORAGES_HOST
  - STORAGES_PORT
  - DOCS_EDITOR_HOST
  - DOCS_EDITOR_PORT