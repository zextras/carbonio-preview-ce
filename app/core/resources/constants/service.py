# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

from app.core.resources.config_loader import read_config


# SERVICE CONFIG
_service_section_name: str = "service"
NAME: str = read_config(section=_service_section_name, value="name")
TIMEOUT: int = int(
    read_config(section=_service_section_name, value="timeout_in_seconds")
)
IP: str = read_config(section=_service_section_name, value="ip")
PORT: int = int(read_config(section=_service_section_name, value="port"))

ENABLE_DOCUMENT_THUMBNAIL: bool = (
    True
    if read_config(
        section=_service_section_name,
        value="enable_document_thumbnail",
        default_value="false",
    ).lower()
    == "true"
    else False
)

ENABLE_DOCUMENT_PREVIEW = (
    True
    if read_config(
        section=_service_section_name,
        value="enable_document_preview",
        default_value="true",
    ).lower()
    == "true"
    else False
)

ARE_DOCS_ENABLED: bool = ENABLE_DOCUMENT_PREVIEW or ENABLE_DOCUMENT_THUMBNAIL

DESCRIPTION = """
Preview service. 🚀 \n
You can preview the following type of files:

* **images(png/jpeg)**
* **pdf**
* **documents (xls, xlsx, ods, ppt, pptx, odp, doc, docx, odt)**

You will be able to:

* **Preview images**.
* **Generate smart thumbnails**.

The main difference between thumbnail and preview
 functionality is that preview tends to be more faithful
while thumbnail tends to elaborate on it, cropping
 it by default and rounding the image if asked.
Preview should always output the file in its original format,
 while thumbnail will convert it to an image.
There is no difference in quality between the two,
 the difference in quality can be achieved only
by asking for a jpeg format and changing the quality parameter.
"""

# IMAGE
IMAGE_NAME: str = read_config(section=_service_section_name, value="image_name")

# HEALTH
HEALTH_NAME: str = read_config(section=_service_section_name, value="health_name")

# PDF
PDF_NAME: str = read_config(section=_service_section_name, value="pdf_name")

# DOCUMENT

DOC_NAME: str = read_config(section=_service_section_name, value="document_name")
