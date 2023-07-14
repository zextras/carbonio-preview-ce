# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config

_document_conversion_section: str = "document_conversion"
PROTOCOL: str = read_config(section=_document_conversion_section, value="protocol")
IP: str = read_config(section=_document_conversion_section, value="ip")
PORT: str = read_config(section=_document_conversion_section, value="port")
SERVICE_ENDPOINT: str = read_config(
    section=_document_conversion_section,
    value="service_endpoint",
)
CONVERT_API: str = read_config(
    section=_document_conversion_section,
    value="convert_api",
)

BASE_ADDRESS: str = f"{PROTOCOL}://{IP}:{PORT}"
FULL_SERVICE_ADDRESS: str = f"{BASE_ADDRESS}/{SERVICE_ENDPOINT}/"
FULL_CONVERT_ADDRESS: str = f"{FULL_SERVICE_ADDRESS}{CONVERT_API}"
