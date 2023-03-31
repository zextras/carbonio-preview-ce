# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config

# LOG
_document_conversion_section: str = "document_conversion"
PROTOCOL: str = read_config(section=_document_conversion_section, value="protocol")
IP: str = read_config(section=_document_conversion_section, value="ip")
PORT: str = read_config(section=_document_conversion_section, value="port")
CONVERT_API: str = read_config(
    section=_document_conversion_section, value="convert_api"
)

FULL_ADDRESS: str = f"{PROTOCOL}://{IP}:{PORT}"
