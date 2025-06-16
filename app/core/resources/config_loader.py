# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import argparse
import configparser
from pathlib import Path
from typing import Any, List, Optional

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


def parse_cli_overrides():
    """Parse command line arguments for IP and port overrides"""
    parser = argparse.ArgumentParser(description='Carbonio Preview Service', add_help=False)

    parser.add_argument('--preview-host', dest='preview_ip', help='Override preview service IP')
    parser.add_argument('--preview-port', dest='preview_port', type=int, help='Override preview service port')

    parser.add_argument('--storages-host', dest='storage_ip', help='Override storages service IP')
    parser.add_argument('--storages-port', dest='storage_port', type=int, help='Override storages service port')

    parser.add_argument('--docs-editor-host', dest='document_conversion_ip', help='Override docs-editor service IP')
    parser.add_argument('--docs-editor-port', dest='document_conversion_port', type=int, help='Override docs-editor service port')

    args, unknown = parser.parse_known_args()

    global cli_overrides
    if args.preview_ip:
        cli_overrides['service.ip'] = args.preview_ip
    if args.preview_port:
        cli_overrides['service.port'] = str(args.preview_port)
    if args.storage_ip:
        cli_overrides['storage.ip'] = args.storage_ip
    if args.storage_port:
        cli_overrides['storage.port'] = str(args.storage_port)
    if args.document_conversion_ip:
        cli_overrides['document_conversion.ip'] = args.document_conversion_ip
    if args.document_conversion_port:
        cli_overrides['document_conversion.port'] = str(args.document_conversion_port)


def load_message_config(path_list: Optional[List[str]] = None) -> List[str]:
    if path_list is None:
        path_list = _create_default_path_list("messages.ini")
    return message_config.read(path_list)


def load_config(path_list: Optional[List[str]] = None) -> List[str]:
    if path_list is None:
        path_list = _create_default_path_list("config.ini")
    return config.read(path_list)


parse_cli_overrides()
load_config()
config_dict = {section: dict(config.items(section)) for section in config.sections()}
load_message_config()


def read_config(
    section: str,
    value: str,
    raw: bool = False,
    default_value: Any = None,
) -> str:
    # Return CLI overrides if present
    override_key = f"{section}.{value}"
    if override_key in cli_overrides:
        return cli_overrides[override_key]

    if default_value:
        return config.get(section, value, raw=raw, fallback=default_value)

    return config.get(section, value, raw=raw)


def read_message_config(section: str, value: str, raw: bool = False) -> str:
    return message_config.get(section, value, raw=raw)
