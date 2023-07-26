# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
from uuid import UUID

from fastapi import APIRouter, Path, UploadFile, status
from fastapi.responses import Response
from pydantic import NonNegativeInt
from typing_extensions import Annotated

from app.core.resources.constants import message, service
from app.core.resources.data_validator import (
    AREA_REGEX,
    create_image_metadata_dict,
)
from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.resources.schemas.preview_image_metadata import PreviewImageMetadata
from app.core.resources.schemas.thumbnail_image_metadata import ThumbnailImageMetadata
from app.core.services import image_service

router = APIRouter(
    prefix=f"/{service.NAME}/{service.IMAGE_NAME}",
    tags=[service.IMAGE_NAME],
    responses={status.HTTP_400_BAD_REQUEST: {"description": message.INPUT_ERROR}},
)


@router.get(
    "/{id}/{version}/{area}/thumbnail/",
    responses={
        status.HTTP_502_BAD_GATEWAY: {
            "description": message.STORAGE_UNAVAILABLE_STRING,
        },
        status.HTTP_404_NOT_FOUND: {"description": message.ITEM_NOT_FOUND},
    },
)
async def get_thumbnail(
    id: UUID,
    version: NonNegativeInt,
    area: Annotated[str, Path(regex=AREA_REGEX)],
    service_type: ServiceTypeEnum,
    shape: ImageBorderShapeEnum = ImageBorderShapeEnum.RECTANGULAR,
    quality: ImageQualityEnum = ImageQualityEnum.MEDIUM,
    output_format: ImageTypeEnum = ImageTypeEnum.JPEG,
) -> Response:
    """
    Creates and returns a thumbnail of the image fetched by id and version
    with the given size, quality, format and shape.
    It will automatically crop the picture.
    - **id**: UUID of the image
    - **version**: version of the image
    - **quality**: quality of the output image
    (the higher you go the slower the process)
    - **output_format**: format of the output image
    - **area**: width of the output image (>=0) x
    height of the output image (>=0), width x height => 100x200.
    The first is width, the latter height, the order is important!
    - **shape**: Rounded and Rectangular are currently supported.
    - **service_type**: Service that owns the resource
     (service that first uploaded the data to storage)
    \f
    :param id: UUID of the image
    :param version: version of the image
    :param quality: quality of the output image
    :param output_format: format of the output image
    :param service_type: service that owns the resource
    :param area: height x width of the output image (both>=0)
    :param shape: Rounded and Rectangular are currently supported
    :return: 400 ok if there were invalid parameters, otherwise
    the requested image modified accordingly.
    """
    metadata_dict = create_image_metadata_dict(
        quality=quality,
        output_format=output_format,
        shape=shape,
        crop_position=VerticalCropPositionEnum.CENTER,
        area=area,
    )
    return await image_service.retrieve_image_and_create_thumbnail(
        image_id=str(id),
        version=version,
        img_metadata=ThumbnailImageMetadata(**metadata_dict),
        service_type=service_type,
    )


@router.post("/{area}/thumbnail/")
async def post_thumbnail(
    area: Annotated[str, Path(regex=AREA_REGEX)],
    file: UploadFile,
    shape: ImageBorderShapeEnum = ImageBorderShapeEnum.RECTANGULAR,
    quality: ImageQualityEnum = ImageQualityEnum.MEDIUM,
    output_format: ImageTypeEnum = ImageTypeEnum.JPEG,
) -> Response:
    """
    Creates and returns a thumbnail of the given image
    with the given size, quality, format and shape.
    - **quality**: quality of the output image
    (the higher you go the slower the process)
    - **output_format**: format of the output image
    - **area**: width of the output image (>=0) x
    height of the output image (>=0), width x height => 100x200.
    The first is width, the latter height, the order is important!
    - **shape**: Rounded and Rectangular are currently supported.
    This option will lose information, leaving it False will scale and
    have borders to fill the requested size.
    - **file**: file uploaded with FormData.
    \f
    :param shape: Rounded and Rectangular are currently supported
    :param quality: quality of the output image
    :param output_format: format of the output image
    :param area: height of the output image (>=0)
     and width of the output image (>=0)
     :param file: file to upload using a FormData
    :return: 400 if there were invalid parameters, otherwise
    the requested image modified accordingly.
    """
    metadata_dict = create_image_metadata_dict(
        quality=quality,
        output_format=output_format,
        shape=shape,
        crop_position=VerticalCropPositionEnum.CENTER,
        area=area,
    )
    return Response(
        content=(
            image_service.process_raw_thumbnail(
                raw_content=io.BytesIO(file.file.read()),
                img_metadata=ThumbnailImageMetadata(**metadata_dict),
            )
        ).read(),
        media_type=f"image/{output_format.value}",
    )


