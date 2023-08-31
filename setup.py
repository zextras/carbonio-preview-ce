#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from pathlib import Path

from setuptools import find_packages, setup

this_directory = str(Path(__file__).parent.resolve())
with Path.open(Path(this_directory, "README.md"), encoding="utf-8") as f:
    long_description = f.read()

setup(
    name="carbonio-preview-ce",
    packages=find_packages(),
    version="0.3.5-1",
    entry_points={"console_scripts": ["controller = controller:main"]},
    description="Carbonio Preview.",
    long_description=Path.open(Path("README.md")).read(),
    author="Zextras",
    url="https://github.com/zextras/carbonio-preview-ce/",
    download_url="https://github.com/zextras/carbonio-preview-ce/archive/master.zip",
    keywords=["carbonio", "preview", "storage"],
    classifiers=[
        "Programming Language :: Python :: 3",
        "Topic :: Software Development :: Embedded Systems",
        "Topic :: Software Development :: Version Control :: Git",
    ],
    license="AGPL3",
    zip_safe=False,
    long_description_content_type="text/markdown",
)
