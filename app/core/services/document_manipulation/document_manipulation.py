# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
import logging
import sys
import pypdfium2

from typing import IO, Optional

from pdfrw import PdfReader, PdfWriter
from pdfrw.errors import PdfParseError
from starlette.exceptions import HTTPException

from app.core.resources import libre_office_handler
from app.core.resources.constants import service, settings
from app.core.resources.schemas.enums.image_quality_enum import ImageQualityEnum
from app.core.services.image_manipulation import image_manipulation

logger = logging.getLogger(__name__)


def split_pdf(
    content: io.BytesIO, first_page_number: int, last_page_number: int
) -> io.BytesIO:
    """
    Gets n pages of the pdf, with n = last_page_number - first_page_number.
    \f
    :param content: pdf to split
    :param first_page_number: first page to convert
    :param last_page_number: last page to convert
    :return: pdf with the first n pages
    """
    raw_content: bytes = content.read()
    pdf: PdfReader = _parse_if_valid_pdf(raw_content)
    if not pdf:
        return _write_pdf_to_buffer(None)  # returns empty pdf

    if first_page_number == 1 and last_page_number == 0 or "/Encrypt" in pdf.keys():
        # If the pdf is encrypted we cannot split it
        content.seek(0)
        return content
    pdf_pages: list = pdf.pages
    pdf_page_count: int = len(pdf_pages)
    start_page: int = first_page_number - 1  # metadata info starts
    # at 0 but first_page_number is >=1
    end_page: int = (
        last_page_number if 0 < last_page_number < pdf_page_count else pdf_page_count
    )
    return _write_pdf_to_buffer(pdf_pages, start_page, end_page)


def _parse_if_valid_pdf(raw_content: bytes) -> Optional[PdfReader]:
    """
    Parses the given bytes into PdfReader, if the file is not valid returns None
    \f
    :param raw_content: file to load into a PdfReader object
    :return: PdfReader object containing the pdf or Empty if not valid
    """
    pdf = None
    try:
        pdf = PdfReader(fdata=raw_content)
    except PdfParseError:  # not a valid pdf
        logger.debug("Not a valid pdf file, replacing it with an empty one")
    finally:
        return pdf


def _write_pdf_to_buffer(
    pdf: PdfReader = None, start_page: int = 0, end_page: int = 1
) -> io.BytesIO:
    """
    Writes file to PDF, if pdf is empty writes an empty pdf file
    \f
    :param pdf: PdfReader object containing the content to write
    :param start_page: first page to write
    :param end_page: last page to write
    :return: io.BytesIO object with pdf content written in it
    """
    buf: io.BytesIO = io.BytesIO()
    writer: PdfWriter = PdfWriter(buf)
    if pdf:
        writer.addpages(pdf[start_page:end_page])
    writer.write()
    buf.seek(0)
    return buf


async def convert_to_pdf(
    content: io.BytesIO,
    first_page_number: int,
    last_page_number: int,
    log: logging = logger,
) -> io.BytesIO:
    """
    Converts any LibreOffice supported format to pdf
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
    log: logging = logger,
) -> io.BytesIO:
    """
    Converts any LibreOffice supported format to any LibreOffice
     supported format using _convert_with_libre.
     SHOULD ALWAYS be used instead of _convert_with_libre
     as it isolates the connection with libre
    \f
    :param content: file to convert
    :param output_extension: output file, should be a format supported by LibreOffice
    :param log: logger to use
    """
    return await _convert_with_libre(
        content=content, output_extension=output_extension, log=log
    )


async def convert_pdf_to_image(
    content: IO, output_extension: str, page_number: int, log: logging = logger
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
        pil_image = page.render_topil()
        return image_manipulation.save_image_to_buffer(
            pil_image,
            output_extension,
            False,
            ImageQualityEnum.HIGHEST.get_jpeg_int_quality(),
            # the render is automatically done at the highest quality.
            # The desired quality will be set while processing the image
            # at the end of the api call because
            # doing it here will just increase method parameters and complexity,
            # without major performance improvements
        )
    except pypdfium2.PdfiumError as e:
        log.info(f"Wrong pdf file passed, error: {e}")
        raise HTTPException(status_code=400, detail="Invalid pdf file")


async def convert_pdf_to(
    content: IO,
    output_extension: str,
    first_page_number: int,
    last_page_number: int,
    log: logging = logger,
) -> io.BytesIO:
    """
    Converts pdf to any LibreOffice supported format
    \f
    :param content: pdf to convert
    :param output_extension: desired file output type
    :param first_page_number: first page to convert
    :param last_page_number: last page to convert
    :param log: logger to use
    """
    content: io.BytesIO = split_pdf(
        content=content,
        first_page_number=first_page_number,
        last_page_number=last_page_number,
    )
    return await convert_file_to(
        content=content, output_extension=output_extension, log=log
    )


async def _convert_with_libre(
    content: io.BytesIO, output_extension, log: logging
) -> io.BytesIO:
    """
    private method that implements conversion logic for every type of file,
    uses LibreOffice, SHOULD ONLY BE CALLED BY convert_file_to and
     never directly to isolate possible POF
    \f
    :param content: pdf to convert
    :param output_extension: desired file output type
    :param log: logger to use
    """
    office_port = libre_office_handler.libre_port
    log.debug(
        f"Converting file to {output_extension} "
        f"using LibreOffice instance on port {office_port}"
    )
    out_data = io.BytesIO()
    try:  # in case of empty file or libre exception
        future = libre_office_handler.executor.submit(
            libre_office_handler.libre_convert_handler,
            service.IP,
            office_port,
            content,
            output_extension,
        )
        out_data = io.BytesIO(future.result(timeout=settings.LIBRE_OFFICE_TIMEOUT))
        out_data.seek(0)
    except TimeoutError as time_error:
        logger.warning(f"LibreOffice is not responding.. error {time_error}")
        sys.exit(1)
    except Exception as e:
        logger.info(
            f"File to convert was corrupted, libre conversion failed with error {e}"
        )

    return out_data
