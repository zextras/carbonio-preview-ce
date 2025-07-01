# SPDX-FileCopyrightText: 2024 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import ipaddress
from pathlib import Path
from typing import Final
import re

from app.core.resources.config_loader import config_dict

PORT_MAX_NUMBER: Final[int] = 65535
PORT_MIN_NUMBER: Final[int] = 0
TEST_LOG_PATH: Final[str] = "./venv/"


def validate_ip(value: str) -> str:
    try:
        ipaddress.ip_address(value)
    except ValueError:
        hostname_pattern = re.compile(
            r'^(?=.{1,253}$)(?!-)[A-Za-z0-9-]{1,63}(?<!-)(\.[A-Za-z0-9-]{1,63})*$'
        )
        
        if hostname_pattern.match(value):
            return value
        else:
            msg = f"Invalid IP address or hostname: {value}"
            raise ValueError(msg)


def validate_log_level(value: str) -> str:
    if value.upper() not in ["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]:
        msg = f"Log level is not valid. Given value {value}"
        raise ValueError(msg)
    return value


def validate_log_path(value: str) -> str:
    if not Path(value).resolve().exists():
        if not Path(TEST_LOG_PATH).resolve().exists():
            msg = "Log path is not valid or does not exist."
            raise ValueError(msg)
        return TEST_LOG_PATH
    return value


def validate_positive_int(value) -> int:
    value = int(value)
    if value <= 0:
        msg = f"Value must be a positive integer. Given value {value}"
        raise ValueError(msg)
    return value


def validate_non_negative_int(value) -> int:
    value = int(value)
    if value < 0:
        msg = f"Value must be a non-negative integer. Given value {value}"
        raise ValueError(msg)
    return value


def validate_port(value) -> int:
    value = int(value)
    if not (PORT_MIN_NUMBER <= value <= PORT_MAX_NUMBER):
        msg = f"Port number must be between {PORT_MIN_NUMBER} and {PORT_MAX_NUMBER}. Given value {value}"
        raise ValueError(msg)
    return value


def create_flat_config_dict(config_dict):
    flat_config = {}

    # Mapping for backward compatibility
    section_mappings = {
        'carbonio.preview': 'service',
        'carbonio.storages': 'storage',
        'carbonio.docs-editor': 'document_conversion',
        'log': 'log',
        'image_constants': 'image_constants'
    }

    field_mappings = {
        'default_host': 'ip',
        'default_port': 'port',
        'default_protocol': 'protocol'
    }

    for section, section_config in config_dict.items():
        flat_prefix = section_mappings.get(section, section)

        for field, value in section_config.items():
            mapped_field = field_mappings.get(field, field)

            flat_key = f"{flat_prefix}_{mapped_field}"
            flat_config[flat_key] = value

    return flat_config


class AppConfig:
    def __init__(self, config: dict) -> None:
        # Preview
        self.service_name: str = config["service_name"]
        self.service_ip: str = validate_ip(config["service_ip"])
        self.service_port: int = validate_port(config["service_port"])
        self.service_timeout_in_seconds: int = validate_positive_int(
            config["service_timeout_in_seconds"],
        )

        self.number_of_workers: int = validate_positive_int(config["service_workers"])

        self.service_image_name: str = config["service_image_name"]
        self.service_health_name: str = config["service_health_name"]
        self.service_pdf_name: str = config["service_pdf_name"]
        self.service_document_name: str = config["service_document_name"]

        self.enable_document_preview: bool = config.get(
            "service_enable_document_preview",
            True,
        )
        self.enable_document_thumbnail: bool = config.get(
            "service_enable_document_thumbnail",
            False,
        )

        self.docs_timeout: int = validate_positive_int(
            config.get("service_docs-timeout", 5),
        )

        # Logs
        self.log_path: str = validate_log_path(config["log_path"])
        self.log_format: str = config["log_format"]
        self.log_level: str = validate_log_level(config["log_level"])

        # Image
        self.image_constants_minimum_resolution: int = validate_non_negative_int(
            config["image_constants_minimum_resolution"],
        )

        # Storages
        self.storage_name: str = config["storage_name"]
        self.storage_download_api: str = config["storage_download_api"]
        self.storage_health_check: str = config["storage_health_check"]

        self.storage_protocol: str = config["storage_protocol"]
        self.storage_ip: str = validate_ip(config["storage_ip"])
        self.storage_port: int = validate_port(config["storage_port"])

        # Docs
        self.document_conversion_protocol: str = config["document_conversion_protocol"]
        self.document_conversion_ip: str = validate_ip(config["document_conversion_ip"])
        self.document_conversion_port: int = validate_port(
            config["document_conversion_port"],
        )

        self.document_conversion_service_endpoint: str = config[
            "document_conversion_service_endpoint"
        ]
        self.document_conversion_convert_api: str = config[
            "document_conversion_convert_api"
        ]


flat_config_dict = create_flat_config_dict(config_dict)
app_config = AppConfig(flat_config_dict)

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
LOG_LEVEL: Final[str] = app_config.log_level.upper()

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