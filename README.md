<!--
SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com

SPDX-License-Identifier: AGPL-3.0-only
-->

<div align="center">
  <h1>Carbonio-Preview-ce 🚀 </h1>
</div>

<div align="center">

Preview-ce backend service for Zextras Carbonio

[![Contributors][contributors-badge]][contributors]
[![Activity][activity-badge]][activity]
[![security: bandit](https://img.shields.io/badge/security-bandit-yellow.svg)](https://github.com/PyCQA/bandit)
[![Code style: black](https://img.shields.io/badge/code%20style-black-000000.svg)](https://github.com/psf/black)
[![Ruff](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/charliermarsh/ruff/main/assets/badge/v2.json)](https://github.com/astral-sh/ruff)
[![License][license-badge]](COPYING)
[![Project][project-badge]][project]
[![Twitter][twitter-badge]][twitter]

</div>

You can preview the following type of files:

- **images(png/jpeg/gif/svg)**
- **pdf**
- **docx, doc, odp, odt, ppt, xls, xlsx**

You will be able to:

- **Get preview of files**.
- **Generate thumbnail of files**.

Preview will always try to output the file in its original format,
 while thumbnail will convert it to an image.
There is no difference in quality between the two,
 the difference in quality can be achieved only
by asking for a jpeg format and changing the quality parameter.
Asking for a GIF output can only be done when the input file is a GIF, otherwise it will raise and error.

## Logging

The service uses Go's standard `log/slog` library with a `TextHandler` on stderr.

The log level is controlled by the **`PREVIEW_LOG_LEVEL`** environment variable. This is a per-instance,
framework-level knob (equivalent to `QUARKUS_LOG_LEVEL` in Quarkus services) — it is **not** part
of the Carbonio networking/application config chain and does not appear in `configs.txt` or the
registry.

| Value | Effective level | Notes |
|-------|----------------|-------|
| `debug` | DEBUG | All messages |
| `info` (default) | INFO | Default when variable is absent or empty |
| `warning` / `warn` | WARN | Python `logging` alias accepted |
| `error` | ERROR | |
| `critical` | ERROR | Python `CRITICAL` has no slog equivalent; mapped to Error |

Values are case-insensitive. An unrecognised value causes the service to fail-fast at startup.

**Per-instance configuration via systemd drop-in:**

```bash
systemctl edit carbonio-preview
```

Add:

```ini
[Service]
Environment="PREVIEW_LOG_LEVEL=debug"
```

**Automatic migration:** When upgrading from the legacy Python service, if `config.ini` contains
a `[log] level` key, the `--setup` migration rewrites it as a systemd drop-in at
`/etc/systemd/system/carbonio-preview.service.d/log-level.conf`. The pending-setups script
runs `systemctl daemon-reload` immediately after so the level is active before the service
restarts.

## Runtime Tuning

Performance-related knobs are **application-layer configuration keys**: they are
resolved through the Carbonio config chain and can be set fleet-wide via **Consul KV**
or overridden per-instance via the matching `APPLICATION_CONFIG_*` environment variable
(the env override always wins over the KV value). See [`docs/configs.md`](docs/configs.md)
for the authoritative list of keys and defaults.

| Consul KV key | Env override | Default | Description |
|---------------|--------------|---------|-------------|
| `carbonio-preview/render/max-concurrent-operations` | `APPLICATION_CONFIG_RENDER_MAX_CONCURRENT_OPERATIONS` | *CPU count* | Max concurrent render operations (image, PDF, document); does not apply to video |
| `carbonio-preview/render/pdf-subprocess-pool-size` | `APPLICATION_CONFIG_RENDER_PDF_SUBPROCESS_POOL_SIZE` | *CPU count* | Number of PDFium helper OS subprocesses |
| `carbonio-preview/render/cache-max-mb` | `APPLICATION_CONFIG_RENDER_CACHE_MAX_MB` | `256` | Size budget (MiB) of the shared rendered-output cache; `0` disables it |
| `carbonio-preview/video/max-concurrent-extractions` | `APPLICATION_CONFIG_VIDEO_MAX_CONCURRENT_EXTRACTIONS` | *CPU count* | Max concurrent video first-frame extraction jobs |
| `carbonio-preview/storage/fetch-timeout-seconds` | `APPLICATION_CONFIG_STORAGE_FETCH_TIMEOUT_SECONDS` | `30` | Timeout (s) for fetching the source blob from carbonio-storages |
| `carbonio-preview/document/conversion-timeout-seconds` | `APPLICATION_CONFIG_DOCUMENT_CONVERSION_TIMEOUT_SECONDS` | `15` | Timeout (s) for carbonio-docs-editor (Collabora) conversion |

Values must be positive integers >= 1 (`render/cache-max-mb` also accepts `0` to disable
the cache). An invalid value causes the service to fail-fast at startup.

A few knobs are true per-instance environment variables, set via systemd drop-in
(same mechanism as `PREVIEW_LOG_LEVEL`) and **not** part of the config chain:

| Variable | Default | Description |
|----------|---------|-------------|
| `PREVIEW_PDFIUM_WORKER_PATH` | *next to main binary* | Override path to the `carbonio-preview-pdfium-worker` binary |

The libvips internal-threads level is a fixed internal constant (`1`) — it has no env
var or KV key.

## APIs Documentation 📚

Once the service is up and running, APIs will be found
[here](https://127.78.0.6:10000/docs)

## Dependencies 🔗

These are the dependencies that the service has.
These dependencies are required to run the service correctly but are not installed by the package.
They must be installed if Mandatory otherwise user discretion is advised

| Name                 | Mandatory/Optional |
|----------------------|--------------------|
| carbonio-storages-ce | Optional           |
 | carbonio-docs-editor | Optional           |

## Service installation 🏁

Install `carbonio-preview-ce` via apt:

```bash
sudo apt install carbonio-preview-ce
```

or via yum:

```bash
sudo yum install carbonio-preview-ce
```

## Daemon setup 📈

After the installation you must run `pending-setups` in order to register the service in `service-discover`.
This will start the service as a daemon and allow `carbonio-preview-ce` to communicate with the suite using Consul.

## Project setup ⚙️🔧

To develop this project you will need to configure a proper enviroment.

- download the project from the repository:

```bash
git clone 'https://github.com/Zextras/carbonio-preview-ce'
```

- Go to the project folder

```bash
virtualenv --python /usr/bin/python3 venv
source venv/bin/activate
```

- Install python libraries

```bash
pip3 install -r "dev_requirements.txt"
```

## Debug and run 🔎

To start the application from command line, go to the project folder and type:

```bash
gunicorn controller:app --config gunicorn.conf.py
```

There are others alternatives, you can also start the program from the main class (if you want to debug it).


## CI and Tests 🤖

Static analysis is provided by a few tools:

- Bandit: security analysis;
- Flake8: code style and indentation analysis;
- Pre-commit: runs static analysis before every commit;
- autopep8: called automatically by pre-commit to static errors.

Pre-commit needs to be activated in the root directory of the project using:

```bash
pre-commit install
```

To activate commit lint (mandatory) then:

```bash
pre-commit install --hook-type commit-msg
```

To run unit tests manually, run the following command from the project folder:

```bash
python -m pytest
```

## Tech Stack 💾

All the python libraries used can be found on the "requirements.txt" file.

## License

Official Preview-ce backend service for Zextras Carbonio.

Released under the AGPL-3.0-only license as specified here: [COPYING](COPYING).

See [COPYING](COPYING) file for the project license details

See [THIRDPARTIES](THIRDPARTIES) file for other licenses details

### Copyright notice

All non-software material (such as, for example, names, images, logos, sounds) is owned by Zextras
s.r.l. and is licensed under [CC-BY-NC-SA](https://creativecommons.org/licenses/by-nc-sa/4.0/).

Where not specified, all source files owned by Zextras s.r.l. are licensed under AGPL-3.0-only

[contributors-badge]: https://img.shields.io/github/contributors/zextras/carbonio-preview-ce "Contributors"

[contributors]: https://github.com/zextras/carbonio-preview-ce/graphs/contributors "Contributors"

[activity-badge]: https://img.shields.io/github/commit-activity/m/zextras/carbonio-preview-ce "Activity"

[activity]: https://github.com/zextras/carbonio-preview-ce/pulse "Activity"

[license-badge]: https://img.shields.io/badge/license-AGPL-blue.svg

[project-badge]: https://img.shields.io/badge/project-carbonio-informational "Project Carbonio"

[project]: https://www.zextras.com/carbonio/ "Project Carbonio"

[twitter-badge]: https://img.shields.io/twitter/follow/zextras?style=social&logo=twitter "Follow on Twitter"

[twitter]: https://twitter.com/intent/follow?screen_name=zextras "Follow Zextras on Twitter"
