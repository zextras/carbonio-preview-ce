# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import unittest
import uuid
from unittest.mock import patch

from requests import Response
from starlette import status

from app.core.resources.constants import message
from app.core.resources.data_validator import (
    is_id_valid,
    check_for_storage_response_error,
    check_for_image_metadata_errors,
)


class TestDataValidator(unittest.TestCase):
    def setUp(self) -> None:
        super(TestDataValidator, self).setUp()
        self.img_metadata = dict(
            id="da2dcce7-cd87-423c-a6c9-38c527ab6e6a",
            version=1,
            area="100x200",
        )

    def tearDown(self) -> None:
        super(TestDataValidator, self).tearDown()

    def test_invalid_id(self):
        self.assertEqual(False, is_id_valid("ciao"))

    def test_valid_id(self):
        test_id = str(uuid.uuid4())
        self.assertEqual(True, is_id_valid(test_id))

    def test_check_for_response_error_no_error(self):
        test_response = Response()
        for i in range(200, 400):
            test_response.status_code = i
            self.assertEqual(None, check_for_storage_response_error(test_response))

    def test_check_for_response_error_no_response(self):
        result = check_for_storage_response_error(response_data=None)
        self.assertEqual(
            result.body.decode("utf-8"), message.STORAGE_UNAVAILABLE_STRING
        )
        self.assertEqual(result.status_code, status.HTTP_502_BAD_GATEWAY)

    def test_check_for_response_error_with_error(self):
        test_response = Response()
        for i in range(400, 600):
            test_response.status_code = i
            result = check_for_storage_response_error(response_data=test_response)
            self.assertEqual(
                result.body.decode("utf-8"), message.GENERIC_ERROR_WITH_STORAGE
            )
            self.assertEqual(result.status_code, i)

    @patch("app.core.resources.data_validator.is_id_valid", return_value=False)
    def test_validate_id_not_valid(self, mock_is_id_valid):
        result = check_for_image_metadata_errors(
            id="test", version=1, area="300x200", metadata_dict=dict()
        )
        self.assertEqual(1, mock_is_id_valid.call_count)
        self.assertEqual(status.HTTP_400_BAD_REQUEST, result.status_code)
        self.assertEqual(message.ID_NOT_VALID_ERROR, result.body.decode())

    @patch("app.core.resources.data_validator.is_id_valid", return_value=True)
    def test_validate_success_with_0x0_area(self, mock_is_id_valid):
        self.img_metadata["area"] = "0x0"
        check_for_image_metadata_errors(
            id="test",
            version=self.img_metadata["version"],
            area=self.img_metadata["area"],
            metadata_dict=dict(),
        )
        self.assertEqual(1, mock_is_id_valid.call_count)

    def test_validate_area_not_int(self):
        self.img_metadata["area"] = "ciaox0"
        result = check_for_image_metadata_errors(
            id="test",
            version=self.img_metadata["version"],
            area=self.img_metadata["area"],
            metadata_dict=dict(),
        )
        self.assertEqual(status.HTTP_400_BAD_REQUEST, result.status_code)
        self.assertEqual(message.HEIGHT_WIDTH_NOT_VALID_ERROR, result.body.decode())

    def test_validate_area_missing_values(self):
        self.img_metadata["area"] = "0x"
        result = check_for_image_metadata_errors(
            id="test",
            version=self.img_metadata["version"],
            area=self.img_metadata["area"],
            metadata_dict=dict(),
        )
        self.assertEqual(status.HTTP_400_BAD_REQUEST, result.status_code)
        self.assertEqual(message.HEIGHT_WIDTH_NOT_VALID_ERROR, result.body.decode())

    def test_validate_area_height_zero_invalid_id(self):
        self.img_metadata["area"] = "0x0"
        result = check_for_image_metadata_errors(
            id="test",
            version=self.img_metadata["version"],
            area=self.img_metadata["area"],
            metadata_dict=dict(),
        )
        self.assertEqual(status.HTTP_400_BAD_REQUEST, result.status_code)
        self.assertEqual(message.ID_NOT_VALID_ERROR, result.body.decode())
