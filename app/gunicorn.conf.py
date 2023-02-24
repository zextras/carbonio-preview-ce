# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only
import multiprocessing
import os
from logging.handlers import QueueListener, QueueHandler, TimedRotatingFileHandler

from app.core.resources.constants.settings import NUMBER_OF_WORKERS
from app.core.resources.constants import service
from app.core.resources.constants.settings import LOG_FORMAT, LOG_PATH, LOG_LEVEL

#
# Server socket
#
#   bind - The socket to bind.
#
#       A string of the form: 'HOST', 'HOST:PORT', 'unix:PATH'.
#       An IP is a valid HOST.
#
#   backlog - The number of pending connections. This refers
#       to the number of clients that can be waiting to be
#       served. Exceeding this number results in the client
#       getting an error when attempting to connect. It should
#       only affect servers under significant load.
#
#       Must be a positive integer. Generally set in the 64-2048
#       range.
#

#
# Worker processes
#
#   workers - The number of worker processes that this server
#       should keep alive for handling requests.
#
#       A positive integer generally in the 2-4 x $(NUM_CORES)
#       range. You'll want to vary this a bit to find the best
#       for your particular application's work load.
#
#   worker_class - The type of workers to use. The default
#       sync class should handle most 'normal' types of work
#       loads. You'll want to read
#       http://docs.gunicorn.org/en/latest/design.html#choosing-a-worker-type
#       for information on when you might want to choose one
#       of the other worker classes.
#
#       A string referring to a Python path to a subclass of
#       gunicorn.workers.base.Worker. The default provided values
#       can be seen at
#       http://docs.gunicorn.org/en/latest/settings.html#worker-class
#
#   worker_connections - For the eventlet and gevent worker classes
#       this limits the maximum number of simultaneous clients that
#       a single process can handle.
#
#       A positive integer generally set to around 1000.
#
#   timeout - If a worker does not notify the master process in this
#       number of seconds it is killed and a new worker is spawned
#       to replace it.
#
#       Generally set to thirty seconds. Only set this noticeably
#       higher if you're sure of the repercussions for sync workers.
#       For the non sync workers it just means that the worker
#       process is still communicating and is not tied to the length
#       of time required to handle a single request.
#
#   keepalive - The number of seconds to wait for the next request
#       on a Keep-Alive HTTP connection.
#
#       A positive integer. Generally set in the 1-5 seconds range.
#

bind = f"{service.IP}:{service.PORT}"
backlog = 2048

workers = NUMBER_OF_WORKERS
worker_class = "uvicorn.workers.UvicornWorker"
worker_connections = 1000
timeout = service.TIMEOUT
keepalive = 2

#
#   spew - Install a trace function that spews every line of Python
#       that is executed when running the server. This is the
#       nuclear option.
#
#       True or False
#

spew = False

#
# Server mechanics
#
#   daemon - Detach the main Gunicorn process from the controlling
#       terminal with a standard fork/fork sequence.
#
#       True or False
#
#   raw_env - Pass environment variables to the execution environment.
#
#   pidfile - The path to a pid file to write
#
#       A path string or None to not write a pid file.
#
#   user - Switch worker processes to run as this user.
#
#       A valid user id (as an integer) or the name of a user that
#       can be retrieved with a call to pwd.getpwnam(value) or None
#       to not change the worker process user.
#
#   group - Switch worker process to run as this group.
#
#       A valid group id (as an integer) or the name of a user that
#       can be retrieved with a call to pwd.getgrnam(value) or None
#       to change the worker processes group.
#
#   umask - A mask for file permissions written by Gunicorn. Note that
#       this affects unix socket permissions.
#
#       A valid value for the os.umask(mode) call or a string
#       compatible with int(value, 0) (0 means Python guesses
#       the base, so values like "0", "0xFF", "0022" are valid
#       for decimal, hex, and octal representations)
#
#   tmp_upload_dir - A directory to store temporary request data when
#       requests are read. This will most likely be disappearing soon.
#
#       A path to a directory where the process owner can write. Or
#       None to signal that Python should choose one on its own.
#

