# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
from typing import Callable, Any

from fastapi.responses import Response as FastApiResp
from requests.models import Response as RequestResp
from returns.maybe import Maybe

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


def retrieve_image_and_create_thumbnail(
    image_id: str,
    version: int,
    img_metadata: ThumbnailImageMetadata,
    service_type: ServiceTypeEnum,
) -> FastApiResp:
    """
    Contact storage and retrieves the image with the file id requested
    and calls process image.
    If the file id is not found returns Generic error specifying the error code
    :param image_id: UUID of the image
    :param version: version of the file
    :param img_metadata: Instance of ThumbnailImageMetadata class
    :param service_type: service that owns the resource
    :return response: a Response with metadata or error message.
    """
    response_data: Maybe[RequestResp] = storage_communication.retrieve_data(
        file_id=image_id, version=version, service_type=service_type
    )
    return _process_response_data(
        response_data=response_data,
        img_metadata=img_metadata,
        func=_select_thumbnail_module,
    )


def retrieve_image_and_create_preview(
    image_id: str,
    version: int,
    img_metadata: PreviewImageMetadata,
    service_type: ServiceTypeEnum,
) -> FastApiResp:
    """
    Contact storage and retrieves the image with the file id requested
    and calls process image.
    If the file id is not found returns Generic error specifying the error code
    :param image_id: UUID of the image
    :param version: version of the file
    :param img_metadata: Instance of PreviewImageMetadata class
    :param service_type: service that owns the resource
    :return response: a Response with metadata or error message.
    """
    response_data: Maybe[RequestResp] = storage_communication.retrieve_data(
        file_id=image_id, version=version, service_type=service_type
    )
    return _process_response_data(
        response_data=response_data,
        img_metadata=img_metadata,
        func=_select_preview_module,
    )


def process_raw_thumbnail(
    raw_content: io.BytesIO, img_metadata: ThumbnailImageMetadata
) -> io.BytesIO:
    """
    process a given raw image file as a thumbnail
    :param raw_content: content to process
    :param img_metadata: Instance of ThumbnailImageMetadata class
    """
    return _select_thumbnail_module(img_metadata=img_metadata, content=raw_content)


def process_raw_preview(
    raw_content: io.BytesIO, img_metadata: PreviewImageMetadata
) -> io.BytesIO:
    """
    process a given raw image file as a thumbnail
    :param raw_content: content to process
    :param img_metadata: Instance of PreviewImageMetadata class
    """
    return _select_preview_module(img_metadata=img_metadata, content=raw_content)


def _process_response_data(
    response_data: Maybe[RequestResp], img_metadata: Any, func: Callable
) -> FastApiResp:
    """
    Validates response data and then process calling func passed.
    If the storage is not available returns
    Storage unavailable error and related error code
    :param response_data:
    :param img_metadata: object containing the image metadata fields.
    :param func:
    :return:
    """
    response_error: Maybe[FastApiResp] = check_for_storage_response_error(
        response_data=response_data
    )
    return response_error.value_or(
        FastApiResp(
            content=func(
                img_metadata=img_metadata,
                content=io.BytesIO(response_data.value_or(RequestResp()).content),
            ).read(),
            media_type=f"image/{img_metadata.format.value}",
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
