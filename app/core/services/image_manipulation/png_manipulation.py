# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io

from PIL import Image

from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.services.image_manipulation.image_manipulation import (
    resize_with_crop_and_paddings,
    save_image_to_buffer,
    resize_with_paddings,
    add_circle_margins_with_transparency,
)


def png_preview(
    _x: int,
    _y: int,
    _crop: bool,
    content: io.BytesIO,
    crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> io.BytesIO:
    """
    Create PNG preview
    \f
    :param _crop: True will crop the image, losing data on the borders
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param content: image raw bytes
    :param crop_position: where should the image zoom when cropped
    :return: compressed image raw bytes
    """
    img: Image.Image
    if _crop:
        img = resize_with_crop_and_paddings(
            content=content, requested_x=_x, requested_y=_y, crop_position=crop_position
        )
    else:
        img = resize_with_paddings(content=content, requested_x=_x, requested_y=_y)
    output: io.BytesIO = save_image_to_buffer(img=img, _format="PNG", _optimize=False)
    return output


def png_thumbnail(
    _x: int,
    _y: int,
    border: ImageBorderShapeEnum,
    content: io.BytesIO,
    crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> io.BytesIO:
    """
    Create PNG thumbnail
    \f
    :param border: which type of border to be used
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param content: image raw bytes
    :param crop_position: where should the image zoom when cropped
    :return: compressed image raw bytes
    """
    img: Image.Image = resize_with_crop_and_paddings(
        content=content, requested_x=_x, requested_y=_y, crop_position=crop_position
    )
    if border == ImageBorderShapeEnum.ROUNDED:
        img = add_circle_margins_with_transparency(img=img, blur_radius=2)

    output: io.BytesIO = save_image_to_buffer(img=img, _format="PNG", _optimize=False)
    return output
