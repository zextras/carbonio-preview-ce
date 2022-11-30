# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import uuid
from typing import Optional

from starlette import status
from starlette.responses import Response

from app.core.resources.constants import message, service


def is_id_valid(file_id: str) -> bool:
    """
    Validate the id, compares it to the structure of UUID1 to UUID4
    \f
    :param file_id: id to validate
    :return: True if there was no error parsing it
    """
    try:
        uuid.UUID(file_id)
        return True
    except ValueError:
        return False


def check_for_storage_response_error(
    response_data: Optional[Response],
) -> Optional[Response]:
    """
    Checks if the storage response contains error and return them accordingly
    if no error is found return None
    \f
    :param response_data: response object to analyze
    :return: None if no error was found, else the error
    """
    if response_data is not None:
        if response_data.ok:
            return None
        else:
            return Response(
                content=message.GENERIC_ERROR_WITH_STORAGE,
                status_code=response_data.status_code,
            )
    else:
        return Response(
            content=message.STORAGE_UNAVAILABLE_STRING,
            status_code=status.HTTP_502_BAD_GATEWAY,
        )


def check_for_document_metadata_errors(
    first_page: int, last_page: int
) -> Optional[Response]:
    """
    Function that handle validation of the given document parameters
    \f
    :param first_page: number representing the first page to convert
    :param last_page: number representing the last page to convert
    :return: a Response with status code 400 and content with detailed explanation
    if there were invalid parameters, otherwise None
    """
    if first_page >= 1 and (first_page <= last_page or last_page == 0):
        return None
    else:
        return Response(
            content=message.NUMBER_OF_PAGES_NOT_VALID,
            status_code=status.HTTP_400_BAD_REQUEST,
        )


def check_for_image_metadata_errors(
    id: str = None,
    version: int = None,
    area: str = None,
    metadata_dict: dict = None,
) -> Optional[Response]:
    """
    Function that handle validation of the given image parameters
     and stores them in metadata_dict
     \f
    :param id: UUID of the image
    :param area: width x height
    :param version: version of the node
    :param metadata_dict: dictionary with optional arguments
     that will be filled with validated input
    :return: a Response with status code 400 and content with detailed explanation
    if there were invalid parameters, otherwise None
    """
    try:
        width, height = area.split("x")
    except ValueError:
        return Response(
            content=message.HEIGHT_OR_WIDTH_NOT_INSERTED_ERROR,
            status_code=status.HTTP_400_BAD_REQUEST,
        )
    try:
        width, height = int(width), int(height)
    except ValueError:
        return Response(
            content=message.HEIGHT_WIDTH_NOT_VALID_ERROR,
            status_code=status.HTTP_400_BAD_REQUEST,
        )
    if width >= 0 and height >= 0:
        if id is None or is_id_valid(file_id=id):
            if version is None or version > 0:
                metadata_dict["height"] = height
                metadata_dict["width"] = width
                metadata_dict["version"] = version
                return None
            else:
                return Response(
                    content=message.VERSION_NOT_VALID_ERROR,
                    status_code=status.HTTP_400_BAD_REQUEST,
                )
        else:
            return Response(
                content=message.ID_NOT_VALID_ERROR,
                status_code=status.HTTP_400_BAD_REQUEST,
            )
    else:
        return Response(
            content=message.HEIGHT_WIDTH_NOT_VALID_ERROR,
            status_code=status.HTTP_400_BAD_REQUEST,
        )


def check_if_document_thumbnail_is_enabled():
    """
    Checks if the document thumbnail option is enabled
    \f
    :return: a Response with status code 400 and content with
     detailed explanation if thumbnail is not enabled
    """
    return (
        None
        if service.ENABLE_DOCUMENT_THUMBNAIL
        else Response(
            content=message.DOCUMENT_THUMBNAIL_NOT_ENABLED_ERROR,
            status_code=status.HTTP_400_BAD_REQUEST,
        )
    )


def check_if_document_preview_is_enabled():
    """
    Checks if the document preview option is enabled
    \f
    :return: a Response with status code 400 and content with
     detailed explanation if preview is not enabled
    """
    return (
        None
        if service.ENABLE_DOCUMENT_PREVIEW
        else Response(
            content=message.DOCUMENT_PREVIEW_NOT_ENABLED_ERROR,
            status_code=status.HTTP_400_BAD_REQUEST,
        )
    )
