# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import logging
from typing import List

import PIL
from PIL import Image, ImageDraw, ImageOps, ImageFilter

from app.core.resources.constants.image import constants as consts
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)

logger = logging.getLogger(__name__)


def save_image_to_buffer(
    img: Image.Image,
    _format: str = "JPEG",
    _optimize: bool = False,
    _quality_value: int = 0,
    log: logging = logger,
) -> io.BytesIO:
    """
    Saves the given image object to a buffer object,
    converting to the given format and to the given quality
    \f
    :param img: img to convert and save
    :param _format: format to save to
    :param _optimize: optimize the image or not (does not change quality)
    :param _quality_value: 0-95 quality value with 0 lowest 95 highest
    :param log: log to use, if missing it will use default class logger
    :return: buffer pointing at the start of the file, containing raw image
    """
    buffer = io.BytesIO()
    img.save(buffer, format=_format, optimize=_optimize, quality=_quality_value)
    buffer.seek(0)
    log.debug("PIL Image successfully saved to buffer.")
    return buffer


def _find_greater_scaled_dimensions(
    original_x: int,
    original_y: int,
    requested_x: int,
    requested_y: int,
    log: logging = logger,
) -> [int, int]:
    """
    Finds new width and new height that are >=
    than requested and with x/y == new width/new height
    \f
    :param original_x: Original width
    :param original_y: Original height
    :param requested_x: x to scale at least to
    :param requested_y: y to scale at least to
    :param log: log to use, if missing it will use default class logger
    :return: Tuple[width, height]
    """
    # if orig_x > requested_x and orig_y > requested_y:
    # return orig_x, orig_y
    # This will zoom too much in the image, we should still scale
    original_ratio = original_x / original_y
    new_ratio = requested_x / requested_y
    if new_ratio == original_ratio:
        log.debug("Same ratio, return requested x and y")
        return requested_x, requested_y
    # If the new ratio is higher, it means that the x grew proportionally
    # more than the y
    log.debug(
        f"Requested size is {requested_x} width/{requested_y} height,"
        f" with a ratio of {new_ratio}"
        f"Original image size is {original_x} width/{original_y} height,"
        f" with a ratio of {original_ratio}"
    )
    if new_ratio > original_ratio:
        return requested_x, int(requested_x * original_y / original_x)
    else:
        return int(requested_y * original_x / original_y), requested_y


def _find_smaller_scaled_dimensions(
    original_x: int,
    original_y: int,
    requested_x: int,
    requested_y: int,
    log: logging = logger,
) -> [int, int]:
    """
    Finds new width and new height that are <=
    than requested and with x/y == new width/new height
    \f
    :param original_x: Original width
    :param original_y: Original height
    :param requested_x: x to scale at least to
    :param requested_y: y to scale at least to
    :param log: log to use, if missing it will use default class logger
    :return: Tuple[width, height]
    """
    original_ratio = original_x / original_y
    new_ratio = requested_x / requested_y
    # images won't be stretched more than double.
    # If the new ratio is higher, it means that the x grew proportionally
    # more than the y
    log.debug(
        f"Requested size is {requested_x} width/{requested_y} height,"
        f" with a ratio of {new_ratio}"
        f"Original image size is {original_x} width/{original_y} height,"
        f" with a ratio of {original_ratio}"
    )
    if new_ratio > original_ratio:
        return int(requested_y * original_x / original_y), requested_y
    else:
        return requested_x, int(requested_x * original_y / original_x)


def _crop_image(
    img: Image.Image,
    requested_x: int,
    requested_y: int,
    crop_position: VerticalCropPositionEnum,
) -> Image.Image:
    """
    Crop the image to the requested width and height
    \f
    :param img: Image to crop
    :param requested_x: width to crop to, should be < than original width
    :param requested_y: height to crop to, should be < than original height
    :return: Cropped image
    """
    width, height = img.size

    [upper, right, bottom, left] = _get_crop_coordinates(
        requested_x, requested_y, height, width, crop_position
    )
    img = img.crop((left, upper, left + right, upper + bottom))
    img = _add_borders_to_crop(
        img=img, requested_x=requested_x, requested_y=requested_y
    )
    return img


def _get_crop_coordinates(
    requested_x: int,
    requested_y: int,
    height: int,
    width: int,
    crop_position: VerticalCropPositionEnum,
) -> List[int]:
    """
    Method that calculates the correct crop coordinates
     for the given image size and desired size
    \f
    :param requested_x: desired x
    :param requested_y: desired y
    :param height: original x
    :param width: original y
    :param crop_position: how to crop (from the top, from the bottom, from the center)
    :returns: [upper, right, bottom, left]
    """
    if crop_position.value == VerticalCropPositionEnum.CENTER:
        # The coordinate system starts from upper left,
        # so this will crop it from the center outwards
        right = requested_x if width > requested_x else width
        bottom = requested_y if height > requested_y else height
        # width/2 finds the center, but we need to draw
        # req_x/2 to the left and req_x/2 to the right
        # // returns int and not float like /
        left = width // 2 - requested_x // 2 if width > requested_x else 0
        upper = height // 2 - requested_y // 2 if height > requested_y else 0
    elif crop_position.value == VerticalCropPositionEnum.TOP:
        right = requested_x if width > requested_x else width
        bottom = requested_y if height > requested_y else height
        left = width // 2 - requested_x // 2 if width > requested_x else 0
        upper = 0
    else:
        [upper, right, bottom, left] = [0, 0, 0, 0]
    return [upper, right, bottom, left]


