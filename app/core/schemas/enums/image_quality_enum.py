# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from enum import Enum

from app.core.resources.constants.image import quality


class ImageQualityEnum(str, Enum):
    """
    Class representing all the image quality accepted values
    """

    LOWEST = "lowest"

    LOW = "low"

    MEDIUM = "medium"

    HIGH = "high"

    HIGHEST = "highest"


def get_jpeg_int_quality(quality_enum: ImageQualityEnum) -> int:
    """
    Returns the numerical value (from 0 to 95) correlated to the enum value
    :param quality_enum: the enum to estimate as int
    :return: int corresponding to the quality
    """
    if quality_enum == ImageQualityEnum.LOWEST:
        return quality.JPEG_LOWEST_INT
    elif quality_enum == ImageQualityEnum.LOW:
        return quality.JPEG_LOW_INT
    elif quality_enum == ImageQualityEnum.MEDIUM:
        return quality.JPEG_MEDIUM_INT
    elif quality_enum == ImageQualityEnum.HIGH:
        return quality.JPEG_HIGH_INT
    else:
        return quality.JPEG_HIGHEST_INT
