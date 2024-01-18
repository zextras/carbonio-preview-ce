#!/bin/bash

# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
# 
# SPDX-License-Identifier: AGPL-3.0-only

# retrieve python3 version (major.minor)
py_ver=$(find /opt/zextras/common/lib \
  -maxdepth 1 \
  -iname "python*" |
  grep -o '[0-9]\.[0-9]' |
  sort |
  tail -1)

# set proper modules path
PYTHONPATH="/opt/zextras/common/lib/python${py_ver}/site-packages:"
PYTHONPATH+="/opt/zextras/common/lib64/python${py_ver}/site-packages:"
export PYTHONPATH

/opt/zextras/common/bin/gunicorn app.controller:app \
  --config "/opt/zextras/common/lib/python${py_ver}/site-packages/app/gunicorn.conf.py"
