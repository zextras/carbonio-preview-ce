# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import logging

from PIL import Image

from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.services.image_manipulation.gif_utility_functions import (
    add_circle_margins_to_gif,
    parse_to_valid_gif,
)
from app.core.services.image_manipulation.image_manipulation import (
    save_image_to_buffer,
    resize_with_crop_and_paddings,
    resize_with_paddings,
)

logger: logging.Logger = logging.getLogger(__name__)


def gif_preview(
    _x: int,
    _y: int,
    _quality: ImageQualityEnum,
    _crop: bool,
    content: io.BytesIO,
    crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> io.BytesIO:
    """
    Create GIF preview with the given quality
    \f
    :param _crop: True will crop the image, losing data on the borders
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param _quality: quality to convert the image to
    :param content: image raw bytes
    :param crop_position: the position from which the image will be cropped
    :return: compressed image raw bytes
    """
    _quality_value = _quality.get_jpeg_int_quality()
    gif: Image.Image = parse_to_valid_gif(content)
    if _crop:
        gif = resize_with_crop_and_paddings(
            img=gif, requested_x=_x, requested_y=_y, crop_position=crop_position
        )
    else:
        gif = resize_with_paddings(img=gif, requested_x=_x, requested_y=_y)

    output: io.BytesIO = save_image_to_buffer(
        img=gif, _format="GIF", _optimize=False, _quality_value=_quality_value
    )
    return output


def gif_thumbnail(
    _x: int,
    _y: int,
    border: ImageBorderShapeEnum,
    _quality: ImageQualityEnum,
    content: io.BytesIO,
    crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> io.BytesIO:
    """
    Create GIF thumbnail with the given quality
    \f
    :param _quality: quality to convert the image to
    :param border: which type of border to be used
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param content: image raw bytes
    :param crop_position: the position from which the image will be cropped
    :return: compressed image raw bytes
    """
    _quality_value = _quality.get_jpeg_int_quality()
    gif: Image.Image = parse_to_valid_gif(content=content)
    gif = resize_with_crop_and_paddings(
        img=gif, requested_x=_x, requested_y=_y, crop_position=crop_position
    )
    if border == ImageBorderShapeEnum.ROUNDED:
        gif = add_circle_margins_to_gif(gif)

    output: io.BytesIO = save_image_to_buffer(
        img=gif, _format="GIF", _optimize=False, _quality_value=_quality_value
    )
    return output
