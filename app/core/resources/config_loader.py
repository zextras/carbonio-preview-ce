# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import configparser
import os
from pathlib import Path
from typing import Any, Dict, List, Optional

DEFAULT_CONFIG_LIST = [
    # Carbonio Preview service configuration
    ("carbonio.preview.name", "preview"),
    ("carbonio.preview.default_host", "127.78.0.6"),
    ("carbonio.preview.default_port", "10000"),
    ("carbonio.preview.timeout_in_seconds", "30"),
    ("carbonio.preview.docs-timeout", "15"),
    ("carbonio.preview.workers", "2"),
    ("carbonio.preview.image_name", "image"),
    ("carbonio.preview.health_name", "health"),
    ("carbonio.preview.pdf_name", "pdf"),
    ("carbonio.preview.document_name", "document"),
    ("carbonio.preview.enable_document_preview", "true"),
    ("carbonio.preview.enable_document_thumbnail", "false"),

    # Log configuration
    ("log.format", "[%(asctime)s] %(levelname)s [%(name)s.%(funcName)s:%(lineno)d] %(message)s"),
    ("log.level", "info"),
    ("log.path", "/var/log/carbonio/preview/"),

    # Image constants
    ("image_constants.minimum_resolution", "80"),

    # Carbonio Storages configuration
    ("carbonio.storages.name", "slimstore"),
    ("carbonio.storages.download_api", "download"),
    ("carbonio.storages.health_check", "health/live"),
    ("carbonio.storages.default_protocol", "http"),
    ("carbonio.storages.default_host", "127.78.0.6"),
    ("carbonio.storages.default_port", "20000"),

    # Carbonio Docs-Editor configuration
    ("carbonio.docs-editor.default_protocol", "http"),
    ("carbonio.docs-editor.default_host", "127.78.0.6"),
    ("carbonio.docs-editor.default_port", "20001"),
    ("carbonio.docs-editor.service_endpoint", "services/docs/editor"),
    ("carbonio.docs-editor.convert_api", "cool/convert-to"),
]

ENV_MAPPING = {
    'PREVIEW_HOST': ('carbonio.preview', 'default_host'),
    'PREVIEW_PORT': ('carbonio.preview', 'default_port'),
    'STORAGES_HOST': ('carbonio.storages', 'default_host'),
    'STORAGES_PORT': ('carbonio.storages', 'default_port'),
    'DOCS_EDITOR_HOST': ('carbonio.docs-editor', 'default_host'),
    'DOCS_EDITOR_PORT': ('carbonio.docs-editor', 'default_port'),
}

config = configparser.ConfigParser(interpolation=None)
message_config = configparser.ConfigParser()
config_dict: Dict[str, Dict[str, str]] = {}


def create_default_path_list(file_name: str) -> List[str]:
    starting_dir = Path(__file__).parent
    return [
        str(Path("/", "etc", "carbonio", "preview", file_name)),
        str(Path("app", "core", "resources", file_name)),
        str(Path(starting_dir, file_name)),
        str(Path(Path.cwd(), "package", "preview", file_name)),
        # This path is for testing purpose
        str(Path(Path.cwd().parent, "package", "preview", file_name)),
    ]


def build_default_config() -> Dict[str, Dict[str, str]]:
    config_dict = {}

    for key, value in DEFAULT_CONFIG_LIST:
        if key.count('.') >= 2:
            parts = key.split('.')
            section = '.'.join(parts[:-1])
            config_key = parts[-1]
        else:
            section, config_key = key.split('.', 1)

        if section not in config_dict:
            config_dict[section] = {}

        config_dict[section][config_key] = value

    return config_dict


def apply_config_file_overrides(base_config: Dict[str, Dict[str, str]],
                               config_parser: configparser.ConfigParser) -> Dict[str, Dict[str, str]]:
    result = deep_copy_dict(base_config)

    for section_name in config_parser.sections():
        if section_name not in result:
            result[section_name] = {}

        for key, value in config_parser.items(section_name):
            result[section_name][key] = value

    return result


def apply_env_overrides(base_config: Dict[str, Dict[str, str]]) -> Dict[str, Dict[str, str]]:
    result = deep_copy_dict(base_config)

    for env_var, (section, key) in ENV_MAPPING.items():
        value = os.getenv(env_var)
        if value is not None:
            if section not in result:
                result[section] = {}
            result[section][key] = value

    return result


def load_message_config(path_list: Optional[List[str]] = None) -> List[str]:
    if path_list is None:
        path_list = create_default_path_list("messages.ini")
    return message_config.read(path_list)


def deep_copy_dict(source: Dict[str, Dict[str, str]]) -> Dict[str, Dict[str, str]]:
    return {section: dict(config_section) for section, config_section in source.items()}


def load_config(path_list: Optional[List[str]] = None) -> List[str]:
    global config_dict

    if path_list is None:
        path_list = create_default_path_list("config.ini")

    config_dict = build_default_config()

    loaded_files = config.read(path_list)
    if loaded_files:
        print(f"Loaded config files: {loaded_files}")
        config_dict = apply_config_file_overrides(config_dict, config)
    else:
        print("No config file found, using default configuration")

    config_dict = apply_env_overrides(config_dict)

    return loaded_files


def read_config(
    section: str,
    value: str,
    raw: bool = False,
    default_value: Any = None,
) -> str:
    try:
        if section in config_dict and value in config_dict[section]:
            return config_dict[section][value]
        elif default_value is not None:
            return str(default_value)
        else:
            raise KeyError(f"Configuration key '{section}.{value}' not found")
    except KeyError:
        if default_value is not None:
            return str(default_value)
        raise


def read_message_config(section: str, value: str, raw: bool = False) -> str:
    return message_config.get(section, value, raw=raw)


def get_config_dict() -> Dict[str, Dict[str, str]]:
    return deep_copy_dict(config_dict)


load_config()
load_message_config()