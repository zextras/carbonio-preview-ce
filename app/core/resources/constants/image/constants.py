# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config

section_name = "image_costants"
MINIMUM_RESOLUTION = int(read_config(section=section_name, value="minimum_resolution"))