def _add_borders_to_crop(
    img: Image.Image, requested_x: int, requested_y: int
) -> Image.Image:
    """
    Optionally add borders to a cropped image when needed
    \f
    :param img: Image to refine
    :param requested_x: width to pad to
    :param requested_y: height to pad to
    :return: Image padded if needed
    """
    width, height = img.size
    if width < requested_x or height < requested_y:
        img = _add_borders_to_image(
            img=img,
            requested_x=requested_x
            if requested_x >= consts.MINIMUM_RESOLUTION
            else consts.MINIMUM_RESOLUTION,
            requested_y=requested_y
            if requested_y >= consts.MINIMUM_RESOLUTION
            else consts.MINIMUM_RESOLUTION,
        )
    return img


def _add_borders_to_image(img: Image.Image, requested_x: int, requested_y: int):
    """
    Add borders to the image to fill the requested width and height
    \f
    :param img: Image to fill with borders
    :param requested_x: width to fill to, should be <= than original width
    :param requested_y: height to fill to, should be <= than original height
    :return: Image filled to requested size with borders if necessary
    """
    width, height = img.size
    new_img = Image.new("RGB", (requested_x, requested_y))
    new_img.paste(
        img,
        (
            # // returns int and not float like /
            (requested_x - width) // 2,
            (requested_y - height) // 2,
        ),
    )
    return new_img


def _convert_requested_size_to_true_res_to_scale(
    requested_x: int,
    requested_y: int,
    original_width: int,
    original_height: int,
    log: logging = logger,
) -> [int, int]:
    """
    Converts requested size to original image size if requested size
    is 0 (if 100x0 it will become 100xOriginalHeight). Otherwise, if
    the requested size is lower than the minimum resolution it will
    be converted to the minimum resolution.
    \f
    :param requested_x: width to convert to
    :param requested_y: height to convert to
    :param original_width: base width
    :param original_height: base height
    :param log: log to use, if missing it will use default class logger
    :return: Tuple[converted_x, converted_y]
    """
    # check if it was requested to size 0, meaning to use the original size of the image.
    if requested_x == 0:
        requested_x = original_width
        log.debug("Using original width of fetched image.")
    if requested_y == 0:
        requested_y = original_height
        log.debug("Using original height of fetched image.")

    # Check if the requested size is higher than the minimum
    if requested_x < consts.MINIMUM_RESOLUTION:
        requested_x = consts.MINIMUM_RESOLUTION
        log.debug(
            f"requested width is too small."
            f"Resizing width to the minimum resolution of {consts.MINIMUM_RESOLUTION}"
        )
    if requested_y < consts.MINIMUM_RESOLUTION:
        requested_y = consts.MINIMUM_RESOLUTION
        log.debug(
            f"requested height is too small."
            f"Resizing height to the minimum resolution of {consts.MINIMUM_RESOLUTION}"
        )

    # check if the original image is lower than the minimum and the requested
    # is not higher than double the original, because if you have
    # original_x = 70, minimum 80 and requested_x = 100 this can still be converted,
    # However, if requested_x=200 then it is more than double, and it will not
    # be resized, leaving it under the minimum. On the other hand, setting everything
    # under the minimum to the minimum will lose information if it can still be resized.
    if original_width < consts.MINIMUM_RESOLUTION and requested_x / 2 > original_width:
        requested_x = consts.MINIMUM_RESOLUTION
        log.debug(
            f"original image width is too small and cannot"
            f" be resized to requested width without stretching."
            f"Setting the requested width to {consts.MINIMUM_RESOLUTION}"
        )
    if (
        original_height < consts.MINIMUM_RESOLUTION
        and requested_y / 2 > original_height
    ):
        requested_y = consts.MINIMUM_RESOLUTION
        log.debug(
            f"original image height is too small and "
            f"cannot be resized to requested width without stretching."
            f"Setting the requested height to {consts.MINIMUM_RESOLUTION}"
        )
    return requested_x, requested_y


def _parse_to_valid_image(content: io.BytesIO):
    """
    Parses an image into a valid Pil Image,
    if the image is empty returns empty MinxMin RGB image
    \f
    :param content: Image to parse
    :return parsed image or new empty image
    """
    try:
        return Image.open(content)
    except PIL.UnidentifiedImageError as e:
        logger.debug(f"Invalid or empty image caused error: {e}")
        return Image.new("RGB", (consts.MINIMUM_RESOLUTION, consts.MINIMUM_RESOLUTION))


