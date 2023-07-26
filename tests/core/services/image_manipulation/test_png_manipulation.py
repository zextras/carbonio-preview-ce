# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io

from PIL import Image

from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.services.image_manipulation import png_manipulation


def test_png_compression_rgb_success_with_crop(expect):
    # Given
    x = 0
    y = 0
    parse_img: Image.Image = Image.new("RGB", (20, 20))
    crop_position = VerticalCropPositionEnum.CENTER
    content_data: io.BytesIO = io.BytesIO()
    expect(png_manipulation, times=1).parse_to_valid_image(content_data).thenReturn(
        parse_img,
    )
    expect(png_manipulation, times=1).resize_with_crop_and_paddings(
        img=parse_img,
        requested_x=x,
        requested_y=y,
        crop_position=crop_position,
    ).thenReturn(parse_img)
    expect(png_manipulation, times=1).save_image_to_buffer(
        img=parse_img,
        _format="PNG",
        _optimize=False,
    ).thenReturn(None)

    # When
    result = png_manipulation.png_preview(_x=0, _y=0, content=content_data, _crop=True)

    # Then
    assert result is None


def test_png_compression_rgb_success_without_crop(expect):
    # Given
    x = 0
    y = 0
    parse_img: Image.Image = Image.new("RGB", (20, 20))
    content_data: io.BytesIO = io.BytesIO()
    expect(png_manipulation, times=1).parse_to_valid_image(content_data).thenReturn(
        parse_img,
    )
    expect(png_manipulation, times=1).resize_with_paddings(
        img=parse_img,
        requested_x=x,
        requested_y=y,
    ).thenReturn(parse_img)
    expect(png_manipulation, times=1).save_image_to_buffer(
        img=parse_img,
        _format="PNG",
        _optimize=False,
    ).thenReturn(None)

    # When
    result = png_manipulation.png_preview(_x=0, _y=0, content=content_data, _crop=False)

    # Then
    assert result is None
