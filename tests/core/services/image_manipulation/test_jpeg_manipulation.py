# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io

from PIL import Image

from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.services.image_manipulation import jpeg_manipulation


def test_jpeg_compression_rgb_success_with_crop(expect):
    # Given
    x = 0
    y = 0
    crop_position = VerticalCropPositionEnum.CENTER
    img_size = (300, 400)
    quality = ImageQualityEnum.MEDIUM
    parse_img: Image.Image = Image.new("RGB", img_size)
    content_data: io.BytesIO = io.BytesIO()

    expect(jpeg_manipulation, times=1).parse_to_valid_image(content_data).thenReturn(
        parse_img,
    )

    expect(jpeg_manipulation, times=1).resize_with_crop_and_paddings(
        img=parse_img,
        requested_x=x,
        requested_y=y,
        crop_position=crop_position,
    ).thenReturn(parse_img)

    expect(jpeg_manipulation, times=1).save_image_to_buffer(
        img=parse_img,
        _format="JPEG",
        _optimize=False,
        _quality_value=quality.get_jpeg_int_quality(),
    ).thenReturn(None)

    # When
    result = jpeg_manipulation.jpeg_preview(
        _x=0,
        _y=0,
        _quality=quality,
        content=content_data,
        _crop=True,
    )

    # Then
    assert result is None


def test_jpeg_compression_rgb_success_without_crop(expect):
    # Given
    x = 0
    y = 0
    img_size = (300, 400)
    quality = ImageQualityEnum.MEDIUM
    parse_img: Image.Image = Image.new("RGB", img_size)
    content_data: io.BytesIO = io.BytesIO()

    expect(jpeg_manipulation, times=1).parse_to_valid_image(content_data).thenReturn(
        parse_img,
    )

    expect(jpeg_manipulation, times=1).resize_with_paddings(
        img=parse_img,
        requested_x=x,
        requested_y=y,
    ).thenReturn(parse_img)

    expect(jpeg_manipulation, times=1).save_image_to_buffer(
        img=parse_img,
        _format="JPEG",
        _optimize=False,
        _quality_value=quality.get_jpeg_int_quality(),
    ).thenReturn(None)

    # When
    result = jpeg_manipulation.jpeg_preview(
        _x=x,
        _y=y,
        _quality=quality,
        content=content_data,
        _crop=False,
    )

    # Then
    assert result is None


def test_jpeg_compression_rgba_success_with_crop(expect):
    # Given
    x = 0
    y = 0
    img_size = (300, 400)
    quality = ImageQualityEnum.MEDIUM
    parse_img: Image.Image = Image.new("RGBA", img_size)
    content_data: io.BytesIO = io.BytesIO()

    expect(jpeg_manipulation, times=1).parse_to_valid_image(content_data).thenReturn(
        parse_img,
    )

    expect(jpeg_manipulation, times=1).resize_with_crop_and_paddings(
        img=parse_img,
        requested_x=x,
        requested_y=y,
        crop_position=VerticalCropPositionEnum.CENTER,
    ).thenReturn(parse_img)

    expect(jpeg_manipulation, times=1).save_image_to_buffer(
        img=parse_img.convert("RGB"),
        _format="JPEG",
        _optimize=False,
        _quality_value=quality.get_jpeg_int_quality(),
    ).thenReturn(None)

    # When
    result = jpeg_manipulation.jpeg_preview(
        _x=0,
        _y=0,
        _quality=quality,
        content=content_data,
        _crop=True,
    )

    # Then
    assert result is None


def test_jpeg_compression_rgba_success_without_crop(expect):
    # Given
    x = 0
    y = 0
    img_size = (300, 400)
    quality = ImageQualityEnum.MEDIUM
    parse_img: Image.Image = Image.new("RGBA", img_size)
    content_data: io.BytesIO = io.BytesIO()

    expect(jpeg_manipulation, times=1).parse_to_valid_image(content_data).thenReturn(
        parse_img,
    )

    expect(jpeg_manipulation, times=1).resize_with_paddings(
        img=parse_img,
        requested_x=x,
        requested_y=y,
    ).thenReturn(parse_img)

    expect(jpeg_manipulation, times=1).save_image_to_buffer(
        img=parse_img.convert("RGB"),
        _format="JPEG",
        _optimize=False,
        _quality_value=quality.get_jpeg_int_quality(),
    ).thenReturn(None)

    # When
    result = jpeg_manipulation.jpeg_preview(
        _x=0,
        _y=0,
        _quality=quality,
        content=content_data,
        _crop=False,
    )

    # Then
    assert result is None
