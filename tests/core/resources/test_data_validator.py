# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import unittest

from requests import Response
from starlette import status

from app.core.resources.constants import message
from app.core.resources.data_validator import (
    check_for_storage_response_error,
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
