FROM python:3.10-slim

WORKDIR /app

RUN mkdir -p /etc/carbonio/preview

RUN apt-get update && apt-get install -y \
    libcairo2 \
    libpango-1.0-0 \
    libpangocairo-1.0-0 \
    libgdk-pixbuf2.0-0 \
    libffi-dev \
    shared-mime-info \
    && rm -rf /var/lib/apt/lists/*

COPY ../../app .
COPY ../../../requirements.txt /tmp/requirements.txt

RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Default values
ENV PREVIEW_HOST=0.0.0.0
ENV PREVIEW_PORT=10000
ENV STORAGES_HOST=127.0.0.1
ENV STORAGES_PORT=10000
ENV DOCS_EDITOR_HOST=127.0.0.1
ENV DOCS_EDITOR_PORT=10000

ENV PYTHONPATH="/opt/zextras/common/lib/python3.10/dist-packages:/opt/zextras/common/lib/python3.10/site-packages:/opt/zextras/common/lib64/python3.10/dist-packages:/opt/zextras/common/lib64/python3.10/site-packages:/opt/zextras/common/local/lib/python3.10/dist-packages:/opt/zextras/common/local/lib/python3.10/site-packages:/opt/zextras/common/local/lib64/python3.10/dist-packages:/opt/zextras/common/local/lib64/python3.10/site-packages:"
#TODO create config ini
CMD ["python", "-m", "gunicorn", "app.controller:app", "--config", "gunicorn.conf.py"]