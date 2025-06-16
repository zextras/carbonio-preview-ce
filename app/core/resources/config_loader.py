# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import configparser
from pathlib import Path
from typing import Any, Dict, List, Optional

config = configparser.ConfigParser(interpolation=None)
message_config = configparser.ConfigParser()

cli_overrides: Dict[str, str] = {}


def _create_default_path_list(file_name: str) -> List[str]:
    starting_dir = Path(__file__).parent
    return [
        str(Path("/", "etc", "carbonio", "preview", file_name)),
        str(Path("app", "core", "resources", file_name)),
        str(Path(starting_dir, file_name)),
        str(Path(Path.cwd(), "package", "preview", file_name)),
        # This path is for testing purpose
        str(Path(Path.cwd().parent, "package", "preview", file_name)),
    ]


def parse_env_overrides():
    """Parse environment variables for configuration overrides"""
    global env_overrides
    env_mapping = {
        'PREVIEW_HOST': 'service.ip',
        'PREVIEW_PORT': 'service.port',
        'STORAGES_HOST': 'storage.ip',
        'STORAGES_PORT': 'storage.port',
        'DOCS_EDITOR_HOST': 'document_conversion.ip',
        'DOCS_EDITOR_PORT': 'document_conversion.port',
    }
    for env_var, config_key in env_mapping.items():
        value = os.getenv(env_var)
        if value is not None:
            env_overrides[config_key] = value


def load_message_config(path_list: Optional[List[str]] = None) -> List[str]:
    if path_list is None:
        path_list = _create_default_path_list("messages.ini")
    return message_config.read(path_list)


def load_config(path_list: Optional[List[str]] = None) -> List[str]:
    if path_list is None:
        path_list = _create_default_path_list("config.ini")
    return config.read(path_list)


parse_env_overrides()
load_config()
for override_key, value in env_overrides.items():
    section, key = override_key.split('.', 1)
    if config.has_section(section):
        config.set(section, key, value)
config_dict = {section: dict(config.items(section)) for section in config.sections()}
load_message_config()


def read_config(
    section: str,
    value: str,
    raw: bool = False,
    default_value: Any = None,
) -> str:
    if default_value:
        return config.get(section, value, raw=raw, fallback=default_value)

    return config.get(section, value, raw=raw)


def read_message_config(section: str, value: str, raw: bool = False) -> str:
    return message_config.get(section, value, raw=raw)
