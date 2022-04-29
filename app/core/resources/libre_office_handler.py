# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
import logging
import os
import signal
import time

import psutil

try:
    from unoserver.converter import UnoConverter
except ImportError:
    UnoConverter = ImportError("Couldn't import UnoConverter")
import random
from subprocess import Popen  # nosec
from unoserver.server import UnoServer

from app.core.resources.constants.service import IP
from app.core.resources.constants.settings import (
    LIBRE_OFFICE_PATH,
)

libre_instance: Popen = None
logger = logging.getLogger(__name__)
libre_port = "100000"


def boot_libre_instance(interface: str = IP, log: logging = logger) -> bool:
    """
    Boots LibreOffice worker instance. IF ALL POSSIBLE PORTS ARE TAKEN IT STAYS IN A LOOP
    \f
    :param interface: interface ip
    :param log: logger to use
    :return: True if the service booted up correctly
    """
    global libre_instance
    global libre_port
    sleep = 0
    while True:  # in Python do while are represented as while true: if x break
        sleep += 1
        libre_port = str(random.randint(49152, 62000))
        server = UnoServer(interface=interface, port=libre_port)
        libre_instance = server.start(daemon=True, executable=LIBRE_OFFICE_PATH)
        time.sleep(2 + sleep)
        # Used to avoid flooding with libreoffice startup.
        # You also need to wait a little for libre process to startup before checking.
        if not is_libre_instance_up():
            _shutdown_libre_instance()  # clean up memory, remove failed to boot instance
        else:
            break
    log.info(f"Started LibreOffice instance at {interface}:{libre_port}")
    # signal to intercept worker shutdown signal and terminate libreoffice correctly
    signal.signal(
        signal.SIGABRT, shutdown_worker
    )  # The one that is truly called on libre timeout
    signal.signal(signal.SIGTERM, shutdown_worker)
    signal.signal(signal.SIGQUIT, shutdown_worker)
    signal.signal(signal.SIGINT, shutdown_worker)
    return True


def shutdown_worker(signal_number, caller):
    """
    Shuts down LibreOffice worker instance and current worker.
    \f
    :param signal_number: number representing signal received (example 6 = Abort)
    :param caller: function that made the signal fire
    :return: True if the service booted up correctly
    """
    _shutdown_libre_instance()
    os._exit(1)


def _shutdown_libre_instance():
    """
    Shuts down LibreOffice worker instance.
    \f
    """
    global libre_instance
    logger.info("Worker kill requested")
    if libre_instance is not None:
        logger.info(f"Killing libre instance with pid {libre_instance.pid}")
        _kill_proc_tree(libre_instance.pid)  # This is probably overkill but keep it.
        libre_instance.terminate()


def is_libre_instance_up() -> bool:
    """
    method that checks if the worker instances of unoserver is up and running
    \f
    :return: True if the instance is working
    """
    if type(UnoConverter) != ImportError:
        try:
            UnoConverter(interface=IP, port=libre_port)
            return True
        except Exception as e:
            logger.warning(
                f"Encountered the following exception"
                f" while trying to connect to unoserver: {e}"
            )

    return False


def _kill_proc_tree(pid):
    """Kill a process tree (including grandchildren) with signal sigkill
    \f
    :param pid: pid of the process whose son are to be killed
    """
    try:
        logger.debug(
            f"Start cleanup (kill) of sons and grandchildren of process with pid: {pid}"
        )
        parent = psutil.Process(pid)
        children = parent.children(recursive=True)
        for p in children:
            if p.pid == os.getpid():
                logger.debug("Wrong pid, the process won't kill itself")
                continue
            p: psutil.Process
            try:
                logger.debug(f"Current son pid to kill {p.pid}")
                p.send_signal(signal.SIGKILL)
            except psutil.NoSuchProcess:
                pass
    except Exception as e:
        logger.warning(
            f"Encountered an exception while cleaning up"
            f" sons and grandchildren of process with pid: {pid}, Exception: {e}"
        )
