# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config

NUMBER_OF_WORKERS = int(read_config(section="libreoffice", value="workers"))

LIBRE_OFFICE_PATH = read_config(section="libreoffice", value="path")

# LOG
log_section = "log"
LOG_FORMAT = read_config(section=log_section, value="format", raw=True)
LOG_PATH = read_config(section=log_section, value="path")
LOG_LEVEL = str(read_config(section=log_section, value="level")).upper()
