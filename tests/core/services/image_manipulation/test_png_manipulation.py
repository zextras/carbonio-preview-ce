# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import unittest
from unittest import mock
from unittest.mock import patch

from app.core.services.image_manipulation import png_manipulation


class TestPngManipulation(unittest.TestCase):
    def setUp(self) -> None:
        super(TestPngManipulation, self).setUp()

    def tearDown(self) -> None:
        super(TestPngManipulation, self).tearDown()

    @patch(
        "app.core.services.image_manipulation." "png_manipulation.save_image_to_buffer"
    )
    def test_png_compression_rgb_success_with_crop(self, mock_save_to_buffer):
        with mock.patch(
            "app.core.services.image_manipulation."
            "png_manipulation"
            ".resize_with_crop_and_paddings"
        ) as mock_resize:
            png_manipulation.png_preview(_x=0, _y=0, content=None, _crop=True)
            self.assertEqual(1, mock_save_to_buffer.call_count)
            self.assertEqual(1, mock_resize.call_count)

    @patch(
        "app.core.services.image_manipulation." "png_manipulation.save_image_to_buffer"
    )
    def test_png_compression_rgb_success_without_crop(self, mock_save):
        with mock.patch(
            "app.core.services.image_manipulation."
            "png_manipulation"
            ".resize_with_paddings"
        ) as mock_resize:
            png_manipulation.png_preview(_x=0, _y=0, content=None, _crop=False)
            self.assertEqual(1, mock_save.call_count)
            self.assertEqual(1, mock_resize.call_count)
