# Run preview locally with Docker

This minimal setup includes all necessary dependencies without mocks (with Consul + Storages being the only exceptions).

Steps:
    1. `cd docker/minimal`
    2. `docker compose up --build`
    3. Browse Carbonio on `http://localhost:9000/`, backend accessible on `http://localhost:20008`
    4. Login using `test@demo.zextras.io`/`password`

Possible configs for preview:
  - PREVIEW_HOST
  - PREVIEW_PORT
  - STORAGES_HOST
  - STORAGES_PORT
  - DOCS_EDITOR_HOST
  - DOCS_EDITOR_PORT