# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

import logging
import os

from app.core.resources.constants import service


def initialize_logging(
    log_format: str = service.LOG_FORMAT,
    log_path: str = service.LOG_PATH,
    service_name: str = service.NAME,
):

    log_path = f"{os.path.join(log_path, service_name)}.log"

    logging.basicConfig(
        level=logging.INFO,
        format=log_format,
        handlers=[logging.FileHandler(log_path), logging.StreamHandler()],
    )
