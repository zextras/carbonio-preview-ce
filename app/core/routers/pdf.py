# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
from uuid import UUID

from fastapi import APIRouter, Depends, Path, UploadFile, status
from fastapi.responses import Response
from pydantic import NonNegativeInt
from typing_extensions import Annotated

from app.core.resources.app_config import PDF_NAME, SERVICE_NAME
from app.core.resources.constants import message
from app.core.resources.data_validator import (
    AREA_REGEX,
    DocumentPagesMetadataModel,
    create_image_metadata_dict,
)
from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.resources.schemas.thumbnail_image_metadata import ThumbnailImageMetadata
from app.core.services import image_service, pdf_service

router = APIRouter(
    prefix=f"/{SERVICE_NAME}/{PDF_NAME}",
    tags=[PDF_NAME],
    responses={status.HTTP_404_NOT_FOUND: {"description": message.ITEM_NOT_FOUND}},
)


@router.get(
    "/{id}/{version}/",
    responses={
        status.HTTP_502_BAD_GATEWAY: {
            "description": message.STORAGE_UNAVAILABLE_STRING,
        },
    },
)
async def get_preview(
    id: UUID,
    version: NonNegativeInt,
    service_type: ServiceTypeEnum,
    pages: DocumentPagesMetadataModel = Depends(),
) -> Response:
    """
    Create and returns a preview of the given file,
    the pdf file will contain the first and last page given.
    With default values will return all the pages.
    - **id**: UUID of the pdf.
    - **version**: version of the pdf.
    - **first_page**: integer value of first page to preview (n>=1)
    - **last_page**: integer value of last page to preview  (0 = last of the pdf)
    - **service_type**: Service that owns the resource
    (service that first uploaded the data to storage)
    \f
    :param id: UUID of the pdf
    :param pages: first and last page to convert
    :param version: version of the file
    :param service_type: service that owns the resource
    :return: 400 if there were invalid parameters, otherwise
    the requested pdf divided accordingly.
    """
    return await pdf_service.retrieve_pdf_and_create_preview(
        file_id=str(id),
        version=version,
        first_page_number=pages.first_page,
        last_page_number=pages.last_page,
        service_type=service_type,
    )


@router.post("/")
async def post_preview(
    file: UploadFile,
    pages: DocumentPagesMetadataModel = Depends(),
) -> Response:
    """
    Create and returns a preview of the given file,
    the pdf file will contain the first and last page given.
    With default values will return all the pages.
    - **file**: file uploaded with FormData.
    - **first_page**: integer value of first page to preview (n>=1)
    - **last_page**: integer value of last page to preview  (0 = last of the pdf)
    \f
    :param file: file uploaded with FormData
    :param pages: integer value of first page and last to preview
    :return: 400 if there were invalid parameters, otherwise
    the requested pdf divided accordingly.
    """

    return Response(
        content=(
            pdf_service.create_preview_from_raw(
                first_page_number=pages.first_page,
                last_page_number=pages.last_page,
                file=file,
            )
        ).read(),
        media_type="application/pdf",
    )


@router.post(
    "/{area}/thumbnail/",
    responses={status.HTTP_400_BAD_REQUEST: {"description": message.INPUT_ERROR}},
)
async def post_thumbnail(
    area: Annotated[str, Path(regex=AREA_REGEX)],
    file: UploadFile,
    shape: ImageBorderShapeEnum = ImageBorderShapeEnum.RECTANGULAR,
    quality: ImageQualityEnum = ImageQualityEnum.MEDIUM,
    output_format: ImageTypeEnum = ImageTypeEnum.JPEG,
) -> Response:
    """
    Create and returns the thumbnail of the given file,
    the image rendered will be the first page.
    - **quality**: quality of the output image
    (the higher you go the slower the process)
    - **output_format**: format of the output image
    - **area**: width of the output image (>=0) x
    height of the output image (>=0), width x height => 100x200.
    The first is width, the latter height, the order is important!
    - **shape**: Rounded and Rectangular are currently supported.
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
        crop_position=VerticalCropPositionEnum.TOP,
        area=area,
    )

    content: io.BytesIO = pdf_service.create_thumbnail_from_raw(
        file=file,
        output_format=output_format.value,
    )
    return Response(
        content=(
            image_service.process_raw_thumbnail(
                raw_content=content,
                img_metadata=ThumbnailImageMetadata(**metadata_dict),
            )
        ).read(),
        media_type=f"image/{output_format}",
    )


@router.get(
    "/{id}/{version}/{area}/thumbnail/",
    responses={
        status.HTTP_502_BAD_GATEWAY: {
            "description": message.STORAGE_UNAVAILABLE_STRING,
        },
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
    Create and returns a thumbnail of the file fetched by id and version
    the image will be rendered from the first page.
    - **id**: UUID of the pdf.
    - **version**: version of the file.
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
    :param id: UUID of the pdf
    :param version: version of the file
    :param service_type: service that owns the resource
    :param shape: Rounded and Rectangular are currently supported
    :param quality: quality of the output image
    :param output_format: format of the output image
    :param area: height of the output image (>=0)
     and width of the output image (>=0)
    :param service_type: service that owns the resource
    :return: 400 if there were invalid parameters, otherwise
    the requested pdf modified accordingly.
    """
    metadata_dict = create_image_metadata_dict(
        quality=quality,
        output_format=output_format,
        shape=shape,
        crop_position=VerticalCropPositionEnum.TOP,
        area=area,
    )

    image_response: Response = await pdf_service.retrieve_pdf_and_create_thumbnail(
        file_id=str(id),
        version=version,
        output_format=output_format.value,
        service_type=service_type,
    )
    if image_response.status_code == status.HTTP_200_OK:
        image_raw: io.BytesIO = io.BytesIO(image_response.body)
        return Response(
            content=(
                image_service.process_raw_thumbnail(
                    raw_content=image_raw,
                    img_metadata=ThumbnailImageMetadata(**metadata_dict),
                )
            ).read(),
            media_type=f"image/{output_format}",
        )

    return image_response
