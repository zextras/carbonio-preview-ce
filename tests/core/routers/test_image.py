# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from unittest import IsolatedAsyncioTestCase
from unittest.mock import patch
from app.core.routers import image
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum


class TestController(IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        super(TestController, self).setUp()
        self.img_metadata = dict(
            id="da2dcce7-cd87-423c-a6c9-38c527ab6e6a",
            version=1,
            area="100x200",
        )

    def tearDown(self) -> None:
        super(TestController, self).tearDown()

    @patch("app.core.routers.image.check_for_validation_errors", return_value=None)
    @patch("app.core.routers.image.image_service." "retrieve_image_and_create_preview")
    @patch("app.core.routers.image.PreviewImageMetadata")
    async def test_get_preview_success(
        self, mock_enum, mock_create_preview, mock_validate
    ):
        await image.get_preview(
            id="test", version=1, area="test", service_type=ServiceTypeEnum.FILES
        )
        self.assertEqual(1, mock_validate.call_count)
        self.assertEqual(1, mock_create_preview.call_count)
        self.assertEqual(1, mock_enum.call_count)

    @patch("app.core.routers.image.check_for_validation_errors", return_value=True)
    async def test_get_preview_failure(self, mock_validate):
        result = await image.get_preview(
            id="test", version=1, area="test", service_type=ServiceTypeEnum.FILES
        )
        self.assertEqual(1, mock_validate.call_count)
        self.assertEqual(result, True)

    @patch("app.core.routers.image.check_for_validation_errors", return_value=None)
    @patch(
        "app.core.routers.image.image_service." "retrieve_image_and_create_thumbnail"
    )
    @patch("app.core.routers.image.ThumbnailImageMetadata")
    async def test_get_thumbnail_success(
        self, mock_enum, mock_create_thumbnail, mock_validate
    ):
        await image.get_thumbnail(
            id="test", version=1, area="test", service_type=ServiceTypeEnum.FILES
        )
        self.assertEqual(1, mock_validate.call_count)
        self.assertEqual(1, mock_create_thumbnail.call_count)
        self.assertEqual(1, mock_enum.call_count)

    @patch("app.core.routers.image.check_for_validation_errors", return_value=True)
    async def test_get_thumbnail_failure(self, mock_validate):
        result = await image.get_thumbnail(
            id="test", version=1, area="test", service_type=ServiceTypeEnum.FILES
        )
        self.assertEqual(1, mock_validate.call_count)
        self.assertEqual(result, True)
