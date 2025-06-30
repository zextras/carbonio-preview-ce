# Run preview locally with Docker

This minimal setup includes all necessary dependencies without mocks (with Consul + Storages being the only exceptions).

Steps:
    1. `mvn clean install -DskipTests=true`
    2. `cd docker/minimal`
    3. `docker compose up --build`
    4. Browse Carbonio on `http://localhost:9000/`, backend accessible on `http://localhost:20008`
    5. Login using `test@demo.zextras.io`/`password`

Possible configs for preview:
  - TODO