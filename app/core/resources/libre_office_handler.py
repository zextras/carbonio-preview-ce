# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

from subprocess import Popen  # nosec
from unoserver.server import UnoServer

from app.core.resources.constants.settings import (
    LIBRE_OFFICE_PATH,
    LIBRE_OFFICE_FIRST_PORT,
)

libre_instance: Popen = None
DEFAULT_PORT = str(LIBRE_OFFICE_FIRST_PORT)


def boot_libre_instance(port: str = DEFAULT_PORT):
    server = UnoServer(interface="127.0.0.1", port=port)
    print(f"Started at port {port}")
    global libre_instance
    libre_instance = server.start(daemon=True, executable=LIBRE_OFFICE_PATH)
    return True


def shutdown_libre_instance(port: str = DEFAULT_PORT):
    global libre_instance
    if libre_instance is not None:
        libre_instance.terminate()
        libre_instance = None
    return True


def reboot_libre_instance(port: str = DEFAULT_PORT):
    if shutdown_libre_instance(port):
        return boot_libre_instance(port)
    else:
        raise RuntimeError("Could not reboot libre office instance")
