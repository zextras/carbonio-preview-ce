# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from unittest.mock import MagicMock

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
