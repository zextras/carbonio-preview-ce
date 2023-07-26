# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import logging

import httpx
from fastapi import APIRouter, status
from fastapi.responses import Response

from app.core.resources.app_config import (
    ARE_DOCS_ENABLED,
    DOCUMENT_CONVERSION_FULL_SERVICE_ADDRESS,
    HEALTH_NAME,
    STORAGE_FULL_ADDRESS,
    STORAGE_HEALTH_CHECK_API,
)
from app.core.resources.constants import message

router = APIRouter(
    prefix=f"/{HEALTH_NAME}",
    tags=[HEALTH_NAME],
    responses={
        status.HTTP_502_BAD_GATEWAY: {
            "description": message.STORAGE_UNAVAILABLE_STRING,
        },
        status.HTTP_429_TOO_MANY_REQUESTS: {
            "description": message.DOCS_EDITOR_UNAVAILABLE_STRING,
        },
    },
)
logger = logging.getLogger(__name__)


@router.get("/")
async def health() -> dict:
    """
    Checks if the service and all of its dependencies are
    working and returns a descriptive json
    \f
    :return: json with status of service and optional dependencies
    """
    is_storage_up: bool = await _is_dependency_up(
        f"{STORAGE_FULL_ADDRESS}/{STORAGE_HEALTH_CHECK_API}",
    )
    is_libre_up: bool = await _is_dependency_up(
        DOCUMENT_CONVERSION_FULL_SERVICE_ADDRESS,
    )

    result_dict = {
        "ready": True,
        "dependencies": [
            {
                "name": "carbonio-storages",
                "ready": is_storage_up,
                "live": is_storage_up,
                "type": "OPTIONAL",
            },
            {
                "name": "carbonio-docs-editor",
                "ready": is_libre_up,
                "live": is_libre_up,
                "type": "OPTIONAL",
            },
        ],
    }
    logger.debug(result_dict)
    return result_dict


@router.get("/ready/")
async def health_ready() -> Response:
    """
    Checks if the service is up and essential dependencies are running correctly
    \f
    :return: returns 200 if service and carbonio-docs-editor are running
    """
    if not ARE_DOCS_ENABLED or _is_dependency_up(
        DOCUMENT_CONVERSION_FULL_SERVICE_ADDRESS,
    ):
        logger.debug("Health ready with status code 200")
        return Response(status_code=status.HTTP_200_OK)

    logger.debug("Health ready with status code 429 (Carbonio-docs-editor not up)")
    return Response(
        content=message.DOCS_EDITOR_UNAVAILABLE_STRING,
        status_code=status.HTTP_429_TOO_MANY_REQUESTS,
    )


@router.get("/live/")
async def health_live() -> Response:
    """
    Checks if the service is up
    \f
    :return: returns 200 if the service is up
    """
    logger.debug("Health live with status code 200")
    return Response(status_code=status.HTTP_200_OK)


async def _is_dependency_up(dependency_url: str, timeout: int = 5) -> bool:
    """
    Checks if the requested dependency is up
    \f
    :param dependency_url: address to ping,
     it should be the health API of the dependency
    :param timeout: if the service does not respond in the given timeout frame,
     it's considered down
    :return: True if the dependency is up
    """
    try:
        async with httpx.AsyncClient() as client:
            resp = await client.get(dependency_url, timeout=timeout)
            resp.raise_for_status()
            return True
    except httpx.HTTPError:
        return False
