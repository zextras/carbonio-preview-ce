# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import logging
from typing import IO
from unoserver.converter import UnoConverter

from pdfrw import PdfReader, PdfWriter

from app.core.resources import libre_office_handler
from app.core.resources.constants import service

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
    raw_content = content.read()
    all_pages = PdfReader(fdata=raw_content).pages
    pdf_page_count: int = len(all_pages)
    if first_page_number == 1 and last_page_number == 0:
        content.seek(0)
        return content

    start_page: int = first_page_number - 1  # metadata info starts
    # at 0 but first_page_number is >0
    end_page: int = (
        last_page_number if 0 < last_page_number < pdf_page_count else pdf_page_count
    )
    buf: io.BytesIO = io.BytesIO()
    writer: PdfWriter = PdfWriter(buf)
    writer.addpages(all_pages[start_page:end_page])
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
    Converts any LibreOffice supported format to any LibreOffice supported format
    \f
    :param content: file to convert
    :param output_extension: output file, should be a format supported by LibreOffice
    :param log: logger to use
    """
    return await _convert_with_libre(
        content=content, output_extension=output_extension, log=log
    )


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
    return await _convert_with_libre(
        content=content, output_extension=output_extension, log=log
    )


async def _convert_with_libre(
    content: io.BytesIO, output_extension, log: logging
) -> io.BytesIO:
    """
    private method that implements conversion logic for every type of file,
    uses LibreOffice
    \f
    :param content: pdf to convert
    :param output_extension: desired file output type
    :param log: logger to use
    """
    office_port = libre_office_handler.libre_port
    log.info(
        f"Converting file to {output_extension} "
        f"using LibreOffice instance on port {office_port}"
    )
    converter = UnoConverter(interface=service.IP, port=office_port)
    out_data = io.BytesIO(
        converter.convert(indata=content.read(), convert_to=output_extension)
    )
    out_data.seek(0)
    return out_data
