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

    def get_jpeg_int_quality(self: "ImageQualityEnum") -> int:
        """
        Returns the numerical value (from 0 to 95) correlated to the enum value
        :param self: the enum to estimate as int
        :return: integer corresponding to the quality
        """
        if self.value == ImageQualityEnum.LOWEST:
            return quality.JPEG_LOWEST_INT
        if self.value == ImageQualityEnum.LOW:
            return quality.JPEG_LOW_INT
        if self.value == ImageQualityEnum.MEDIUM:
            return quality.JPEG_MEDIUM_INT
        if self.value == ImageQualityEnum.HIGH:
            return quality.JPEG_HIGH_INT

        return quality.JPEG_HIGHEST_INT
