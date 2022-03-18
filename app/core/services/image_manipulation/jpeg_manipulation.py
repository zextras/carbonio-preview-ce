# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

import io

from PIL import Image

from app.core.schemas.enums import image_quality_enum
from app.core.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.services.image_manipulation.image_manipulation import (
    resize_with_crop_and_paddings,
    save_image_to_buffer,
    resize_with_paddings,
    add_circle_margins_to_image,
)


def jpeg_preview(
    _x: int, _y: int, _quality: ImageQualityEnum, _crop: bool, content: io.BytesIO
) -> io.BytesIO:
    """
    Create JPEG preview with the given quality
    \f
    :param _crop: True will crop the image, losing data on the borders
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param _quality: quality to convert the image to
    :param content: image raw bytes
    :return: compressed image raw bytes
    """
    _quality_value = image_quality_enum.get_jpeg_int_quality(_quality)
    if _crop:
        img: Image.Image = resize_with_crop_and_paddings(
            content=content, requested_x=_x, requested_y=_y
        )
    else:
        img: Image.Image = resize_with_paddings(
            content=content, requested_x=_x, requested_y=_y
        )
    # JPEG does not support RGBA or P
    if img.mode in ("RGBA", "P"):
        img = img.convert("RGB")
    output: io.BytesIO = save_image_to_buffer(
        img=img, _format="JPEG", _optimize=False, _quality_value=_quality_value
    )
    return output


def jpeg_thumbnail(
    _x: int,
    _y: int,
    border: ImageBorderShapeEnum,
    _quality: ImageQualityEnum,
    content: io.BytesIO,
) -> io.BytesIO:
    """
    Create JPEG thumbnail with the given quality
    \f
    :param _quality: quality to convert the image to
    :param border: which type of border to be used
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param content: image raw bytes
    :return: compressed image raw bytes
    """
    _quality_value = image_quality_enum.get_jpeg_int_quality(_quality)
    img: Image.Image = resize_with_crop_and_paddings(
        content=content, requested_x=_x, requested_y=_y
    )
    if border == ImageBorderShapeEnum.ROUNDED:
        img = add_circle_margins_to_image(img)

    # JPEG does not support RGBA or P
    if img.mode in ("RGBA", "P"):
        img = img.convert("RGB")
    output: io.BytesIO = save_image_to_buffer(
        img=img, _format="JPEG", _optimize=False, _quality_value=_quality_value
    )
    return output
