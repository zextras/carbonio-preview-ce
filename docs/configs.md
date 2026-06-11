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
| `carbonio-preview/docs-editor/convert-api` | `cool/convert-to` |  |
| `carbonio-preview/docs-editor/service-endpoint` | `services/docs/editor` |  |
| `carbonio-preview/docs-timeout-in-seconds` | `15` |  |
| `carbonio-preview/enable-document-preview` | `true` |  |
| `carbonio-preview/enable-document-thumbnail` | `false` |  |
| `carbonio-preview/image-minimum-resolution` | `80` |  |
| `carbonio-preview/pdf-workers` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/storages/download-api` | `download` |  |
| `carbonio-preview/storages/health-check` | `health/live` |  |
| `carbonio-preview/timeout-in-seconds` | `30` |  |
| `carbonio-preview/vips-concurrency` | `1` |  |
| `carbonio-preview/workers` | `2` |  |

