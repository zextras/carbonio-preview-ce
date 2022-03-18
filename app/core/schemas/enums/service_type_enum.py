# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from enum import Enum


class ServiceTypeEnum(str, Enum):
    """
    Class representing all the service type accepted values
    """

    FILES = "files"

    CHATS = "chats"
