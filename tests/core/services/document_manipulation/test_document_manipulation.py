# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import pytest
from pypdfium2 import PdfiumError, PdfDocument
from returns.maybe import Maybe
from returns.result import Failure

from app.core.services.document_manipulation import document_manipulation  # noqa


def test_parse_if_valid_pdf_not_valid():
    # Given
    buf: io.BytesIO = io.BytesIO(b"")

    # When
    result = document_manipulation._parse_if_valid_pdf(buf)

    # Then
    assert result.value_or(None) is None


def test_split_pdf_invalid_pdf(expect):
    # Given
    empty_pdf = b"%PDF-1.7"
    buff_argument = io.BytesIO()
    expect(document_manipulation, times=1)._parse_if_valid_pdf(
        buff_argument
    ).thenReturn(
        Maybe.from_value([buff_argument])
    )  # so it can have a len
    expect(document_manipulation, times=1)._write_pdf_to_buffer(
        [buff_argument], 0, 1
    ).thenReturn(empty_pdf)

    # When
    result = document_manipulation.split_pdf(buff_argument, 1, 1)

    # Then
    assert result is empty_pdf


def test_split_pdf_valid_pdf_no_pages_to_split(expect):
    # Given
    buff_argument = io.BytesIO()
    new_doc = PdfDocument.new()
    expected_result = io.BytesIO()
    new_doc.save(expected_result)
    expected_result.seek(0)
    expect(document_manipulation, times=1)._parse_if_valid_pdf(
        buff_argument
    ).thenReturn(Maybe.from_value(new_doc))

    # When
    result = document_manipulation.split_pdf(buff_argument, 1, 0)

    # Then
    assert result.read() == expected_result.read()


@pytest.mark.parametrize(
    "in_out_extensions_tuple",
    [
        ("jpeg", "png"),
        ("JPEG", "png"),
        ("png", "png"),
        ("PNG", "png"),
        ("other_ext", "other_ext"),
    ],
)
def test_sanitize_libre_extension(expect, in_out_extensions_tuple):
    # Given
    input_format = in_out_extensions_tuple[0]
    output_format = in_out_extensions_tuple[1]

    # When
    result = document_manipulation._sanitize_output_extension(input_format)

    # Then
    assert result is output_format
