# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import sys
from unittest import IsolatedAsyncioTestCase
from unittest.mock import MagicMock, patch

sys.modules["unoserver"] = MagicMock()
sys.modules["unoserver.converter"] = MagicMock()
sys.modules["unoserver.server"] = MagicMock()

# This MUST be AFTER mocking unoserver library, otherwise it will try to import uno
from app.core.services.document_manipulation import document_manipulation  # noqa


class TestPdfManipulation(IsolatedAsyncioTestCase):
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
    def test_split_pdf_invalid_pdf(self, mock_parse_pdf):
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

    @patch(
        "app.core.services.document_manipulation.document_manipulation._parse_if_valid_pdf",
        return_value=True,
    )
    def test_split_pdf_valid_pdf_no_pages_to_split(self, mock_parse_pdf):
        result = document_manipulation.split_pdf(io.BytesIO(), 1, 0)
        self.assertEqual(result.read(), b"")
        self.assertEqual(mock_parse_pdf.call_count, 1)
