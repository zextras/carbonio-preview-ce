# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import io
import unittest
from unittest import mock

from httpx import Response
from returns.maybe import Maybe, Nothing
from fastapi import status

from app.core.resources.schemas.enums.image_type_enum import ImageTypeEnum
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum
from app.core.resources.schemas.preview_image_metadata import PreviewImageMetadata
from app.core.services import image_service


class TestImageService(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        super(TestImageService, self).setUp()
        self.img_metadata = PreviewImageMetadata(
            id="da2dcce7-cd87-423c-a6c9-38c527ab6e6a", version=1, height=100, width=100
        )
        self.fake_response = Response(status_code=200)

    def tearDown(self) -> None:
        super(TestImageService, self).tearDown()

    async def test_create_preview_success(self):
        self.fake_response.status_code = status.HTTP_200_OK
        with mock.patch(
            "app.core.services." "image_service." "storage_communication.retrieve_data",
            mock.AsyncMock(),
        ) as retrieve_data_mock, mock.patch(
            "app.core.services." "image_service." "_process_response_data",
            mock.AsyncMock(return_value=Response(status_code=status.HTTP_200_OK)),
        ) as process_mock:
            img_response: Response = (
                await image_service.retrieve_image_and_create_preview(
                    image_id="test",
                    version=1,
                    img_metadata=self.img_metadata,
                    service_type=ServiceTypeEnum.FILES,
                )
            )
            self.assertEqual(1, retrieve_data_mock.call_count)
            self.assertEqual(1, process_mock.call_count)
            self.assertEqual(self.fake_response.status_code, img_response.status_code)

    async def test_create_preview_failure_retrieving(self):
        self.fake_response.status_code = status.HTTP_404_NOT_FOUND
        with mock.patch(
            "app.core.services." "image_service." "storage_communication.retrieve_data",
            mock.AsyncMock(),
        ) as retrieve_data_mock, mock.patch(
            "app.core.services." "image_service." "_process_response_data",
            mock.AsyncMock(
                return_value=Response(status_code=status.HTTP_404_NOT_FOUND)
            ),
        ) as process_mock:
            retrieve_data_mock.return_value = Maybe.from_value(self.fake_response)
            stream_response: Response = (
                await image_service.retrieve_image_and_create_preview(
                    image_id="test",
                    version=1,
                    img_metadata=self.img_metadata,
                    service_type=ServiceTypeEnum.FILES,
                )
            )
            self.assertEqual(1, retrieve_data_mock.call_count)
            self.assertEqual(1, process_mock.call_count)
            self.assertEqual(
                self.fake_response.status_code, stream_response.status_code
            )

    async def test_create_preview_failure_contacting_storage(self):
        with mock.patch(
            "app.core.services." "image_service." "storage_communication.retrieve_data",
            mock.AsyncMock(),
        ) as retrieve_data_mock, mock.patch(
            "app.core.services." "image_service." "_process_response_data",
            mock.AsyncMock(
                return_value=Response(status_code=status.HTTP_502_BAD_GATEWAY)
            ),
        ) as process_mock:
            retrieve_data_mock.return_value = Nothing
            stream_response: Response = (
                await image_service.retrieve_image_and_create_preview(
                    image_id="test",
                    version=1,
                    img_metadata=self.img_metadata,
                    service_type=ServiceTypeEnum.FILES,
                )
            )
            self.assertEqual(1, process_mock.call_count)
            self.assertEqual(1, retrieve_data_mock.call_count)
            self.assertEqual(status.HTTP_502_BAD_GATEWAY, stream_response.status_code)

    def test_select_manipulation_jpeg(self):
        self.img_metadata.format = ImageTypeEnum.JPEG
        self.img_metadata.quality = None
        with mock.patch("app.core.services.image_service" ".jpeg_preview") as jpeg_mock:
            jpeg_mock.return_value = None
            result = image_service._select_preview_module(
                img_metadata=self.img_metadata, content=io.BytesIO()
            )
            self.assertEqual(1, jpeg_mock.call_count)
            self.assertEqual(jpeg_mock(), result)

    def test_select_manipulation_png(self):
        self.img_metadata.format = ImageTypeEnum.PNG
        self.img_metadata.quality = None
        with mock.patch("app.core.services.image_service" ".png_preview") as png_mock:
            png_mock.return_value = None
            result = image_service._select_preview_module(
                img_metadata=self.img_metadata, content=io.BytesIO()
            )
            self.assertEqual(1, png_mock.call_count)
            self.assertEqual(png_mock(), result)

    def test_select_not_valid(self):
        self.img_metadata.format = None
        self.assertRaises(
            ValueError, image_service._select_preview_module, self.img_metadata, bytes()
        )
