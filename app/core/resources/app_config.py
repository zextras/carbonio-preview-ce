# SPDX-FileCopyrightText: 2023 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import ipaddress
from pathlib import Path
from typing import Final, Type

from pydantic import (
    Field,
    NonNegativeInt,
    PositiveInt,
    field_validator,
)
from pydantic.main import BaseModel

from app.core.resources.config_loader import config_dict

PORT_MAX_NUMBER: Final[int] = 65535
PORT_MIN_NUMBER: Final[int] = 0


class AppConfig(BaseModel):
    # Service
    service_name: str
    service_ip: str
    service_port: NonNegativeInt = Field(ge=PORT_MIN_NUMBER, le=PORT_MAX_NUMBER)
    service_timeout_in_seconds: PositiveInt

    number_of_workers: PositiveInt = Field(alias="service_workers")

    service_image_name: str
    service_health_name: str
    service_pdf_name: str
    service_document_name: str

    enable_document_preview: bool = Field(
        default=True,
        alias="service_enable_document_preview",
    )
    enable_document_thumbnail: bool = Field(
        default=False,
        alias="service_enable_document_thumbnail",
    )

    docs_timeout: PositiveInt = Field(default=5, alias="service_docs-timeout")

    # log
    log_path: str
    log_format: str
    log_level: str

    # image
    image_constants_minimum_resolution: NonNegativeInt

    # storage
    storage_name: str
    storage_download_api: str
    storage_health_check: str

    storage_protocol: str
    storage_ip: str
    storage_port: NonNegativeInt = Field(ge=PORT_MIN_NUMBER, le=PORT_MAX_NUMBER)

    # document conv
    document_conversion_protocol: str
    document_conversion_ip: str
    document_conversion_port: NonNegativeInt = Field(
        ge=PORT_MIN_NUMBER,
        le=PORT_MAX_NUMBER,
    )

    document_conversion_service_endpoint: str
    document_conversion_convert_api: str

    @field_validator("service_ip", "storage_ip", "document_conversion_ip")
    def ip_must_be_valid(cls: Type["AppConfig"], value: str) -> str:
        # raises a ValueError if ip is not a valid ipV4 or V6
        ipaddress.ip_address(value)
        return value

    @field_validator("log_level")
    def log_level_must_be_valid(cls: Type["AppConfig"], value: str) -> str:
        if value.upper() not in ["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]:
            msg = f"Log level is not valid. Given value {value}"
            raise ValueError(msg)

        return value

    @field_validator("log_path")
    def log_path_must_exist(cls: Type["AppConfig"], value: str) -> str:
        if not Path(value).resolve().exists():
            msg = "Log path is not valid or does not exist."
            raise ValueError(msg)
        return value


# LOAD CONFIG
# pydantic does not accept nested dict, we must flatten it!
# we do it with sectionname_fieldname: field_value
flat_config_dict = {
    section + "_" + field: config_dict[section][field]
    for section in config_dict
    for field in config_dict[section]
}
app_config = AppConfig.model_validate(flat_config_dict)

# fields
SERVICE_NAME: Final[str] = app_config.service_name
SERVICE_TIMEOUT: Final[int] = app_config.service_timeout_in_seconds
SERVICE_IP: Final[str] = app_config.service_ip
SERVICE_PORT: Final[int] = app_config.service_port
SERVICE_NUMBER_OF_WORKERS: Final[int] = app_config.number_of_workers
ENABLE_DOCUMENT_PREVIEW: Final[bool] = app_config.enable_document_preview
ENABLE_DOCUMENT_THUMBNAIL: Final[bool] = app_config.enable_document_thumbnail
ARE_DOCS_ENABLED: Final[bool] = ENABLE_DOCUMENT_PREVIEW or ENABLE_DOCUMENT_THUMBNAIL

IMAGE_NAME: Final[str] = app_config.service_image_name
HEALTH_NAME: Final[str] = app_config.service_health_name
PDF_NAME: Final[str] = app_config.service_pdf_name
DOC_NAME: Final[str] = app_config.service_document_name

DOCS_TIMEOUT: Final[int] = app_config.docs_timeout
SERVICE_DESCRIPTION: Final[
    str
] = """
Preview service. 🚀 \n
You can preview the following type of files:

* **images(png/jpeg)**
* **pdf**
* **documents (xls, xlsx, ods, ppt, pptx, odp, doc, docx, odt)**

You will be able to:

* **Preview images**.
* **Generate smart thumbnails**.

The main difference between thumbnail and preview
 functionality is that preview tends to be more faithful
while thumbnail tends to elaborate on it, cropping
 it by default and rounding the image if asked.
Preview should always output the file in its original format,
 while thumbnail will convert it to an image.
There is no difference in quality between the two,
 the difference in quality can be achieved only
by asking for a jpeg format and changing the quality parameter.
"""

# LOGS

LOG_FORMAT: Final[str] = app_config.log_format
LOG_PATH: Final[str] = str(Path(app_config.log_path).resolve())
LOG_LEVEL: Final[str] = app_config.log_level

# STORAGE
STORAGE_NAME: Final[str] = app_config.storage_name
STORAGE_DOWNLOAD_API: Final[str] = app_config.storage_download_api
STORAGE_HEALTH_CHECK_API: Final[str] = app_config.storage_health_check
STORAGE_PROTOCOL: Final[str] = app_config.storage_protocol
STORAGE_IP: Final[str] = app_config.storage_ip
STORAGE_PORT: Final[int] = app_config.storage_port
STORAGE_FULL_ADDRESS: Final[str] = f"{STORAGE_PROTOCOL}://{STORAGE_IP}:{STORAGE_PORT}"

# DOCUMENT CONVERSION
DOCUMENT_CONVERSION_PROTOCOL: Final[str] = app_config.document_conversion_protocol
DOCUMENT_CONVERSION_IP: Final[str] = app_config.document_conversion_ip
DOCUMENT_CONVERSION_PORT: Final[int] = app_config.document_conversion_port
DOCUMENT_CONVERSION_SERVICE_ENDPOINT: Final[
    str
] = app_config.document_conversion_service_endpoint
DOCUMENT_CONVERSION_CONVERT_API: Final[str] = app_config.document_conversion_convert_api
DOCUMENT_CONVERSION_BASE_ADDRESS: Final[
    str
] = f"{DOCUMENT_CONVERSION_PROTOCOL}://{DOCUMENT_CONVERSION_IP}:{DOCUMENT_CONVERSION_PORT}"
DOCUMENT_CONVERSION_FULL_SERVICE_ADDRESS: Final[
    str
] = f"{DOCUMENT_CONVERSION_BASE_ADDRESS}/{DOCUMENT_CONVERSION_SERVICE_ENDPOINT}/"
DOCUMENT_CONVERSION_FULL_CONVERT_ADDRESS: Final[
    str
] = f"{DOCUMENT_CONVERSION_FULL_SERVICE_ADDRESS}{DOCUMENT_CONVERSION_CONVERT_API}"

IMAGE_MIN_RES: Final[int] = app_config.image_constants_minimum_resolution
