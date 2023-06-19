# SPDX-FileCopyrightText: 2023 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
from unittest.mock import MagicMock

import pytest
from PIL import Image

from app.core.services.image_manipulation import gif_utility_functions


def test_is_img_a_gif_not_a_valid_img_should_return_false():
    assert gif_utility_functions.is_img_a_gif(None) is False


def test_is_img_a_gif_a_valid_img_but_not_gif_should_return_false():
    assert gif_utility_functions.is_img_a_gif(Image.new("RGB", (80, 80))) is False


def test_is_img_a_gif_a_valid_gif_should_return_true():
    gif = MagicMock()
    gif.is_animated = True
    assert gif_utility_functions.is_img_a_gif(gif) is True


def test_parse_to_gif_with_invalid_content():
    with pytest.raises(ValueError):
        gif_utility_functions.parse_to_valid_gif(None)


def test_parse_to_gif_with_invalid_image_content():
    with pytest.raises(ValueError):
        content: io.BytesIO = io.BytesIO()
        Image.new("RGB", (80, 80)).save(content)
        gif_utility_functions.parse_to_valid_gif(content)
