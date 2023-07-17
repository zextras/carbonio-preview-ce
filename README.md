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

- **images(png/jpeg/gif)**
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
