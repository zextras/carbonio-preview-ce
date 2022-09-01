# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
import unittest
from unittest.mock import MagicMock, patch

from PIL import ImageOps

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


class TestImageManipulation(unittest.TestCase):
    def setUp(self) -> None:
        super(TestImageManipulation, self).setUp()

    def tearDown(self) -> None:
        super(TestImageManipulation, self).tearDown()

    def test_save_image_to_buffer(self):
        img_to_save = MagicMock()
        img_to_save.save = MagicMock()
        ImageOps.exif_transpose = MagicMock(return_value=img_to_save)
        buffer = image_manipulation.save_image_to_buffer(img_to_save)
        self.assertEqual(1, img_to_save.save.call_count)
        self.assertEqual([], buffer.readlines())

    def test_find_scaled_dimension_higher_than_original_square_result(self):
        orig_x, orig_y = 346, 826
        result = image_manipulation._find_greater_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=1000,
            requested_y=1000,
        )

        self.assertEqual(
            truncate(orig_x / orig_y, 2), truncate(result[0] / result[1], 2)
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 2), truncate(result[1] / result[0], 2)
        )
        self.assertEqual(1000, result[0])
        self.assertEqual(2387, result[1])

    def test_find_scaled_dimension_higher_than_original(self):
        orig_x, orig_y = 346, 826
        result = image_manipulation._find_greater_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=700,
            requested_y=1000,
        )

        self.assertEqual(
            truncate(orig_x / orig_y, 2), truncate(result[0] / result[1], 2)
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 2), truncate(result[1] / result[0], 2)
        )
        self.assertEqual(700, result[0])
        self.assertEqual(1671, result[1])

    def test_find_scaled_dimension_lower_than_original(self):
        orig_x, orig_y = 346, 826
        result = image_manipulation._find_greater_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=300,
            requested_y=500,
        )

        self.assertEqual(
            truncate(orig_x / orig_y, 2), truncate(result[0] / result[1], 2)
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 2), truncate(result[1] / result[0], 2)
        )
        self.assertEqual(300, result[0])
        self.assertEqual(716, result[1])

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation._add_borders_to_crop"
    )
    def test_crop_image_image_same_size_as_requested(self, mock_borders):
        img_width, img_height = 300, 400
        req_x, req_y = 300, 400
        img_mock = MagicMock()
        img_mock.size = [img_width, img_height]
        with patch(
            "app.core.services.image_manipulation.image_manipulation._get_crop_coordinates"
        ) as coordinates_mock:
            coordinates_mock.return_value = [0, 0, 0, 0]
            image_manipulation._crop_image(
                img=img_mock,
                requested_x=req_x,
                requested_y=req_y,
                crop_position=VerticalCropPositionEnum.CENTER,
            )
            self.assertEqual(0, img_mock.call_count)
            self.assertEqual(1, img_mock.crop.call_count)
            self.assertEqual(1, mock_borders.call_count)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        30,
    )
    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation._add_borders_to_image"
    )
    def test_add_border_to_crop_image_image_more_then_requested(self, mock_borders):
        img_width, img_height = 500, 600
        req_x, req_y = 300, 400
        img_mock = MagicMock()
        img_mock.size = [img_width, img_height]
        image_manipulation._add_borders_to_crop(
            img=img_mock, requested_x=req_x, requested_y=req_y
        )
        self.assertEqual(mock_borders.call_count, 0)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        30,
    )
    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation._add_borders_to_image"
    )
    def test_add_border_to_crop_image_image_less_then_requested_more_than_min(
        self, mock_borders
    ):
        img_width, img_height = 200, 400
        req_x, req_y = 300, 400
        img_mock = MagicMock()
        img_mock.size = [img_width, img_height]
        image_manipulation._add_borders_to_crop(
            img=img_mock, requested_x=req_x, requested_y=req_y
        )
        self.assertEqual(mock_borders.call_count, 1)

    def test_find_smaller_scaled_dimension_with_higher_reqxy(self):
        # 826x346 => 1000x500
        orig_x, orig_y = 826, 346
        result = image_manipulation._find_smaller_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=1000,
            requested_y=500,
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 1), truncate(result[1] / result[0], 1)
        )
        self.assertEqual(1000, result[0])
        self.assertEqual(418, result[1])

    def test_find_smaller_scaled_dimension_req_x_double_y_half(self):
        orig_x, orig_y = 200, 100
        result = image_manipulation._find_smaller_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=400,
            requested_y=50,
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 1), truncate(result[1] / result[0], 1)
        )
        self.assertEqual(100, result[0])
        self.assertEqual(50, result[1])

    def test_find_smaller_scaled_dimension_1(self):
        orig_x, orig_y = 200, 100
        result = image_manipulation._find_smaller_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=800,
            requested_y=50,
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 1), truncate(result[1] / result[0], 1)
        )
        self.assertEqual(100, result[0])
        self.assertEqual(50, result[1])

    def test_find_smaller_scaled_dimension_2(self):
        orig_x, orig_y = 300, 200
        result = image_manipulation._find_smaller_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=1200,
            requested_y=800,
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 2), truncate(result[1] / result[0], 2)
        )
        self.assertEqual(1200, result[0])
        self.assertEqual(800, result[1])

    def test_find_smaller_scaled_dimension_3(self):
        orig_x, orig_y = 900, 1000
        result = image_manipulation._find_smaller_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=1700,
            requested_y=2000,
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 2), truncate(result[1] / result[0], 2)
        )
        self.assertEqual(1700, result[0])
        self.assertEqual(1888, result[1])

    def test_find_smaller_scaled_dimension_4(self):
        orig_x, orig_y = 4000, 200
        result = image_manipulation._find_smaller_scaled_dimensions(
            original_x=orig_x,
            original_y=orig_y,
            requested_x=8000,
            requested_y=399,
        )
        self.assertEqual(
            truncate(orig_y / orig_x, 2), truncate(result[1] / result[0], 2)
        )
        self.assertEqual(7980, result[0])
        self.assertEqual(399, result[1])

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        0,
    )
    def test_convert_requested_to_true_res_x_zero_y_zero(self):
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
        self.assertEqual(orig_x, to_scale_x)
        self.assertEqual(orig_y, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        0,
    )
    def test_convert_requested_to_true_res_x_zero_y_something(self):
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
        self.assertEqual(orig_x, to_scale_x)
        self.assertEqual(req_y, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        400,
    )
    def test_convert_requested_to_true_res_x_less_than_min(self):
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
        self.assertEqual(400, to_scale_x)
        self.assertEqual(400, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        400,
    )
    def test_convert_requested_to_true_res_y_less_than_min(self):
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
        self.assertEqual(400, to_scale_x)
        self.assertEqual(400, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        400,
    )
    def test_convert_requested_to_true_res_x_and_y_less_than_min(self):
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
        self.assertEqual(400, to_scale_x)
        self.assertEqual(400, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        80,
    )
    def test_convert_requested_to_true_res_origx_less_than_min_but_can_scale(self):
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
        self.assertEqual(req_x, to_scale_x)
        self.assertEqual(orig_y, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        80,
    )
    def test_convert_requested_to_true_res_origy_less_than_min_but_can_scale(self):
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
        self.assertEqual(orig_x, to_scale_x)
        self.assertEqual(req_y, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        80,
    )
    def test_convert_requested_to_true_res_origxy_less_than_min_but_can_scale(self):
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
        self.assertEqual(req_x, to_scale_x)
        self.assertEqual(req_y, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        80,
    )
    def test_convert_requested_to_true_res_origx_less_than_min_but_cannot_scale(self):
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
        self.assertEqual(80, to_scale_x)
        self.assertEqual(req_y, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        80,
    )
    def test_convert_requested_to_true_res_origy_less_than_min_but_cannot_scale(self):
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
        self.assertEqual(req_x, to_scale_x)
        self.assertEqual(80, to_scale_y)

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        80,
    )
    def test_convert_requested_to_true_res_origxy_less_than_min_but_cannot_scale(self):
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
        self.assertEqual(80, to_scale_x)
        self.assertEqual(80, to_scale_y)

    def test_get_crop_coordinates_top_fitting(self):
        result = image_manipulation._get_crop_coordinates(
            requested_x=10,
            requested_y=10,
            height=10,
            width=10,
            crop_position=VerticalCropPositionEnum.TOP,
        )
        [desired_upper, desired_right, desired_bottom, desired_left] = [0, 10, 10, 0]
        self.assertEqual(
            [desired_upper, desired_right, desired_bottom, desired_left], result
        )

    def test_get_crop_coordinates_top_not_enough_space(self):
        result = image_manipulation._get_crop_coordinates(
            requested_x=5,
            requested_y=5,
            height=10,
            width=10,
            crop_position=VerticalCropPositionEnum.TOP,
        )
        [desired_upper, desired_right, desired_bottom, desired_left] = [0, 5, 5, 3]
        self.assertEqual(
            [desired_upper, desired_right, desired_bottom, desired_left], result
        )

    def test_get_crop_coordinates_center_fitting(self):
        result = image_manipulation._get_crop_coordinates(
            requested_x=10,
            requested_y=10,
            height=10,
            width=10,
            crop_position=VerticalCropPositionEnum.CENTER,
        )
        [desired_upper, desired_right, desired_bottom, desired_left] = [0, 10, 10, 0]
        self.assertEqual(
            [desired_upper, desired_right, desired_bottom, desired_left], result
        )

    def test_get_crop_coordinates_center_not_enough_space(self):
        result = image_manipulation._get_crop_coordinates(
            requested_x=5,
            requested_y=5,
            height=10,
            width=10,
            crop_position=VerticalCropPositionEnum.CENTER,
        )
        [desired_upper, desired_right, desired_bottom, desired_left] = [3, 5, 5, 3]
        self.assertEqual(
            [desired_upper, desired_right, desired_bottom, desired_left], result
        )

    @patch(
        "app.core.services.image_manipulation"
        ".image_manipulation.consts.MINIMUM_RESOLUTION",
        40,
    )
    def test_parse_to_valid_image_invalid_image(self):
        result = image_manipulation._parse_to_valid_image(content=io.BytesIO(b""))
        self.assertEqual(result.size, (40, 40))
