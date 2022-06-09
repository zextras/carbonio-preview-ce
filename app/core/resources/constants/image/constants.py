# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config

MINIMUM_RESOLUTION: int = int(
    read_config(section="image_constants", value="minimum_resolution")
)
