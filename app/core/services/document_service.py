# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
from typing import Optional

from fastapi import UploadFile
from starlette.responses import Response

from app.core.resources.data_validator import check_for_storage_response_error
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum
from app.core.services.document_manipulation import document_manipulation
from app.core.services.storage_communication import retrieve_data


async def retrieve_doc_and_create_preview(
    file_id: str,
    version: int,
    first_page_number: int,
    last_page_number: int,
    service_type: ServiceTypeEnum,
) -> Response:
    """
    Contact storage and retrieves the image with the nodeid requested
    and trims it to the number of pages requested.
    If the nodeid is not found returns Generic error specifying the error code
    :param file_id: UUID of the file
    :param version: version of the file
    :param first_page_number: first page to return
    :param last_page_number: last page to return
    :param service_type: service that owns the resource
    :return response: a Response with metadata or error message.
    """
    response_data: Optional[Response] = await retrieve_data(
        file_id=file_id, version=version, service_type=service_type
    )

    response_error: Optional[Response] = check_for_storage_response_error(
        response_data=response_data
    )
    if response_error:
        return response_error
    else:
        pdf: io.BytesIO = await document_manipulation.convert_to_pdf(
            first_page_number=first_page_number,
            last_page_number=last_page_number,
            content=io.BytesIO(response_data.content),
        )
        return Response(
            content=pdf.read(),
            media_type="application/pdf",
        )


async def create_preview_from_raw(
    file: UploadFile, first_page_number: int, last_page_number: int
) -> io.BytesIO:
    """
    Create pdf preview of a given file
    :param file: uploaded file to convert
    :param first_page_number: the first page of the pdf to return
    :param last_page_number: the last page of the pdf to return
    """
    return await document_manipulation.convert_to_pdf(
        first_page_number=first_page_number,
        last_page_number=last_page_number,
        content=file.file,
    )


async def create_thumbnail_from_raw(file: UploadFile, output_format: str) -> io.BytesIO:
    """
    Create image thumbnail of a given file
    :param file: uploaded file to convert
    :param output_format: the image type that the thumbnail will have
    """
    return await document_manipulation.convert_file_to(
        content=file.file, output_extension=output_format
    )


async def retrieve_doc_and_create_thumbnail(
    file_id: str, version: int, output_format: str, service_type: ServiceTypeEnum
) -> Response:
    """
    Contact storage and retrieves the document with the nodeid requested and converts it to image.
    If the nodeid is not found returns Generic error specifying the error code
    :param file_id: UUID of the file
    :param version: version of the file
    :param output_format: format
    :param service_type: service that owns the resource
    :return response: a Response with metadata or error message.
    """
    response_data: Optional[Response] = await retrieve_data(
        file_id=file_id, version=version, service_type=service_type
    )

    response_error: Optional[Response] = check_for_storage_response_error(
        response_data=response_data
    )
    if response_error:
        return response_error
    else:
        return Response(
            content=(
                await document_manipulation.convert_file_to(
                    content=io.BytesIO(response_data.content),
                    output_extension=output_format,
                )
            ).read(),
            media_type=f"image/{output_format}",
        )
