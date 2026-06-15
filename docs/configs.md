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

| Key | Default |
| --- | ------- |
| `carbonio-preview/cache-max-mb` | `256` |
| `carbonio-preview/enable-document-preview` | `true` |
| `carbonio-preview/enable-document-thumbnail` | `false` |

