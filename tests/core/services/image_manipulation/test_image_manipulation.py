# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
from unittest.mock import MagicMock, patch

from PIL import ImageOps, Image

from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.services.image_manipulation import image_manipulation


def truncate(f, n):
    """Truncates/pads a float f to n decimal places without rounding"""
    s = "{}".format(f)
    if "e" in s or "E" in s:
        return "{0:.{1}f}".format(f, n)
    i, p, d = s.partition(".")
    return ".".join([i, (d + "0" * n)[:n]])


def test_save_image_to_buffer():
    img_to_save = MagicMock()
    img_to_save.save = MagicMock()
    ImageOps.exif_transpose = MagicMock(return_value=img_to_save)
    buffer = image_manipulation.save_image_to_buffer(img_to_save)

    assert 1 == img_to_save.save.call_count
    assert [] == buffer.readlines()


def test_find_scaled_dimension_higher_than_original_square_result():
    orig_x, orig_y = 346, 826
    result = image_manipulation._find_greater_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=1000,
        requested_y=1000,
    )

    assert truncate(orig_x / orig_y, 2) == truncate(result[0] / result[1], 2)
    assert truncate(orig_y / orig_x, 2) == truncate(result[1] / result[0], 2)
    assert 1000 == result[0]
    assert 2387 == result[1]


def test_find_scaled_dimension_higher_than_original():
    orig_x, orig_y = 346, 826
    result = image_manipulation._find_greater_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=700,
        requested_y=1000,
    )

    assert truncate(orig_x / orig_y, 2) == truncate(result[0] / result[1], 2)
    assert truncate(orig_y / orig_x, 2) == truncate(result[1] / result[0], 2)
    assert 700 == result[0]
    assert 1671 == result[1]


def test_find_scaled_dimension_lower_than_original():
    orig_x, orig_y = 346, 826
    result = image_manipulation._find_greater_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=300,
        requested_y=500,
    )

    assert truncate(orig_x / orig_y, 2) == truncate(result[0] / result[1], 2)
    assert truncate(orig_y / orig_x, 2) == truncate(result[1] / result[0], 2)
    assert 300 == result[0]
    assert 716 == result[1]


def test_crop_image_image_same_size_as_requested(expect):
    # Given
    img_width, img_height = 300, 400
    crop_coordinates = (0, 0, 0, 0)
    req_x, req_y = 300, 400
    img: Image.Image = Image.new("RGB", (img_width, img_height))
    expect(image_manipulation, times=1)._get_crop_coordinates(
        req_x, req_y, img_height, img_width, VerticalCropPositionEnum.CENTER
    ).thenReturn(crop_coordinates)

    expect(image_manipulation, times=1)._crop_image_given_box(
        img, crop_coordinates
    ).thenReturn(img)
    expect(image_manipulation, times=1)._add_borders_to_crop(
        img=img, requested_x=req_x, requested_y=req_y
    ).thenReturn(img)

    image_manipulation._crop_image(
        img=img,
        requested_x=req_x,
        requested_y=req_y,
        crop_position=VerticalCropPositionEnum.CENTER,
    )


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    30,
)
@patch(
    "app.core.services.image_manipulation" ".image_manipulation._add_borders_to_image"
)
def test_add_border_to_crop_image_image_more_then_requested(mock_borders):
    img_width, img_height = 500, 600
    req_x, req_y = 300, 400
    img_mock = MagicMock()
    img_mock.size = [img_width, img_height]
    image_manipulation._add_borders_to_crop(
        img=img_mock, requested_x=req_x, requested_y=req_y
    )
    assert mock_borders.call_count == 0


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    30,
)
@patch(
    "app.core.services.image_manipulation" ".image_manipulation._add_borders_to_image"
)
def test_add_border_to_crop_image_image_less_then_requested_more_than_min(mock_borders):
    img_width, img_height = 200, 400
    req_x, req_y = 300, 400
    img_mock = MagicMock()
    img_mock.size = [img_width, img_height]
    image_manipulation._add_borders_to_crop(
        img=img_mock, requested_x=req_x, requested_y=req_y
    )
    assert mock_borders.call_count == 1


def test_find_smaller_scaled_dimension_with_higher_reqxy():
    # 826x346 => 1000x500
    orig_x, orig_y = 826, 346
    result = image_manipulation._find_smaller_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=1000,
        requested_y=500,
    )
    assert truncate(orig_y / orig_x, 1) == truncate(result[1] / result[0], 1)
    assert 1000 == result[0]
    assert 418 == result[1]


def test_find_smaller_scaled_dimension_req_x_double_y_half():
    orig_x, orig_y = 200, 100
    result = image_manipulation._find_smaller_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=400,
        requested_y=50,
    )
    assert truncate(orig_y / orig_x, 1) == truncate(result[1] / result[0], 1)
    assert 100 == result[0]
    assert 50 == result[1]


def test_find_smaller_scaled_dimension_1():
    orig_x, orig_y = 200, 100
    result = image_manipulation._find_smaller_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=800,
        requested_y=50,
    )
    assert truncate(orig_y / orig_x, 1) == truncate(result[1] / result[0], 1)
    assert 100 == result[0]
    assert 50 == result[1]


def test_find_smaller_scaled_dimension_2():
    orig_x, orig_y = 300, 200
    result = image_manipulation._find_smaller_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=1200,
        requested_y=800,
    )
    assert truncate(orig_y / orig_x, 2) == truncate(result[1] / result[0], 2)
    assert 1200 == result[0]
    assert 800 == result[1]


