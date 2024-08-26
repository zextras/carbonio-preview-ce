# SPDX-FileCopyrightText: 2024 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import cairosvg


def svg_to_png(svg_bytes_io: io.BytesIO) -> io.BytesIO:
    png_bytes_io = io.BytesIO()
    cairosvg.svg2png(bytestring=svg_bytes_io.getvalue(), write_to=png_bytes_io)
    svg_bytes_io.close()
    png_bytes_io.seek(0)
    return png_bytes_io


def is_svg(svg_bytes_io: io.BytesIO) -> bool:
    try:
        svg_bytes_io.seek(0)
        cairosvg.svg2png(bytestring=svg_bytes_io.getvalue())
        return True
    except:
        return False
