# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
from typing import Optional, IO

from fastapi.responses import Response

from app.core.resources.constants import message
from app.core.resources.data_validator import check_for_storage_response_error
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum
from app.core.resources.schemas.preview_image_metadata import PreviewImageMetadata
from app.core.resources.schemas.thumbnail_image_metadata import ThumbnailImageMetadata
from app.core.services import storage_communication

from app.core.services.image_manipulation.jpeg_manipulation import (
    jpeg_preview,
    jpeg_thumbnail,
)
from app.core.services.image_manipulation.png_manipulation import (
    png_preview,
    png_thumbnail,
)


async def retrieve_image_and_create_thumbnail(
    image_id: str, img_metadata: ThumbnailImageMetadata, service_type: ServiceTypeEnum
) -> Response:
    """
    Contact storage and retrieves the image with the nodeid requested
    and calls process image.
    If the nodeid is not found returns Generic error specifying the error code
    :param image_id: UUID of the image
    :param img_metadata: Instance of ThumbnailImageMetadata class
    :param service_type: service that owns the resource
    :return response: a Response with metadata or error message.
    """
    response_data: Optional[Response] = await storage_communication.retrieve_data(
        file_id=image_id, version=img_metadata.version, service_type=service_type
    )
    return await _process_response_data(
        response_data=response_data,
        img_metadata=img_metadata,
        func=_select_thumbnail_module,
    )


async def retrieve_image_and_create_preview(
    image_id: str, img_metadata: PreviewImageMetadata, service_type: ServiceTypeEnum
) -> Response:
    """
    Contact storage and retrieves the image with the nodeid requested
    and calls process image.
    If the nodeid is not found returns Generic error specifying the error code
    :param image_id: UUID of the image
    :param img_metadata: Instance of PreviewImageMetadata class
    :param service_type: service that owns the resource
    :return response: a Response with metadata or error message.
    """
    response_data: Optional[Response] = await storage_communication.retrieve_data(
        file_id=image_id, version=img_metadata.version, service_type=service_type
    )
    return await _process_response_data(
        response_data=response_data,
        img_metadata=img_metadata,
        func=_select_preview_module,
    )


async def process_raw_thumbnail(
    raw_content: IO, img_metadata: ThumbnailImageMetadata
) -> io.BytesIO:
    """
    process a given raw image file as a thumbnail
    :param raw_content: content to process
    :param img_metadata: Instance of ThumbnailImageMetadata class
    """
    return _select_thumbnail_module(img_metadata=img_metadata, content=raw_content)


async def process_raw_preview(
    raw_content: IO, img_metadata: PreviewImageMetadata
) -> io.BytesIO:
    """
    process a given raw image file as a thumbnail
    :param raw_content: content to process
    :param img_metadata: Instance of PreviewImageMetadata class
    """
    return _select_preview_module(img_metadata=img_metadata, content=raw_content)


async def _process_response_data(
    response_data: Optional[Response], img_metadata, func
) -> Response:
    """
    Validates response data and then process calling func passed.
    If the storage is not available returns
    Storage unavailable error and related error code
    :param response_data:
    :param img_metadata:
    :param func:
    :return:
    """
    response_error: Optional[Response] = check_for_storage_response_error(
        response_data=response_data
    )
    return (
        response_error
        if response_error
        else Response(
            content=func(img_metadata=img_metadata, content=response_data.raw).read(),
            media_type=f"image/{img_metadata.format}",
        )
    )


def _select_thumbnail_module(
    img_metadata: ThumbnailImageMetadata, content: io.BytesIO
) -> io.BytesIO:
    """
    Based on the given format chooses the correct module to call
    :param img_metadata: Instance of PreviewImageMetadata class
    :param content: Raw bytes of the image
    :return: Raw bytes of the converted image
    :raises: ValueError if the format is not supported
    """
    _format = img_metadata.format
    if _format == ImageTypeEnum.JPEG:
        return jpeg_thumbnail(
            _x=img_metadata.width,
            _y=img_metadata.height,
            _quality=img_metadata.quality,
            border=img_metadata.shape,
            content=content,
            crop_position=img_metadata.crop_position,
        )
    elif _format == ImageTypeEnum.PNG:
        return png_thumbnail(
            _x=img_metadata.width,
            _y=img_metadata.height,
            border=img_metadata.shape,
            content=content,
            crop_position=img_metadata.crop_position,
        )
    else:
        raise ValueError(message.FORMAT_NOT_SUPPORTED_ERROR)


def _select_preview_module(
    img_metadata: PreviewImageMetadata, content: io.BytesIO
) -> io.BytesIO:
    """
    Based on the given format chooses the correct module to call
    :param img_metadata: Instance of PreviewImageMetadata class
    :param content: Raw bytes of the image
    :return: Raw bytes of the converted image
    :raises: ValueError if the format is not supported
    """
    _format = img_metadata.format
    if _format == ImageTypeEnum.JPEG:
        return jpeg_preview(
            _x=img_metadata.width,
            _y=img_metadata.height,
            _quality=img_metadata.quality,
            content=content,
            _crop=img_metadata.crop,
            crop_position=img_metadata.crop_position,
        )
    elif _format == ImageTypeEnum.PNG:
        return png_preview(
            _x=img_metadata.width,
            _y=img_metadata.height,
            content=content,
            _crop=img_metadata.crop,
            crop_position=img_metadata.crop_position,
        )
    else:
        raise ValueError(message.FORMAT_NOT_SUPPORTED_ERROR)
