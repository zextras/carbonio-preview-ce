# SPDX-FileCopyrightText: 2024 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
from typing import TYPE_CHECKING

from wand.exceptions import CoderError
from wand.image import Image

from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)

if TYPE_CHECKING:
    from PIL import Image


def svg_preview(
        _x: int,
        _y: int,
        _crop: bool,
        content: io.BytesIO,
        crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> io.BytesIO:
    """
    Create SVG preview
    \f
    :param _crop: True will crop the image, losing data on the borders
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param content: image raw bytes
    :param crop_position: where should the image zoom when cropped
    :return: compressed image raw bytes
    """

    with Image(blob=content.getvalue(), format='svg') as img:
        try:
            if _x != 0 and _y != 0:
                img.resize(width=_x, height=_y)
            if _crop:
                if crop_position == VerticalCropPositionEnum.CENTER:
                    left = (img.width - _x) // 2
                    top = (img.height - _y) // 2
                elif crop_position == VerticalCropPositionEnum.TOP:
                    left = (img.width - _x) // 2
                    top = 0
                else:
                    left = 0
                    top = 0

                img.crop(left=left, top=top, width=_x, height=_y)

            svg_bytes_io = io.BytesIO()
            img.save(file=svg_bytes_io)
            print(img)
            svg_bytes_io.seek(0)

            return svg_bytes_io
        except Exception as e:
            print(e)
            return content



'''
def svg_thumbnail(
        _x: int,
        _y: int,
        border: ImageBorderShapeEnum,
        content: io.BytesIO,
        crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> io.BytesIO:
    """
    Create SVG thumbnail
    \f
    :param border: which type of border to be used
    :param _x: width to resize the image to
    :param _y: height to resize the image to
    :param content: image raw bytes
    :param crop_position: where should the image zoom when cropped
    :return: compressed image raw bytes
    """
    img: Image.Image = parse_to_valid_image(content)
    img = resize_with_crop_and_paddings(
        img=img,
        requested_x=_x,
        requested_y=_y,
        crop_position=crop_position,
    )
    if border == ImageBorderShapeEnum.ROUNDED:
        img = add_circle_margins_with_transparency(img=img, blur_radius=2)

    output: io.BytesIO = save_image_to_buffer(img=img, _format="SVG", _optimize=False)
    return output
'''


def svg_to_png(svg_bytes_io: io.BytesIO) -> io.BytesIO:
    with Image(blob=svg_bytes_io.getvalue(), format="svg") as img:
        png_bytes_io = io.BytesIO()
        img.format = 'png'
        img.save(file=png_bytes_io)
    png_bytes_io.seek(0)
    return png_bytes_io


def is_svg(svg_bytes_io: io.BytesIO) -> bool:
    try:
        with Image(blob=svg_bytes_io.getvalue(), format="svg"):
            return True
    except CoderError:
        return False
