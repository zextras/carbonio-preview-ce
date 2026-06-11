#!/bin/bash

# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# 
# SPDX-License-Identifier: AGPL-3.0-only

# Run config migration (writes application keys to Consul KV).
# SETUP_CONSUL_TOKEN is provided by the pending-setups framework and is
# consumed by carbonio-preview-bin --setup when the legacy config.ini
# contains application-level keys that must be migrated.
export SETUP_CONSUL_TOKEN
# TODO(dockerization): derive the consul URL from env instead of hardcoding localhost
/usr/bin/carbonio-preview-bin --setup http://127.0.0.1:8500 || {
  echo "carbonio-preview config migration failed; aborting setup" >&2
  exit 1
}

# Register service-discover ACL policy/token, write intentions and
# service-protocol, create systemd override, then restart the service.
carbonio-preview setup
