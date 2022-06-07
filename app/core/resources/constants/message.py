# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_message_config

hard_errors_section_name: str = "hard_errors"
STORAGE_UNAVAILABLE_STRING: str = read_message_config(
    section=hard_errors_section_name, value="storage_unavailable_string"
)
GENERIC_ERROR_WITH_STORAGE: str = read_message_config(
    section=hard_errors_section_name, value="generic_error_with_storage"
)

ITEM_NOT_FOUND: str = read_message_config(
    section=hard_errors_section_name, value="item_not_found"
)

INPUT_ERROR: str = read_message_config(
    section=hard_errors_section_name, value="input_error"
)

LIBRE_OFFICE_NOT_RUNNING: str = read_message_config(
    section=hard_errors_section_name, value="libre_office_not_running"
)

# Validation
validation_section_name: str = "validation"

# pdf
NUMBER_OF_PAGES_NOT_VALID: str = read_message_config(
    section=validation_section_name, value="number_of_pages_not_valid_error"
)

# image
HEIGHT_WIDTH_NOT_VALID_ERROR: str = read_message_config(
    section=validation_section_name, value="height_or_width_not_valid_error"
)

HEIGHT_OR_WIDTH_NOT_INSERTED_ERROR: str = read_message_config(
    section=validation_section_name, value="height_or_width_not_inserted_error"
)

ID_NOT_VALID_ERROR: str = read_message_config(
    section=validation_section_name, value="id_not_valid_error"
)

VERSION_NOT_VALID_ERROR: str = read_message_config(
    section=validation_section_name, value="version_not_valid_error"
)

FORMAT_NOT_SUPPORTED_ERROR: str = read_message_config(
    section=validation_section_name, value="format_not_supported_error"
)

FILE_NOT_VALID_ERROR: str = read_message_config(
    section=validation_section_name, value="file_not_valid_error"
)
