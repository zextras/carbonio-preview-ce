# SPDX-FileCopyrightText: 2024 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
from typing import TYPE_CHECKING
from wand.image import Image

from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.services.image_manipulation.image_manipulation import (
    add_circle_margins_with_transparency,
    parse_to_valid_image,
    resize_with_crop_and_paddings,
    resize_with_paddings,
    save_image_to_buffer,
)

if TYPE_CHECKING:
    from PIL import Image


# Essentially the same as png preview because we need a png to return a svg, we just convert to svg the output buffer
# before returning it.
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

    img: Image.Image = parse_to_valid_image(content)
    if _crop:
        img = resize_with_crop_and_paddings(
            img=img,
            requested_x=_x,
            requested_y=_y,
            crop_position=crop_position,
        )
    else:
        img = resize_with_paddings(img=img, requested_x=_x, requested_y=_y)
    output: io.BytesIO = save_image_to_buffer(img=img, _format="SVG", _optimize=False)
    return png_to_svg(output)


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


def svg_to_png(svg_bytes_io):
    with Image(blob=svg_bytes_io.getvalue(), format="svg") as img:
        png_bytes_io = io.BytesIO()
        img.format = 'png'
        img.save(file=png_bytes_io)
    png_bytes_io.seek(0)
    return png_bytes_io


def png_to_svg(png_bytes_io):
    with Image(blob=png_bytes_io.getvalue(), format="png") as img:
        svg_bytes_io = io.BytesIO()
        img.format = 'svg'
        img.save(file=svg_bytes_io)
    svg_bytes_io.seek(0)
    return svg_bytes_io
