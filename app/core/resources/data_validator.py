# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from typing import Optional, Dict

import pydantic
from fastapi import HTTPException
from pydantic import BaseModel, NonNegativeInt
from starlette import status
from starlette.responses import Response

from app.core.resources.constants import message, service
from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)

AREA_REGEX: str = "^[0-9]+x[0-9]+$"


def create_image_metadata_dict(
    quality: ImageQualityEnum,
    output_format: ImageTypeEnum,
    crop_position: VerticalCropPositionEnum,
    area: str,
    shape: Optional[ImageBorderShapeEnum] = None,
    crop: Optional[bool] = None,
) -> dict:
    """
    Helper function used to build an image metadata dict
    \f
    :param quality: valid value for "quality" key in dict
    :param output_format: valid value for "format" key in dict
    :param crop_position: valid value for "crop_position" key in dict
    :param area: string that follows the format (numberXnumber)
    :param shape: optional dict parameter for "shape" key in dict
    :param crop: optional dict parameter for "crop" key in dict
    """
    width, height = map(int, area.lower().split("x"))
    metadata_dict = {
        "quality": quality,
        "format": output_format,
        "crop_position": crop_position,
        "width": width,
        "height": height,
    }
    if shape is not None:
        metadata_dict["shape"] = shape
    if crop is not None:
        metadata_dict["crop"] = crop
    return metadata_dict


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


class DocumentPagesMetadataModel(BaseModel):
    """
    Model to validate for document metadata
    """

    first_page: NonNegativeInt = 1
    last_page: NonNegativeInt = 0

    @pydantic.root_validator()
    def first_page_must_be_less_than_last_page(
        cls, field_values: Dict[str, int]
    ) -> Dict[str, int]:
        first_page = field_values.get("first_page")
        last_page = field_values.get("last_page")
        if first_page >= 1 and (first_page <= last_page or last_page == 0):
            return field_values
        else:
            raise HTTPException(
                status_code=422, detail=message.NUMBER_OF_PAGES_NOT_VALID
            )


def check_if_document_thumbnail_is_enabled() -> Optional[Response]:
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


def check_if_document_preview_is_enabled() -> Optional[Response]:
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
