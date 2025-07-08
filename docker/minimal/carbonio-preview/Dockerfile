FROM python:3.10-slim

RUN mkdir -p /etc/carbonio/preview

RUN apt-get update && apt-get install -y \
    libcairo2 \
    libpango-1.0-0 \
    libpangocairo-1.0-0 \
    libgdk-pixbuf2.0-0 \
    libffi-dev \
    shared-mime-info \
    && rm -rf /var/lib/apt/lists/*

COPY app ./app
COPY requirements.txt /tmp/requirements.txt
COPY package/preview/messages.ini /etc/carbonio/preview/messages.ini

RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Create log directory
RUN mkdir -p /var/log/carbonio/preview/

ENV PYTHONPATH="/app:/opt/zextras/common/lib/python3.10/dist-packages:/opt/zextras/common/lib/python3.10/site-packages:/opt/zextras/common/lib64/python3.10/dist-packages:/opt/zextras/common/lib64/python3.10/site-packages:/opt/zextras/common/local/lib/python3.10/dist-packages:/opt/zextras/common/local/lib/python3.10/site-packages:/opt/zextras/common/local/lib64/python3.10/dist-packages:/opt/zextras/common/local/lib64/python3.10/site-packages:"

CMD sh -c 'echo "# Carbonio Preview service configuration\n\
[carbonio.preview]\n\
name = preview\n\
default_host = ${PREVIEW_HOST}\n\
default_port = ${PREVIEW_PORT}\n\
timeout_in_seconds = 30\n\
docs-timeout = 15\n\
workers = 2\n\
image_name = image\n\
health_name = health\n\
pdf_name = pdf\n\
document_name = document\n\
enable_document_preview = true\n\
enable_document_thumbnail = false\n\
\n\
# Log configuration\n\
[log]\n\
format = [%(asctime)s] %(levelname)s [%(name)s.%(funcName)s:%(lineno)d] %(message)s\n\
level = warning\n\
path = /var/log/carbonio/preview/\n\
\n\
# Image constants\n\
[image_constants]\n\
minimum_resolution = 80\n\
\n\
# Carbonio Storages configuration\n\
[carbonio.storages]\n\
name = slimstore\n\
download_api = download\n\
health_check = health/live\n\
default_protocol = http\n\
default_host = ${STORAGES_HOST}\n\
default_port = ${STORAGES_PORT}\n\
\n\
# Carbonio Docs-Editor configuration\n\
[carbonio.docs-editor]\n\
default_protocol = http\n\
default_host = ${DOCS_EDITOR_HOST}\n\
default_port = ${DOCS_EDITOR_PORT}\n\
service_endpoint = services/docs/editor\n\
convert_api = cool/convert-to" > /etc/carbonio/preview/config.ini && \
python -m gunicorn app.controller:app --config app/gunicorn.conf.py'