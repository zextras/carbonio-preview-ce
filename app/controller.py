# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import uvicorn
from fastapi import FastAPI

from app.core.resources.app_config import (
    SERVICE_DESCRIPTION,
    SERVICE_IP,
    SERVICE_NAME,
    SERVICE_PORT,
)
from app.core.routers import document, health, image, pdf

app = FastAPI(
    title=SERVICE_NAME,
    version="0.3.10-SNAPSHOT",
    description=SERVICE_DESCRIPTION,
)

app.include_router(image.router)
app.include_router(pdf.router)
app.include_router(document.router)
app.include_router(health.router)

if __name__ == "__main__":
    uvicorn.run(app, host=SERVICE_IP, port=SERVICE_PORT)