def test_find_smaller_scaled_dimension_3():
    orig_x, orig_y = 900, 1000
    result = image_manipulation._find_smaller_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=1700,
        requested_y=2000,
    )
    assert truncate(orig_y / orig_x, 2) == truncate(result[1] / result[0], 2)
    assert 1700 == result[0]
    assert 1888 == result[1]


def test_find_smaller_scaled_dimension_4():
    orig_x, orig_y = 4000, 200
    result = image_manipulation._find_smaller_scaled_dimensions(
        original_x=orig_x,
        original_y=orig_y,
        requested_x=8000,
        requested_y=399,
    )
    assert truncate(orig_y / orig_x, 2) == truncate(result[1] / result[0], 2)
    assert 7980 == result[0]
    assert 399 == result[1]


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    0,
)
def test_convert_requested_to_true_res_x_zero_y_zero():
    orig_x, orig_y = 300, 400
    req_x, req_y = 0, 0
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert orig_x == to_scale_x
    assert orig_y == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    0,
)
def test_convert_requested_to_true_res_x_zero_y_something():
    orig_x, orig_y = 300, 400
    req_x, req_y = 0, 300
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert orig_x == to_scale_x
    assert req_y == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    400,
)
def test_convert_requested_to_true_res_x_less_than_min():
    orig_x, orig_y = 300, 400
    req_x, req_y = 300, 400
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert 400 == to_scale_x
    assert 400 == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    400,
)
def test_convert_requested_to_true_res_y_less_than_min():
    orig_x, orig_y = 400, 300
    req_x, req_y = 0, 0
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert 400 == to_scale_x
    assert 400 == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    400,
)
def test_convert_requested_to_true_res_x_and_y_less_than_min():
    orig_x, orig_y = 300, 300
    req_x, req_y = 0, 0
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert 400 == to_scale_x
    assert 400 == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    80,
)
def test_convert_requested_to_true_res_origx_less_than_min_but_can_scale():
    orig_x, orig_y = 70, 100
    req_x, req_y = 100, 100
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert req_x == to_scale_x
    assert orig_y == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    80,
)
def test_convert_requested_to_true_res_origy_less_than_min_but_can_scale():
    orig_x, orig_y = 100, 70
    req_x, req_y = 100, 100
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert orig_x == to_scale_x
    assert req_y == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    80,
)
def test_convert_requested_to_true_res_origxy_less_than_min_but_can_scale():
    orig_x, orig_y = 70, 70
    req_x, req_y = 100, 100
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert req_x == to_scale_x
    assert req_y, to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    80,
)
def test_convert_requested_to_true_res_origx_less_than_min_but_cannot_scale():
    orig_x, orig_y = 30, 70
    req_x, req_y = 100, 100
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert 80 == to_scale_x
    assert req_y == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    80,
)
def test_convert_requested_to_true_res_origy_less_than_min_but_cannot_scale():
    orig_x, orig_y = 70, 30
    req_x, req_y = 100, 100
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert req_x == to_scale_x
    assert 80 == to_scale_y


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    80,
)
def test_convert_requested_to_true_res_origxy_less_than_min_but_cannot_scale():
    orig_x, orig_y = 30, 30
    req_x, req_y = 100, 100
    (
        to_scale_x,
        to_scale_y,
    ) = image_manipulation._convert_requested_size_to_true_res_to_scale(
        requested_x=req_x,
        requested_y=req_y,
        original_width=orig_x,
        original_height=orig_y,
    )
    assert 80 == to_scale_x
    assert 80 == to_scale_y


def test_get_crop_coordinates_top_fitting():
    result = image_manipulation._get_crop_coordinates(
        requested_x=10,
        requested_y=10,
        height=10,
        width=10,
        crop_position=VerticalCropPositionEnum.TOP,
    )
    [desired_upper, desired_right, desired_bottom, desired_left] = [0, 10, 10, 0]
    assert [desired_upper, desired_right, desired_bottom, desired_left] == result


def test_get_crop_coordinates_top_not_enough_space():
    result = image_manipulation._get_crop_coordinates(
        requested_x=5,
        requested_y=5,
        height=10,
        width=10,
        crop_position=VerticalCropPositionEnum.TOP,
    )
    [desired_upper, desired_right, desired_bottom, desired_left] = [0, 5, 5, 3]
    assert [desired_upper, desired_right, desired_bottom, desired_left] == result


def test_get_crop_coordinates_center_fitting():
    result = image_manipulation._get_crop_coordinates(
        requested_x=10,
        requested_y=10,
        height=10,
        width=10,
        crop_position=VerticalCropPositionEnum.CENTER,
    )
    [desired_upper, desired_right, desired_bottom, desired_left] = [0, 10, 10, 0]
    assert [desired_upper, desired_right, desired_bottom, desired_left] == result


def test_get_crop_coordinates_center_not_enough_space():
    result = image_manipulation._get_crop_coordinates(
        requested_x=5,
        requested_y=5,
        height=10,
        width=10,
        crop_position=VerticalCropPositionEnum.CENTER,
    )
    [desired_upper, desired_right, desired_bottom, desired_left] = [3, 5, 5, 3]
    assert [desired_upper, desired_right, desired_bottom, desired_left] == result


@patch(
    "app.core.services.image_manipulation"
    ".image_manipulation.consts.MINIMUM_RESOLUTION",
    40,
)
def test_parse_to_valid_image_invalid_image():
    result = image_manipulation.parse_to_valid_image(content=io.BytesIO(b""))
    assert result.size == (40, 40)
