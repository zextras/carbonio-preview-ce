<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Default Configuration

## Networking Config

Overridable by `/etc/carbonio/preview/config.properties`

| Key | Default |
| --- | ------- |
| `carbonio.docs-editor.host` | `127.78.0.6` |
| `carbonio.docs-editor.port` | `20001` |
| `carbonio.docs-editor.protocol` | `http` |
| `carbonio.postgresql.host` | `127.78.0.6` |
| `carbonio.postgresql.port` | `20000` |
| `carbonio.service-discover.host` | `127.0.0.1` |
| `carbonio.service-discover.port` | `8500` |
| `carbonio.service.host` | `127.78.0.6` |
| `carbonio.service.port` | `10000` |
| `carbonio.storages.host` | `127.78.0.6` |
| `carbonio.storages.port` | `20000` |
| `carbonio.storages.protocol` | `http` |

## Application Config

Overridable by Consul KV

| Key | Default | If not set |
| --- | ------- | ---------- |
| `carbonio-preview/cache-max-mb` | `256` |  |
| `carbonio-preview/db-conn-max-lifetime-seconds` | `600` |  |
| `carbonio-preview/db-pool-max-conns` | `10` |  |
| `carbonio-preview/db-pool-min-conns` | `2` |  |
| `carbonio-preview/docs-timeout-in-seconds` | `15` |  |
| `carbonio-preview/enable-document-preview` | `true` |  |
| `carbonio-preview/enable-document-thumbnail` | `false` |  |
| `carbonio-preview/pdf-workers` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/render-concurrency` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/timeout-in-seconds` | `30` |  |
| `carbonio-preview/video-concurrency` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/video-max-attempts` | `3` |  |
| `carbonio-preview/video-stale-ttl-seconds` | `900` |  |
| `carbonio-preview/video-sweep-interval-seconds` | `15` |  |

