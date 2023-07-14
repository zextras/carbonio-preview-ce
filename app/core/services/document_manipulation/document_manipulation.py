# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
import logging

import httpx
import pypdfium2
from fastapi.exceptions import HTTPException
from pypdfium2 import PdfDocument, PdfiumError
from returns.result import Failure, Result, Success

from app.core.resources.constants import document_conversion, service
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.services.image_manipulation import image_manipulation

logger = logging.getLogger(__name__)


def split_pdf(
    content: io.BytesIO,
    first_page_number: int,
    last_page_number: int,
) -> io.BytesIO:
    """
    Gets n pages of the pdf, with n = last_page_number - first_page_number.
    \f
    :param content: pdf to split
    :param first_page_number: first page to convert
    :param last_page_number: last page to convert
    :return: pdf with the first n pages
    """
    pdf: PdfDocument = _parse_if_valid_pdf(content).value_or(PdfDocument.new())
    start_page: int = first_page_number - 1
    end_page: int = last_page_number if 0 < last_page_number < len(pdf) else len(pdf)
    return _write_pdf_to_buffer(pdf, start_page, end_page)


def _parse_if_valid_pdf(content: io.BytesIO) -> Result[PdfDocument, PdfiumError]:
    """
    Parses the given buffer of bytes into PdfReader,
     if the file is not valid returns None
    \f
    :param content: file to load into a PdfDocument object
    :return: PdfReader object containing the pdf or Empty if not valid
    """
    try:
        return Success(PdfDocument(content))
    except PdfiumError as e:  # not a valid pdf
        logger.warning(
            f"Not a valid pdf file, replacing it with an empty one. Error: {e}",
        )
        return Failure(e)


def _write_pdf_to_buffer(
    pdf: PdfDocument,
    start_page: int = 0,
    end_page: int = 1,
) -> io.BytesIO:
    """
    Writes PDF to io.BytesIO buffer, if PDF is empty writes an empty pdf file
    \f
    :param pdf: PdfDocument containing the content to write
    :param start_page: first page to write
    :param end_page: last page to write
    :return: io.BytesIO object with pdf content written in it
    """
    out_pdf: PdfDocument = PdfDocument.new()
    buf: io.BytesIO = io.BytesIO()

    end_page = len(pdf) if end_page == 0 else end_page
    if start_page == 0 and end_page == len(pdf):
        out_pdf = pdf
    else:
        out_pdf.import_pages(pdf, list(range(start_page, end_page)))

    out_pdf.save(buf)
    buf.seek(0)
    return buf


async def convert_to_pdf(
    content: io.BytesIO,
    first_page_number: int,
    last_page_number: int,
    log: logging.Logger = logger,
) -> io.BytesIO:
    """
    Converts any Carbonio-docs-editor supported format to pdf
    \f
    :param content: file to convert
    :param first_page_number: first page to convert
    :param last_page_number: last page to convert
    :param log: logger to use
    """

    return split_pdf(
        content=await convert_file_to(
            content=content,
            output_extension="pdf",
            log=log,
        ),
        first_page_number=first_page_number,
        last_page_number=last_page_number,
    )


async def convert_file_to(
    content: io.BytesIO,
    output_extension: str,
    log: logging.Logger = logger,
) -> io.BytesIO:
    """
    Converts any Carbonio-docs-editor supported format to any Carbonio-docs-editor
     supported format using _convert_with_libre.
     SHOULD ALWAYS be used instead of _convert_with_libre
     as it isolates the connection with libre
    \f
    :param content: file to convert
    :param output_extension: output file,
     should be a format supported by Carbonio-docs-editor
    :param log: logger to use
    """
    return await _convert_with_libre(
        content=content,
        output_extension=output_extension,
        log=log,
    )


def convert_pdf_to_image(
    content: io.BytesIO,
    output_extension: str,
    page_number: int,
    log: logging.Logger = logger,
) -> io.BytesIO:
    """
    Converts pdf to any image supported format using PDFium
    \f
    :param content: pdf to convert
    :param output_extension: desired file output type
    :param page_number: first page to convert
    :param log: logger to use
    """
    try:
        pdf = pypdfium2.PdfDocument(content)
        page = pdf.get_page(page_number)
        pil_image = page.render().to_pil()
        return image_manipulation.save_image_to_buffer(
            img=pil_image,
            _format=output_extension,
            _optimize=False,
            _quality_value=ImageQualityEnum.HIGHEST.get_jpeg_int_quality(),
            # the render is automatically done at the highest quality.
            # The desired quality will be set while processing the image
            # at the end of the api call because
            # doing it here will just increase method parameters and complexity,
            # without major performance improvements
        )
    except pypdfium2.PdfiumError as e:
        log.info(f"Wrong pdf file passed, error: {e}")
        raise HTTPException(status_code=400, detail="Invalid pdf file") from e


async def convert_pdf_to(
    content: io.BytesIO,
    output_extension: str,
    first_page_number: int,
    last_page_number: int,
    log: logging.Logger = logger,
) -> io.BytesIO:
    """
    Converts pdf to any Carbonio-docs-editor supported format
    \f
    :param content: pdf to convert
    :param output_extension: desired file output type
    :param first_page_number: first page to convert
    :param last_page_number: last page to convert
    :param log: logger to use
    """
    out_content: io.BytesIO = split_pdf(
        content=content,
        first_page_number=first_page_number,
        last_page_number=last_page_number,
    )
    return await convert_file_to(
        content=out_content,
        output_extension=output_extension,
        log=log,
    )


async def _convert_with_libre(
    content: io.BytesIO,
    output_extension: str,
    log: logging.Logger,
) -> io.BytesIO:
    output_extension = _sanitize_output_extension(output_extension)

    url = f"{document_conversion.FULL_CONVERT_ADDRESS}/{output_extension}"

    files = {"files": ("docs-editor-file", content)}
    out_data = io.BytesIO()

    try:
        async with httpx.AsyncClient() as client:
            response = await client.post(url, timeout=service.DOCS_TIMEOUT, files=files)
            response.raise_for_status()
            out_data = io.BytesIO(response.content)
    except httpx.HTTPStatusError as http_error:
        log.debug(f"Http Error: {http_error}")
    # from this onward are not related to the raise_for_status,
    # these are all critical errors.
    except httpx.ConnectTimeout as timeout_error:
        log.error(f"Timeout Error: {timeout_error}")
    except httpx.RequestError as request_error:
        log.critical(f"Unexpected Error: {request_error}")
    except Exception as crit_err:
        log.critical(f"Critical Error: {crit_err}")

    out_data.seek(0)
    return out_data


def _sanitize_output_extension(output_extension: str) -> str:
    """Checks if the output extension is valid for conversion.
    Some extensions like jpeg are not supported and will be swapped with png
    \f
    :param output_extension: extension to check
    :return the given output_extension or a valid alternative
    """
    return (
        "png"
        if output_extension.upper() in ImageTypeEnum.__members__
        else output_extension
    )