@router.post("/{area}/")
async def post_preview(
    area: Annotated[str, Path(regex=AREA_REGEX)],
    file: UploadFile,
    crop: bool = False,
    quality: ImageQualityEnum = ImageQualityEnum.MEDIUM,
    output_format: ImageTypeEnum = ImageTypeEnum.JPEG,
) -> Response:
    """
    Creates and returns a preview of the given image
    with the given size, quality and format
    - **quality**: quality of the output image
    (the higher you go the slower the process)
    - **output_format**: format of the output image
    - **area**: width of the output image (>=0) x
    height of the output image (>=0), width x height => 100x200.
    The first is width, the latter height, the order is important!
    - **crop**: True will crop the picture starting from the borders.
    This option will lose information, leaving it False will scale and
    have borders to fill the requested size.
    - **file**: file uploaded with FormData.
    \f
    :param crop: True will crop the picture starting from the borders
    :param quality: quality of the output image
    :param output_format: format of the output image
    :param area: height of the output image (>=0)
     and width of the output image (>=0)
     :param file: file to upload using a FormData
    :return: 400 if there were invalid parameters, otherwise
    the requested image modified accordingly.
    """
    metadata_dict = create_image_metadata_dict(
        quality=quality,
        output_format=output_format,
        crop=crop,
        crop_position=VerticalCropPositionEnum.CENTER,
        area=area,
    )

    return Response(
        content=(
            image_service.process_raw_preview(
                raw_content=io.BytesIO(file.file.read()),
                img_metadata=PreviewImageMetadata(**metadata_dict),
            )
        ).read(),
        media_type=f"image/{output_format.value}",
    )


@router.get(
    "/{id}/{version}/{area}/",
    responses={
        status.HTTP_502_BAD_GATEWAY: {
            "description": message.STORAGE_UNAVAILABLE_STRING,
        },
        status.HTTP_404_NOT_FOUND: {"description": message.ITEM_NOT_FOUND},
    },
)
async def get_preview(
    id: UUID,
    version: NonNegativeInt,
    area: Annotated[str, Path(regex=AREA_REGEX)],
    service_type: ServiceTypeEnum,
    crop: bool = False,
    quality: ImageQualityEnum = ImageQualityEnum.MEDIUM,
    output_format: ImageTypeEnum = ImageTypeEnum.JPEG,
) -> Response:
    """
    Creates and returns a preview of the image fetched by id and version
    with the given size, quality and format
    - **id**: UUID of the image
    - **version**: version of the image
    - **quality**: quality of the output image
    (the higher you go the slower the process)
    - **output_format**: format of the output image
    - **area**: width of the output image (>=0) x
    height of the output image (>=0), width x height => 100x200.
    The first is width, the latter height, the order is important!
    - **crop**: True will crop the picture starting from the borders.
    This option will lose information, leaving it False will scale and
    have borders to fill the requested size.
    - **service_type**: Service that owns the resource
    (service that first uploaded the data to storage)
    \f
    :param crop: True will crop the picture starting from the borders
    :param id: UUID of the image
    :param version: version of the image
    :param quality: quality of the output image
    :param output_format: format of the output image
    :param area: height of the output image (>=0)
     and width of the output image (>=0)
    :param service_type: service that owns the resource
    :return: 400 if there were invalid parameters, otherwise
    the requested image modified accordingly.
    """
    metadata_dict = create_image_metadata_dict(
        quality=quality,
        output_format=output_format,
        crop=crop,
        crop_position=VerticalCropPositionEnum.CENTER,
        area=area,
    )
    return await image_service.retrieve_image_and_create_preview(
        image_id=str(id),
        version=version,
        img_metadata=PreviewImageMetadata(**metadata_dict),
        service_type=service_type,
    )
