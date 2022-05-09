# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from fastapi import FastAPI

from app.core.resources import libre_office_handler
from app.core.resources.constants import service

import uvicorn

from app.core.routers import image, health, pdf, document

app = FastAPI(
    title=service.NAME,
    version="0.2.7",
    description=service.DESCRIPTION,
)

libre_office_handler.boot_libre_instance(service.IP)

app.include_router(image.router)
app.include_router(pdf.router)
app.include_router(document.router)
app.include_router(health.router)


if __name__ == "__main__":
    uvicorn.run(app, host=service.IP, port=int(service.PORT))
