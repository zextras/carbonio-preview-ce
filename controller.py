# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from fastapi import FastAPI

from app.core.resources import logging_initialization
from app.core.resources.constants import service

import uvicorn

from app.core.routers import image, health, pdf

app = FastAPI(
    title=service.NAME,
    version="0.2.3",
    description=service.DESCRIPTION,
    on_startup=logging_initialization.initialize_logging(service_name=service.NAME),
)

app.include_router(image.router)
app.include_router(health.router)
app.include_router(pdf.router)


if __name__ == "__main__":
    uvicorn.run(app, host=service.IP, port=int(service.PORT))
