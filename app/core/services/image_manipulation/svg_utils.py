# SPDX-FileCopyrightText: 2024 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io

from wand.exceptions import CoderError
from wand.image import Image


def svg_to_png(svg_bytes_io: io.BytesIO) -> io.BytesIO:
    with Image(blob=svg_bytes_io.getvalue(), format="svg") as img:
        png_bytes_io = io.BytesIO()
        img.format = 'png'
        img.save(file=png_bytes_io)
    png_bytes_io.seek(0)
    return png_bytes_io


def is_svg(svg_bytes_io: io.BytesIO) -> bool:
    try:
        with Image(blob=svg_bytes_io.getvalue(), format="svg"):
            return True
    except CoderError:
        return False
