# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import unittest

from fastapi import status
from httpx import Response
from returns.maybe import Maybe, Nothing

from app.core.resources.constants import message
from app.core.resources.data_validator import (
    check_for_storage_response_error,
)


class TestDataValidator(unittest.TestCase):
    def setUp(self) -> None:
        super().setUp()
        self.img_metadata = {
            "id": "da2dcce7-cd87-423c-a6c9-38c527ab6e6a",
            "version": 1,
            "area": "100x200",
        }

    def tearDown(self) -> None:
        super().tearDown()

    def test_check_for_response_error_no_error(self):
        test_response = Response(status_code=200)
        for i in range(200, 400):
            test_response.status_code = i
            self.assertEqual(
                None,
                check_for_storage_response_error(
                    Maybe.from_value(test_response),
                ).value_or(None),
            )

    def test_check_for_response_error_no_response(self):
        result = check_for_storage_response_error(response_data=Nothing)
        self.assertEqual(
            result.value_or(Response(status_code=200)).body.decode("utf-8"),
            message.STORAGE_UNAVAILABLE_STRING,
        )
        self.assertEqual(
            result.value_or(Response(status_code=200)).status_code,
            status.HTTP_502_BAD_GATEWAY,
        )

    def test_check_for_response_error_with_error(self):
        test_response = Response(status_code=200)
        for i in range(400, 600):
            test_response.status_code = i
            result = check_for_storage_response_error(
                response_data=Maybe.from_value(test_response),
            )
            self.assertEqual(
                result.value_or(Response(status_code=200)).body.decode("utf-8"),
                message.STORAGE_UNAVAILABLE_STRING
                if i >= 500
                else message.GENERIC_ERROR_WITH_STORAGE,
            )
            self.assertEqual(result.value_or(Response(status_code=200)).status_code, i)
