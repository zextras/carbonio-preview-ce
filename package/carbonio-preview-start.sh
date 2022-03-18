#!/bin/bash

# This file sole purpose is to change working directory and execute gunicorn.
# This cannot be done through ExecStart variable in the .service file because
# it does expect an executable in input, cd is not.

# We need to change working directory because the config file imports app, this
# import does not work if you are not in the project directory. We also need to use
# the config file (and not the command line arguments) because we need to setup
# specific behaviour to be run only once during startup and shutdown (libreoffice handling)
# and it can only be specified with a config file.

# cd could be avoided but we do it nonetheless

export JAVA_HOME=/opt/zextras/common/lib/jvm/java
cd /usr/bin/carbonio/preview/ && gunicorn controller:app --config gunicorn.conf.py