daemon = False
pidfile = None
umask = 0
user = None
group = None
tmp_upload_dir = None

#
#   Logging
#
#   logfile - The path to a log file to write to.
#
#       A path string. "-" means log to stdout.
#
#   loglevel - The granularity of log output
#
#       A string of "debug", "info", "warning", "error", "critical"
#

log_path = f"{os.path.join(LOG_PATH, service.NAME)}.log"
capture_output = False

# Set up a queue to communicate with the handlers
log_queue = multiprocessing.Queue()

# Create a listener and add it to the root logger
listener = QueueListener(
    log_queue,
    TimedRotatingFileHandler(
        filename=log_path,
        when="h",
        encoding="utf8",
        backupCount=50,
    ),
)
listener.start()

# Set up the QueueHandler with the queue
queue_handler = QueueHandler(log_queue)


LOGGING_CONFIG = {
    "version": 1,
    "disable_existing_loggers": True,
    "root": {
        "handlers": ["console_standard", "queue"],
        "level": LOG_LEVEL,
    },
    "loggers": {
        "gunicorn.access": {
            "level": LOG_LEVEL,
            "handlers": ["console_gunicorn", "file_gunicorn"],
            "propagate": False,
            "qualname": "gunicorn.access",
        },
    },
    "handlers": {
        "console_standard": {
            "class": "logging.StreamHandler",
            "stream": "ext://sys.stdout",
            "formatter": "syslog",
        },
        "file_standard": {
            "class": "logging.handlers.TimedRotatingFileHandler",
            "filename": log_path,
            "formatter": "syslog",
            "encoding": "utf8",
            "when": "d",
            "backupCount": 50,
        },
        "queue": {
            "class": "logging.handlers.QueueHandler",
            "queue": log_queue,
        },
        "console_gunicorn": {
            "class": "logging.StreamHandler",
            "stream": "ext://sys.stdout",
            "formatter": "syslog",
        },
        "file_gunicorn": {
            "class": "logging.handlers.TimedRotatingFileHandler",
            "filename": log_path,
            "formatter": "syslog",
            "encoding": "utf8",
            "when": "d",
            "backupCount": 50,
        },
    },
    "formatters": {
        "syslog": {"format": LOG_FORMAT},
    },
}

logconfig_dict = LOGGING_CONFIG

#
# Process naming
#
#   proc_name - A base to use with setproctitle to change the way
#       that Gunicorn processes are reported in the system process
#       table. This affects things like 'ps' and 'top'. If you're
#       going to be running more than one instance of Gunicorn you'll
#       probably want to set a name to tell them apart. This requires
#       that you install the setproctitle module.
#
#       A string or None to choose a default of something like 'gunicorn'.
#

proc_name = "carbonio-preview-manager"

#
# Server hooks
#
#   post_fork - Called just after a worker has been forked.
#
#       A callable that takes a server and worker instance
#       as arguments.
#
#   pre_fork - Called just prior to forking the worker subprocess.
#
#       A callable that accepts the same arguments as after_fork
#
#   pre_exec - Called just prior to forking off a secondary
#       master process during things like config reloading.
#
#       A callable that takes a server instance as the sole argument.
#


def child_exit(server, worker):
    server.log.info(f"Worker killed: {worker.pid}")


def post_worker_init(worker):
    worker.log.info("Post worker init")
    # import here, otherwise logs won't be configured correctly
    from app.core.resources.libre_office_handler import init_signals

    init_signals()


def pre_exec(server):
    server.log.info("Forked child, re-executing.")


def when_ready(server):
    server.log.info("Server is ready. Spawning workers")


def worker_abort(worker):
    worker.log.info("worker received SIGABRT signal")


def on_starting(server):
    server.log.info("Starting server and libreoffice daemons")


def on_reload(server):
    server.log.info("Restarting server and libreoffice daemons")


def on_shutdown(server):
    server.log.info("Closing server and terminating libreoffice daemons")
