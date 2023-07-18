# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from typing import Dict, Final, Optional, Type

import pydantic
from fastapi import HTTPException, status
from fastapi.responses import Response as FastApiResp
from httpx import Response as RequestResp
from pydantic import BaseModel, NonNegativeInt
from returns.maybe import Maybe, Nothing

from app.core.resources.app_config import (
    ENABLE_DOCUMENT_PREVIEW,
    ENABLE_DOCUMENT_THUMBNAIL,
)
from app.core.resources.constants import message
from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)

PREVIEW_NOT_ENABLED_RESPONSE: Final[FastApiResp] = FastApiResp(
    content=message.DOCUMENT_PREVIEW_NOT_ENABLED_ERROR,
    status_code=status.HTTP_400_BAD_REQUEST,
)

THUMBNAIL_NOT_ENABLED_RESPONSE: Final[FastApiResp] = FastApiResp(
    content=message.DOCUMENT_THUMBNAIL_NOT_ENABLED_ERROR,
    status_code=status.HTTP_400_BAD_REQUEST,
)

AREA_REGEX: Final[str] = "^[0-9]+x[0-9]+$"


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
    response_data: Maybe[RequestResp],
) -> Maybe[FastApiResp]:
    """
    Checks if the storage response contains error and return them accordingly
    if no error is found return None
    \f
    :param response_data: response object to analyze
    :return: None if no error was found, else the error
    """
    status_code = response_data.value_or(
        FastApiResp(
            status_code=status.HTTP_502_BAD_GATEWAY,
        ),
    ).status_code
    if status.HTTP_200_OK <= status_code < status.HTTP_400_BAD_REQUEST:
        return Nothing

    return Maybe.from_value(
        FastApiResp(
            content=message.STORAGE_UNAVAILABLE_STRING
            if status_code >= status.HTTP_500_INTERNAL_SERVER_ERROR
            else message.GENERIC_ERROR_WITH_STORAGE,
            status_code=status_code,
        ),
    )


class DocumentPagesMetadataModel(BaseModel):
    """
    Model to validate for document metadata
    """

    first_page: NonNegativeInt = 1
    last_page: NonNegativeInt = 0

    @classmethod
    @pydantic.model_validator(mode="before")
    def first_page_must_be_less_than_last_page(
        cls: Type["DocumentPagesMetadataModel"],
        field_values: Dict[str, int],
    ) -> Dict[str, int]:
        first_page = field_values.get("first_page", 0)
        last_page = field_values.get("last_page", 0)
        if first_page >= 1 and (first_page <= last_page or last_page == 0):
            return field_values

        raise HTTPException(
            status_code=422,
            detail=message.NUMBER_OF_PAGES_NOT_VALID,
        )


def check_if_document_thumbnail_is_enabled() -> bool:
    """
    Checks if the document thumbnail option is enabled
    \f
    :return: True if enabled
    """
    return ENABLE_DOCUMENT_THUMBNAIL


def _check_if_document_preview_is_enabled() -> bool:
    """
    Checks if the document preview option is enabled
    \f
    :return: True if enabled
    """
    return ENABLE_DOCUMENT_PREVIEW


def get_document_preview_enabled_response_error() -> Maybe[FastApiResp]:
    """
    Checks if the document preview option is enabled
    \f
    :return: a Response with status code 400 and content with
     detailed explanation if preview is not enabled
    """

    return (
        Nothing
        if _check_if_document_preview_is_enabled()
        else Maybe.from_value(PREVIEW_NOT_ENABLED_RESPONSE)
    )
