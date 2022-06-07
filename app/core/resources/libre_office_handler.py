# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
from app.core.resources.constants.settings import (
    LIBRE_OFFICE_PATH,
)
from app.core.resources.constants.service import IP
from unoserver.server import UnoServer
from subprocess import Popen  # nosec
import random
import io
import logging
import os
import signal
import time

import psutil
import concurrent.futures

try:
    from unoserver.converter import UnoConverter
except ImportError:
    UnoConverter = ImportError("Couldn't import UnoConverter")


libre_instance: Popen = None
logger = logging.getLogger(__name__)
unoserver_log = logging.getLogger("unoserver")
unoserver_log.setLevel(logging.WARNING)
libre_port = "49152"

executor = concurrent.futures.ThreadPoolExecutor()


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
        libre_port = str(
            random.randint(49152, 62000)  # nosec
        )  # This random is not used for security or cryptographic purposes
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
    return True


def init_signals():
    """
    Initializes the signal handling of the program
    """
    signal.signal(
        signal.SIGABRT, shutdown_worker
    )  # The one that is truly called on libre timeout
    signal.signal(signal.SIGTERM, shutdown_worker)
    signal.signal(signal.SIGQUIT, shutdown_worker)
    signal.signal(signal.SIGINT, shutdown_worker)
    signal.signal(signal.SIGHUP, shutdown_worker)


def shutdown_worker(signal_number=6, caller=None):
    """
    Shuts down LibreOffice worker instance and current worker.
    \f
    :param signal_number: number representing signal received (example 6 = Abort)
    :param caller: function that made the signal fire
    :return: True if the service booted up correctly
    """
    logger.warning(
        f"Shut down worker called from {caller} with signal number {signal_number}"
    )
    _shutdown_libre_instance()
    logging.shutdown()
    executor.shutdown()
    os._exit(1)


def _shutdown_libre_instance():
    """
    Shuts down LibreOffice worker instance.
    \f
    """
    global libre_instance
    logger.info(f"Libre instance kill requested, instance is {libre_instance}")
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
            future = executor.submit(UnoConverter, IP, libre_port)
            future.result(timeout=5)
            return True
        except Exception as e:
            logger.warning(
                f"Encountered the following exception"
                f" while trying to connect to unoserver: {e}"
            )
    return False


def libre_convert_handler(
    service_ip: str, office_port: str, content: io.BytesIO, output_extension: str
) -> bytes:
    """
    private method used to isolate and handle conversion
    of file using the external library UnoServer
    \f
    :param service_ip: libreoffice instance ip
    :param office_port: libreoffice instance port
    :param content: content to convert
    :param output_extension: desired output format
    """
    converter: UnoConverter = UnoConverter(service_ip, office_port)
    return converter.convert(indata=content.read(), convert_to=output_extension)


def _kill_proc_tree(pid):
    """Kill a process tree (including grandchildren) with signal sigkill
    \f
    :param pid: pid of the process whose son are to be killed
    """
    try:
        logger.debug(
            f"Start cleanup (kill) of children and"
            f" grandchildren of process with pid: {pid}"
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
            f" children and grandchildren of process with pid: {pid}, Exception: {e}"
        )
