# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from enum import Enum


class ImageTypeEnum(str, Enum):
    """
    Class representing all the image type accepted values
    """

    JPEG = "jpeg"

    PNG = "png"

    GIF = "gif"
