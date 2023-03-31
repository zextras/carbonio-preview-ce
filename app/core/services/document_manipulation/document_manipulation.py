# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
import logging
import pypdfium2

from typing import IO, Optional, Generator

import requests
from pdfrw import PdfReader, PdfWriter
from pdfrw.errors import PdfParseError
from starlette.exceptions import HTTPException

from app.core.resources.constants import document_conversion
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
    content.seek(_get_sanitize_offset(content))
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
    except PdfParseError as e:  # not a valid pdf
        logger.warning(
            f"Not a valid pdf file, replacing it with an empty one. Error: {e}"
        )
    finally:
        return pdf


def _write_pdf_to_buffer(
    pdf: list = None, start_page: int = 0, end_page: int = 1
) -> io.BytesIO:
    """
    Writes file to PDF, if pdf is empty writes an empty pdf file
    \f
    :param pdf: list of PdfReader pages containing the content to write
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
    log: logging = logger,
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


def _get_sanitize_offset(buffer: io.BytesIO, pattern_to_find: bytes = b"%PDF") -> int:
    """
    Calculates how many bytes to skip to avoid extra headers, it continues until
    it finds the pattern requested
    \f
    :param buffer: pdf to search inside, the pattern MUST be of
    the same type of the buffer
    :param pattern_to_find: pattern to search for inside the pdf,
    every byte before the pattern will be summed up and returned
    :returns: number of bytes before the pattern to find
    """
    offset = 0
    # usually the extra headers are not many,
    # it would be a waste to read all the file
    for line in _read_buffer_line_by_line(buffer):
        if line.startswith(pattern_to_find):
            break
        else:
            offset_in_str = line.find(pattern_to_find)
            if offset_in_str != -1:
                offset += offset_in_str
                break
            else:
                offset += len(line)
    return offset


def _read_buffer_line_by_line(buffer: IO) -> Generator:
    while True:
        curr_line = buffer.readline()
        if not curr_line:
            break
        yield curr_line


async def convert_pdf_to(
    content: io.BytesIO,
    output_extension: str,
    first_page_number: int,
    last_page_number: int,
    log: logging = logger,
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
    content: io.BytesIO = split_pdf(
        content=content,
        first_page_number=first_page_number,
        last_page_number=last_page_number,
    )
    return await convert_file_to(
        content=content, output_extension=output_extension, log=log
    )


async def _convert_with_libre(
    content: io.BytesIO, output_extension: str, log: logging
) -> io.BytesIO:
    supported_images: set = {"PNG", "JPEG", "SVG"}
    output_extension = (
        "png" if output_extension.upper() in supported_images else output_extension
    )

    url = (
        f"{document_conversion.FULL_ADDRESS}/"
        f"{document_conversion.CONVERT_API}/{output_extension}"
    )

    files = {"files": ("docs-editor-file", content)}

    s = requests.Session()
    s.stream = True
    response = s.post(url, timeout=5, files=files)
    out_data = io.BytesIO()
    try:
        response.raise_for_status()
        converted_file_bytes: bytes = response.content
        out_data = io.BytesIO(converted_file_bytes)

    except requests.exceptions.HTTPError as http_error:
        log.critical(f"Http Error: {http_error}")
    except requests.exceptions.ConnectionError as connection_error:
        log.critical(f"Connection Error: {connection_error}")
    # from this onward are not related to the raise_for_status,
    # these are all critical errors.
    except requests.exceptions.Timeout as timeout_error:
        log.error(f"Timeout Error: {timeout_error}")
    except requests.exceptions.RequestException as request_error:
        log.critical(f"Unexpected Error: {request_error}")
    except Exception as crit_err:
        log.critical(f"Critical Error: {crit_err}")

    out_data.seek(0)
    return out_data
