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
| `carbonio.postgresql.port` | `20003` |
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
| `carbonio-preview/database/credentials/db-name` | *(not set)* | Crashes; but always set by database bootstrap |
| `carbonio-preview/database/credentials/db-password` | *(not set)* | Crashes; but always set by database bootstrap |
| `carbonio-preview/database/credentials/db-username` | *(not set)* | Crashes; but always set by database bootstrap |
| `carbonio-preview/database/db-pool-max-lifetime` | `600000` |  |
| `carbonio-preview/database/db-pool-max-size` | `10` |  |
| `carbonio-preview/database/db-pool-min-size` | `2` |  |
| `carbonio-preview/document/conversion-timeout-seconds` | `15` |  |
| `carbonio-preview/document/enable-preview` | `true` |  |
| `carbonio-preview/document/enable-thumbnail` | `false` |  |
| `carbonio-preview/document/subprocess-pool-size` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/image-document/fetch-timeout-seconds` | `30` |  |
| `carbonio-preview/image-document/max-concurrent-operations` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/video/max-attempts` | `3` |  |
| `carbonio-preview/video/max-concurrent-extractions` | *(not set)* | Defaults to the number of CPUs |
| `carbonio-preview/video/poll-interval-seconds` | `15` |  |
| `carbonio-preview/video/stuck-generation-timeout-seconds` | `900` |  |

