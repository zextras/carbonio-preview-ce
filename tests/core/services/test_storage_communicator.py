# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import logging
import unittest
from typing import Optional
from unittest.mock import MagicMock

import httpx
import respx as respx
from httpx import Response
from returns.maybe import Nothing

import app.core.services.storage_communication as st_com
from app.core.resources.app_config import STORAGE_DOWNLOAD_API, STORAGE_FULL_ADDRESS
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum


class TestStorageCommunicator(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.log_mock = logging.Logger("test")
        self._mock_loggers()
        self.test_id = "da2dcce7-cd87-423c-a6c9-38c527ab6e6a"
        self.version = 1
        self.req = (
            f"{STORAGE_FULL_ADDRESS}/{STORAGE_DOWNLOAD_API}"
            f"?node={self.test_id}&version={self.version}&type={ServiceTypeEnum.FILES.value}"
        )

    def tearDown(self) -> None:
        super().setUp()

    def _mock_loggers(self):
        # For some reason if i do not return
        # something it doesn't count method calls
        self.log_mock.debug = MagicMock(return_value=False)
        self.log_mock.info = MagicMock(return_value=False)
        self.log_mock.critical = MagicMock(return_value=False)
        self.log_mock.error = MagicMock(return_value=False)

    async def test_retrieve_data_success(self):
        # need to use context manager to patch async
        with respx.mock:
            mock_resp = respx.get(self.req).mock(return_value=Response(200))
            response: Optional[Response] = await st_com.retrieve_data(
                file_id=self.test_id,
                version=self.version,
                log=self.log_mock,
            )
            self.assertEqual(1, mock_resp.call_count)
            self.assertIsNotNone(response)
        self.assertEqual(1, self.log_mock.info.call_count)
        self.assertEqual(0, self.log_mock.critical.call_count)
        self.assertEqual(0, self.log_mock.error.call_count)

    async def test_retrieve_data_request_exception(self):
        with respx.mock:
            mock_resp = respx.get(
                self.req,
            ).mock(side_effect=httpx.RequestError)
            response: Optional[Response] = await st_com.retrieve_data(
                file_id=self.test_id,
                version=self.version,
                log=self.log_mock,
            )
            self.assertEqual(1, mock_resp.call_count)
        self.assertEqual(1, self.log_mock.critical.call_count)
        self.assertEqual(0, self.log_mock.error.call_count)
        self.assertEqual(0, self.log_mock.info.call_count)
        self.assertEqual(Nothing, response)

    async def test_retrieve_data_http_error(self):
        # Status code from 400 to 600 raise http error
        starting_point = 400
        ending_point = 600
        for i in range(starting_point, ending_point):
            with respx.mock:
                mock_resp = respx.get(self.req).mock(return_value=Response(i))
                response: Optional[Response] = await st_com.retrieve_data(
                    file_id=self.test_id,
                    version=self.version,
                    log=self.log_mock,
                )
                self.assertEqual(1, mock_resp.call_count)
                self.assertIsNotNone(response)
            self.assertEqual(0, self.log_mock.critical.call_count)
            self.assertEqual(i + 1 - starting_point, self.log_mock.debug.call_count)
            self.assertEqual(0, self.log_mock.info.call_count)

    async def test_retrieve_data_timeout_error(self):
        with respx.mock:
            mock_resp = respx.get(self.req).mock(side_effect=httpx.ConnectTimeout)
            response: Optional[Response] = await st_com.retrieve_data(
                file_id=self.test_id,
                version=self.version,
                log=self.log_mock,
            )
            self.assertEqual(1, mock_resp.call_count)
        self.assertEqual(0, self.log_mock.critical.call_count)
        self.assertEqual(1, self.log_mock.error.call_count)
        self.assertEqual(0, self.log_mock.info.call_count)
        self.assertEqual(Nothing, response)

    async def test_retrieve_data_generic_error(self):
        with respx.mock:
            mock_resp = respx.get(self.req).mock(side_effect=Exception)
            response: Optional[Response] = await st_com.retrieve_data(
                file_id=self.test_id,
                version=self.version,
                log=self.log_mock,
            )
            self.assertEqual(1, mock_resp.call_count)
        self.assertEqual(1, self.log_mock.critical.call_count)
        self.assertEqual(0, self.log_mock.error.call_count)
        self.assertEqual(0, self.log_mock.info.call_count)
        self.assertEqual(Nothing, response)
