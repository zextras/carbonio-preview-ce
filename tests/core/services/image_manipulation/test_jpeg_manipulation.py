# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import unittest
from unittest import mock
from unittest.mock import patch, MagicMock

from app.core.services.image_manipulation import jpeg_manipulation


class TestJpegManipulation(unittest.TestCase):
    def setUp(self) -> None:
        super(TestJpegManipulation, self).setUp()

    def tearDown(self) -> None:
        super(TestJpegManipulation, self).tearDown()

    @patch(
        "app.core.services.image_manipulation." "jpeg_manipulation.save_image_to_buffer"
    )
    def test_jpeg_compression_rgb_success_with_crop(self, mock_save):
        with mock.patch(
            "app.core.services.image_manipulation."
            "jpeg_manipulation.resize_with_crop_and_paddings"
        ) as mock_resize:
            mock_quality = MagicMock()
            jpeg_manipulation.jpeg_preview(
                _x=0, _y=0, _quality=mock_quality, content=None, _crop=True
            )
            self.assertEqual(1, mock_resize.call_count)
            self.assertEqual(1, mock_save.call_count)

    @patch(
        "app.core.services.image_manipulation." "jpeg_manipulation.save_image_to_buffer"
    )
    def test_jpeg_compression_rgb_success_without_crop(self, mock_save):
        with mock.patch(
            "app.core.services.image_manipulation."
            "jpeg_manipulation.resize_with_paddings"
        ) as mock_resize:
            mock_quality = MagicMock()
            jpeg_manipulation.jpeg_preview(
                _x=0, _y=0, _quality=mock_quality, content=None, _crop=False
            )
            self.assertEqual(1, mock_save.call_count)
            self.assertEqual(1, mock_resize.call_count)

    @patch(
        "app.core.services.image_manipulation." "jpeg_manipulation.save_image_to_buffer"
    )
    def test_jpeg_compression_rgba_success_with_crop(self, mock_save_to_buffer):
        with mock.patch(
            "app.core.services.image_manipulation."
            "jpeg_manipulation.resize_with_crop_and_paddings"
        ) as mock_resize:
            mock_resize().mode = "RGBA"
            mock_resize().convert = MagicMock()
            mock_quality = MagicMock()
            jpeg_manipulation.jpeg_preview(
                _x=0, _y=0, _quality=mock_quality, content=None, _crop=True
            )
            self.assertEqual(1, mock_save_to_buffer.call_count)
            self.assertEqual(3, mock_resize.call_count)
            self.assertEqual(1, mock_resize().convert.call_count)

    @patch(
        "app.core.services.image_manipulation." "jpeg_manipulation.save_image_to_buffer"
    )
    def test_jpeg_compression_rgba_success_without_crop(self, mock_save_to_buffer):
        with mock.patch(
            "app.core.services.image_manipulation."
            "jpeg_manipulation.resize_with_paddings"
        ) as mock_resize:
            mock_resize().mode = "RGBA"
            mock_resize().convert = MagicMock()
            mock_quality = MagicMock()
            jpeg_manipulation.jpeg_preview(
                _x=0, _y=0, _quality=mock_quality, content=None, _crop=False
            )
            self.assertEqual(1, mock_save_to_buffer.call_count)
            self.assertEqual(3, mock_resize.call_count)
            self.assertEqual(1, mock_resize().convert.call_count)
