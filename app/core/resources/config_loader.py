# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import configparser
import os
from typing import List

config = configparser.ConfigParser()
message_config = configparser.ConfigParser()


def _create_default_path_list(file_name: str) -> List[str]:
    starting_dir = os.path.dirname(__file__)
    return [
        os.path.join("/", "etc", "carbonio", "preview", file_name),
        os.path.join("app", "core", "resources", file_name),
        os.path.join(starting_dir, file_name),
    ]


def load_message_config(path_list=None) -> List[str]:
    if path_list is None:
        path_list = _create_default_path_list("messages.ini")
    return message_config.read(path_list)


def load_config(path_list=None) -> List[str]:
    if path_list is None:
        path_list = _create_default_path_list("config.ini")
    return config.read(path_list)


load_config()
load_message_config()


def read_config(section: str, value: str, raw: bool = False) -> str:
    return config.get(section, value, raw=raw)


def read_message_config(section: str, value: str, raw: bool = False) -> str:
    return message_config.get(section, value, raw=raw)