def resize_with_crop_and_paddings(
    content: io.BytesIO,
    requested_x: int,
    requested_y: int,
    crop_position: VerticalCropPositionEnum = VerticalCropPositionEnum.CENTER,
) -> Image.Image:
    """
    Resize the image and crop it if necessary
    \f
    :param content: content to resize
    :param requested_x: width to resize to
    :param requested_y: height to resize to
    :param crop_position: where should the image zoom when cropped
    :return: PIL Image containing resized image to fit requested x and y
    """
    img: Image.Image = _parse_to_valid_image(content=content)
    original_width, original_height = img.size
    to_crop = False
    to_scale_x, to_scale_y = _convert_requested_size_to_true_res_to_scale(
        requested_x=requested_x,
        requested_y=requested_y,
        original_width=original_width,
        original_height=original_height,
    )
    # minimum resolution is already checked on convert_req_size_to_true
    if original_width >= requested_x / 2 and original_height >= requested_y / 2:
        new_width, new_height = _find_greater_scaled_dimensions(
            original_x=original_width,
            original_y=original_height,
            requested_x=to_scale_x,
            requested_y=to_scale_y,
        )
        to_crop = True
    elif original_width <= to_scale_x / 2 and original_height <= to_scale_y / 2:
        new_width, new_height = original_width, original_height
    else:
        new_width, new_height = _find_smaller_scaled_dimensions(
            original_x=original_width,
            original_y=original_height,
            requested_x=to_scale_x,
            requested_y=to_scale_y,
        )
    img = img.resize((new_width, new_height))

    if to_crop:
        img = _crop_image(
            img=img,
            requested_x=to_scale_x,
            requested_y=to_scale_y,
            crop_position=crop_position,
        )
    else:
        img = _add_borders_to_image(
            img=img,
            requested_x=to_scale_x
            if to_scale_x >= consts.MINIMUM_RESOLUTION
            else consts.MINIMUM_RESOLUTION,
            requested_y=to_scale_y
            if to_scale_y >= consts.MINIMUM_RESOLUTION
            else consts.MINIMUM_RESOLUTION,
        )

    return img


def resize_with_paddings(
    content: io.BytesIO, requested_x: int, requested_y: int
) -> Image.Image:
    """
    Resize the image and add borders it if necessary
    \f
    :param content: content to resize
    :param requested_x: width to resize to
    :param requested_y: height to resize to
    :return: PIL Image containing resized image to fit requested x and y
    """
    img: Image.Image = _parse_to_valid_image(content=content)
    original_width, original_height = img.size
    to_scale_x, to_scale_y = _convert_requested_size_to_true_res_to_scale(
        requested_x=requested_x,
        requested_y=requested_y,
        original_width=original_width,
        original_height=original_height,
    )
    if (
        consts.MINIMUM_RESOLUTION <= original_width <= to_scale_x / 2
        and consts.MINIMUM_RESOLUTION <= original_height <= to_scale_y / 2
    ):
        # return original_x, original_y
        new_width, new_height = original_width, original_height
    else:
        new_width, new_height = _find_smaller_scaled_dimensions(
            original_x=original_width,
            original_y=original_height,
            requested_x=to_scale_x,
            requested_y=to_scale_y,
        )
    img = img.resize((new_width, new_height))
    img = _add_borders_to_image(
        img=img,
        requested_x=to_scale_x
        if to_scale_x >= consts.MINIMUM_RESOLUTION
        else consts.MINIMUM_RESOLUTION,
        requested_y=to_scale_y
        if to_scale_y >= consts.MINIMUM_RESOLUTION
        else consts.MINIMUM_RESOLUTION,
    )
    return img


def add_circle_margins_to_image(img: Image.Image) -> Image.Image:
    """
    Adds circle margins to the image
    \f
    :param img: image to add circles to
    :return: modified image
    """
    size = img.size
    mask = Image.new("L", size, 255)
    draw = ImageDraw.Draw(mask)
    draw.ellipse((0, 0) + size, fill=0)

    output_image = ImageOps.fit(img, mask.size, centering=(0.5, 0.5))
    output_image.paste(0, mask=mask)
    return output_image


def add_circle_margins_with_transparency(
    img: Image.Image, blur_radius: int, offset: int = 0
) -> Image.Image:
    """
    Adds circle margins with transparency
    \f
    :param img: Image to blurr the borders to
    :param blur_radius: how much to blur
    :param offset: offset
    :return: Image blurred
    """
    offset = blur_radius * 2 + offset
    mask = Image.new("L", img.size, 0)
    draw = ImageDraw.Draw(mask)
    draw.ellipse((offset, offset, img.size[0] - offset, img.size[1] - offset), fill=255)
    mask = mask.filter(ImageFilter.GaussianBlur(blur_radius))

    result = img.copy()
    result.putalpha(mask)

    return result
