# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import uvicorn
from fastapi import FastAPI

from app.core.resources.constants import service
from app.core.routers import document, health, image, pdf

app = FastAPI(
    title=service.NAME,
    version="0.3.4-3",
    description=service.DESCRIPTION,
)

app.include_router(image.router)
app.include_router(pdf.router)
app.include_router(document.router)
app.include_router(health.router)

if __name__ == "__main__":
    uvicorn.run(app, host=service.IP, port=service.PORT)
