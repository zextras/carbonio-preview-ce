# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
from enum import Enum


class VerticalCropPositionEnum(str, Enum):
    """
    Class representing all the vertical crop positions accepted values
    """

    TOP = "top"

    CENTER = "center"
