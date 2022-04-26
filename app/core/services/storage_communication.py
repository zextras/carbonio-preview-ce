# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from typing import Optional

import requests
import logging
from requests.models import Response

from app.core.resources.constants import storage
from app.core.resources.schemas.enums.service_type_enum import ServiceTypeEnum

logger = logging.getLogger("Storage")


async def retrieve_data(
    file_id: str,
    version: int = 1,
    service_type: ServiceTypeEnum = ServiceTypeEnum.FILES,
    log: logging.Logger = logger,
) -> Optional[Response]:
    """
    Retrieves given node and version from the config storage
    :param file_id: Unique identifier (UUID4) of the file
    :param version: Version of the file (Default 1)
    :param log: logging used to keep track of errors and program flow
    :param service_type: service that owns the resource
    :return: Optional response,
    if there was a problem connecting with storage returns None,
     otherwise returns storage response
    """
    req = (
        f"{storage.FULL_ADDRESS}/{storage.DOWNLOAD_API}"
        + f"?node={file_id}&version={version}&type={service_type.value}"
    )
    response: Response = None
    try:
        s = requests.Session()
        s.stream = True
        response = s.get(req, timeout=60)
        response.raise_for_status()
    except requests.exceptions.HTTPError as http_error:
        log.error(f"Http Error: {http_error}")
    except requests.exceptions.ConnectionError as connection_error:
        log.error(f"Connection Error: {connection_error}")
    # from this onward are not related to the raise_for_status,
    # these are all critical errors.
    except requests.exceptions.Timeout as timeout_error:
        log.error(f"Timeout Error: {timeout_error}")
    except requests.exceptions.RequestException as request_error:
        log.critical(f"Unexpected Error: {request_error}")
    except Exception as crit_err:
        log.critical(f"Critical Error: {crit_err}")
    finally:
        log.info(f"[Requested: {req}, Response: {response}]")
        return response
