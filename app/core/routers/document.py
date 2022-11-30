# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
from typing import Optional

from fastapi import APIRouter, UploadFile
from starlette import status
from starlette.responses import Response

from app.core.resources.constants import service, message
from app.core.resources.data_validator import (
    is_id_valid,
    check_for_image_metadata_errors,
    check_for_document_metadata_errors,
    check_if_document_preview_is_enabled,
    check_if_document_thumbnail_is_enabled,
)
from app.core.resources.schemas.enums.image_border_form_enum import ImageBorderShapeEnum
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum
from app.core.resources.schemas.enums.vertical_crop_position_enum import (
    VerticalCropPositionEnum,
)
from app.core.resources.schemas.thumbnail_image_metadata import ThumbnailImageMetadata
from app.core.services import image_service, document_service

router = APIRouter(
    prefix=f"/{service.NAME}/{service.DOC_NAME}",
    tags=[service.DOC_NAME],
    responses={404: {"description": message.ITEM_NOT_FOUND}},
)


@router.get(
    "/{id}/{version}/",
    responses={502: {"description": message.STORAGE_UNAVAILABLE_STRING}},
)
async def get_preview(
    id: str,
    version: int,
    service_type: ServiceTypeEnum,
    first_page: int = 1,
    last_page: int = 0,
) -> Response:
    """
    Create and returns a pdf preview of the given file,
    the pdf file will contain the first and last page given.
    With default values will return all the pages.
    - **id**: UUID of the file.
    - **version**: version of the file.
    - **first_page**: integer value of first page to preview (n>=1)
    - **last_page**: integer value of last page to preview  (0 = last of the file)
    - **service_type**: Service that owns the resource
    (service that first uploaded the data to storage)
    \f
    :param id: UUID of the file
    :param first_page: first page to convert
    :param last_page: last page to convert
    :param version: version of the file
    :param service_type: service that owns the resource
    :return: 400 if there were invalid parameters, otherwise
    the requested file converted accordingly to pdf.
    """

    document_preview_service_errors: Optional[
        Response
    ] = check_if_document_preview_is_enabled()
    if document_preview_service_errors:
        return document_preview_service_errors

    validation_errors: Optional[Response] = check_for_document_metadata_errors(
        first_page=first_page,
        last_page=last_page,
    )
    if validation_errors:
        return validation_errors
    else:
        if is_id_valid(file_id=id):
            return await document_service.retrieve_doc_and_create_preview(
                file_id=id,
                version=version,
                first_page_number=first_page,
                last_page_number=last_page,
                service_type=service_type,
            )
        else:
            return Response(
                content=message.ID_NOT_VALID_ERROR,
                status_code=status.HTTP_400_BAD_REQUEST,
            )


@router.post("/")
async def post_preview(
    file: UploadFile, first_page: int = 1, last_page: int = 0
) -> Response:
    """
    Create and returns a pdf preview of the given file,
    the pdf file will contain the first and last page given.
    With default values will return all the pages.
    - **file**: file uploaded with FormData.
    - **first_page**: integer value of first page to preview (n>=1)
    - **last_page**: integer value of last page to preview  (0 = last of the pdf)
    \f
    :param file: file uploaded with FormData
    :param first_page: integer value of first page to preview
    :param last_page: integer value of last page to preview
    :return: 400 if there were invalid parameters, otherwise
    the requested file converted accordingly to pdf.
    """
    document_preview_service_errors: Optional[
        Response
    ] = check_if_document_preview_is_enabled()
    if document_preview_service_errors:
        return document_preview_service_errors

    validation_errors: Optional[Response] = check_for_document_metadata_errors(
        first_page=first_page,
        last_page=last_page,
    )
    if validation_errors:
        return validation_errors
    else:
        return Response(
            content=(
                await document_service.create_preview_from_raw(
                    first_page_number=first_page, last_page_number=last_page, file=file
                )
            ).read(),
            media_type="application/pdf",
        )


@router.post(
    "/{area}/thumbnail/", responses={400: {"description": message.INPUT_ERROR}}
)
async def post_thumbnail(
    area: str,
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

    document_thumbnail_service_errors: Optional[
        Response
    ] = check_if_document_thumbnail_is_enabled()
    if document_thumbnail_service_errors:
        return document_thumbnail_service_errors

    metadata_dict = {
        "quality": quality,
        "format": output_format,
        "shape": shape,
        "crop_position": VerticalCropPositionEnum.TOP,
    }
    validation_errors: Optional[Response] = check_for_image_metadata_errors(
        area=area,
        metadata_dict=metadata_dict,
    )

    if validation_errors:
        return validation_errors
    else:
        content: io.BytesIO = await document_service.create_thumbnail_from_raw(
            file=file, output_format=output_format.value
        )
        return Response(
            content=(
                await image_service.process_raw_thumbnail(
                    raw_content=content,
                    img_metadata=ThumbnailImageMetadata(**metadata_dict),
                )
            ).read(),
            media_type=f"image/{output_format}",
        )


@router.get(
    "/{id}/{version}/{area}/thumbnail/",
    responses={502: {"description": message.STORAGE_UNAVAILABLE_STRING}},
)
async def get_thumbnail(
    id: str,
    version: int,
    area: str,
    service_type: ServiceTypeEnum,
    shape: ImageBorderShapeEnum = ImageBorderShapeEnum.RECTANGULAR,
    quality: ImageQualityEnum = ImageQualityEnum.MEDIUM,
    output_format: ImageTypeEnum = ImageTypeEnum.JPEG,
) -> Response:
    """
    Create and returns a thumbnail of the file fetched by id and version
    the image will be rendered from the first page.
    - **id**: UUID of the file.
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
    :param id: UUID of the file
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

    document_thumbnail_service_errors: Optional[
        Response
    ] = check_if_document_thumbnail_is_enabled()
    if document_thumbnail_service_errors:
        return document_thumbnail_service_errors

    metadata_dict = {
        "quality": quality,
        "format": output_format,
        "shape": shape,
        "crop_position": VerticalCropPositionEnum.TOP,
    }

    validation_errors: Optional[Response] = check_for_image_metadata_errors(
        area=area,
        metadata_dict=metadata_dict,
    )

    if validation_errors:
        return validation_errors
    else:

        image_response: Response = (
            await document_service.retrieve_doc_and_create_thumbnail(
                file_id=id,
                version=version,
                output_format=output_format.value,
                service_type=service_type,
            )
        )
        if image_response.status_code == 200:
            image_raw: io.BytesIO = io.BytesIO(image_response.body)
            return Response(
                content=(
                    await image_service.process_raw_thumbnail(
                        raw_content=image_raw,
                        img_metadata=ThumbnailImageMetadata(**metadata_dict),
                    )
                ).read(),
                media_type=f"image/{output_format}",
            )

        else:
            return image_response
