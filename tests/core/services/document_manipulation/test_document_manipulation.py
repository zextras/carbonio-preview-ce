# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import pytest
from typing import List
from unittest import IsolatedAsyncioTestCase
from unittest.mock import MagicMock, patch
from app.core.services.document_manipulation import document_manipulation  # noqa


class TestPdfManipulation(IsolatedAsyncioTestCase):
    encrypted_pdf_mock = MagicMock(return_value=b"encrypted_sample")
    encrypted_pdf_mock.keys = MagicMock(return_value=["key1", "/Encrypt", "key2"])

    def setUp(self) -> None:
        super(TestPdfManipulation, self).setUp()

    def tearDown(self) -> None:
        super(TestPdfManipulation, self).tearDown()

    def test_parse_if_valid_pdf_not_valid(self):
        buf: bytes = b""
        result = document_manipulation._parse_if_valid_pdf(buf)
        self.assertEqual(result, None)

    def test_parse_if_valid_pdf_valid(self):
        mock_pdfreader = MagicMock()
        document_manipulation.PdfReader = mock_pdfreader
        buf: bytes = b""
        result = document_manipulation._parse_if_valid_pdf(buf)
        self.assertNotEqual(result, None)

    @patch(
        "app.core.services.document_manipulation.document_manipulation._parse_if_valid_pdf",
        return_value=None,
    )
    @patch(
        "app.core.services.document_manipulation.document_manipulation._get_sanitize_offset",
        return_value=0,
    )
    def test_split_pdf_invalid_pdf(self, mock_sanitize_offset, mock_parse_pdf):
        # TODO(RakuJa): Remove this empty_pdf_buffer, mock write to buffer and check its input arguments
        empty_pdf_bytes: bytes = (
            b"%PDF-1.3\n%\xe2\xe3\xcf\xd3\n1 0 "
            b"obj\n<</Pages 2 0 R /Type /Catalog>>"
            b"\nendobj\n2 0 obj\n<</Count 0 /Kids []"
            b" /Type /Pages>>\nendobj\nxref\n0 3\n0000000000"
            b" 65535 f\r\n0000000015 00000 n\r\n0000000062"
            b" 00000 n\r\ntrailer\n\n<</Root 1 0 R "
            b"/Size 3>>\nstartxref\n112\n%%EOF\n"
        )
        result = document_manipulation.split_pdf(io.BytesIO(), 0, 1)
        self.assertEqual(result.read(), empty_pdf_bytes)
        self.assertEqual(mock_parse_pdf.call_count, 1)
        self.assertEqual(mock_sanitize_offset.call_count, 1)

    @patch(
        "app.core.services.document_manipulation.document_manipulation._parse_if_valid_pdf",
        return_value=True,
    )
    @patch(
        "app.core.services.document_manipulation.document_manipulation._get_sanitize_offset",
        return_value=0,
    )
    def test_split_pdf_valid_pdf_no_pages_to_split(
        self, mock_sanitize_offset, mock_parse_pdf
    ):
        result = document_manipulation.split_pdf(io.BytesIO(), 1, 0)
        self.assertEqual(result.read(), b"")
        self.assertEqual(mock_parse_pdf.call_count, 1)
        self.assertEqual(mock_sanitize_offset.call_count, 1)

    @patch(
        "app.core.services.document_manipulation.document_manipulation._parse_if_valid_pdf",
        return_value=encrypted_pdf_mock,
    )
    @patch(
        "app.core.services.document_manipulation.document_manipulation._get_sanitize_offset",
        return_value=0,
    )
    def test_split_encrypted_pdf(self, mock_sanitize_offset, mock_parse_pdf):
        result = document_manipulation.split_pdf(io.BytesIO(b"ciao"), 2, 5)
        self.assertEqual(result.read(), b"ciao")
        self.assertEqual(mock_parse_pdf.call_count, 1)
        self.assertEqual(mock_sanitize_offset.call_count, 1)


def test_given_pdf_without_extra_headers_sanitize_offset_should_return_0(expect):
    # Given
    buff_argument = io.BytesIO()
    pdf_content: List[bytes] = [b"%PDF-1.5", b"pdf_content1", b"pdf_content2"]
    expect(document_manipulation, times=1)._read_buffer_line_by_line(
        buff_argument
    ).thenReturn(pdf_content)

    # When
    result = document_manipulation._get_sanitize_offset(buff_argument)

    # Then
    assert result == 0


def test_given_pdf_with_one_extra_headers_sanitize_offset_should_return_len_first_string(
    expect,
):
    # Given
    buff_argument = io.BytesIO()
    pdf_content: List[bytes] = [b"extra-header" b"%PDF-1.5", b"pdf_content1"]
    expect(document_manipulation, times=1)._read_buffer_line_by_line(
        buff_argument
    ).thenReturn(pdf_content)

    # When
    result = document_manipulation._get_sanitize_offset(buff_argument)

    # Then
    assert result == 12


@pytest.mark.parametrize(
    "in_out_tuple",
    [
        ([b"x%PDF-1.5"], 1),
        ([b"xxxx%PDF-1.5"], 4),
        ([b"xxxxxxxxxxx%PDF-1.5"], 11),
        ([b"x%PDF-1.5", b"pdf-content"], 1),
        ([b"pdf-content", b"x%PDF-1.5"], 12),
        ([b"xxxx%PDF-1.5", b"pdf-content"], 4),
        ([b"pdf-content", b"xxxx%PDF-1.5"], 15),
        ([b"xxxxxxxxxxx%PDF-1.5", b"pdf-content"], 11),
        ([b"pdf-content", b"xxxxxxxxxxx%PDF-1.5"], 22),
    ],
)
def test_given_pdf_with_extra_headers_sanitize_offset_should_return_correct_size(
    expect, in_out_tuple
):
    # Given
    buff_argument = io.BytesIO()
    expect(document_manipulation, times=1)._read_buffer_line_by_line(
        buff_argument
    ).thenReturn(in_out_tuple[0])

    # When
    result = document_manipulation._get_sanitize_offset(buff_argument)

    # Then
    assert result is in_out_tuple[1]


def test_given_pdf_without_correct_header_and_multiple_lines_offset_should_return_str_size(
    expect,
):
    # Given
    buff_argument = io.BytesIO()
    str_to_search = [b"zextras", b"-", b"test-rakuja"]
    expect(document_manipulation, times=1)._read_buffer_line_by_line(
        buff_argument
    ).thenReturn(str_to_search)

    # When
    result = document_manipulation._get_sanitize_offset(buff_argument)

    # Then
    assert result is sum(len(x) for x in str_to_search)


def test_given_pdf_without_correct_header_and_single_line_offset_should_return_str_size(
    expect,
):
    # Given
    buff_argument = io.BytesIO()
    str_to_search = b"zextras-test-rakuja"
    expect(document_manipulation, times=1)._read_buffer_line_by_line(
        buff_argument
    ).thenReturn([str_to_search])

    # When
    result = document_manipulation._get_sanitize_offset(buff_argument)

    # Then
    assert result is len(str_to_search)


def test_given_empty_buffer_read_by_line_should_return_nothing():
    # Given
    buff_argument = io.BytesIO()

    # When
    result = document_manipulation._read_buffer_line_by_line(buff_argument)

    # Then
    with pytest.raises(StopIteration) as e_info:
        next(result)
    assert e_info.value.value is None


def test_given_buffer_with_one_line_read_by_line_should_return_one_line():
    # Given
    buff_argument = io.BytesIO(b"first-line")

    # When
    result = document_manipulation._read_buffer_line_by_line(buff_argument)

    # Then
    for line in result:
        assert b"first-line" == line
