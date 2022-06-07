# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config

# FOR CONTACTING STORAGE
storage_section_name: str = "storage"
NAME: str = read_config(section=storage_section_name, value="name")
DOWNLOAD_API: str = read_config(section=storage_section_name, value="download_api")
HEALTH_CHECK_API: str = read_config(section=storage_section_name, value="health_check")
PROTOCOL: str = read_config(section=storage_section_name, value="protocol")
IP: str = read_config(section=storage_section_name, value="ip")
PORT: str = read_config(section=storage_section_name, value="port")
FULL_ADDRESS: str = f"{PROTOCOL}://{IP}:{PORT}"
