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
[![License][license-badge]](COPYING)
[![Project][project-badge]][project]
[![Twitter][twitter-badge]][twitter]

</div>


You can preview the following type of files:
- **images(png/jpeg)**
- **pdf**


You will be able to:

* **Preview files**.
* **Generate thumbnails**.


Preview will always try to output the file in its original format,
 while thumbnail will convert it to an image.
There is no difference in quality between the two,
 the difference in quality can be achieved only
by asking for a jpeg format and changing the quality parameter.


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

* download the project from the repository:
```bash
git clone 'https://github.com/Zextras/carbonio-preview-ce'
```

* Go to the project folder

* Optionally you can now create a virtual enviroment. Unfortunately, python3-libreoffice needs to be installed somewhere inside the enviroment or you can also do it using the global libreoffice instance with:
```bash
virtualenv --python /usr/bin/python3 --system-site-packages venv
source venv/bin/activate
```

* Install libreoffice

* Install python libraries
```bash
pip3 install -r "dev_requirements.txt"
```

## Debug and run 🔎

To start the application from command line, go to the project folder and type:

```bash
gunicorn controller:app --config gunicorn.conf.py
```

There are others alternatives, you can also start the program from the main class (if you want to debug it). **keep in 
mind that starting from the main class does not load LibreOffice as the loading is done inside the gunicorn.conf.py file.**


## CI and Tests 🤖

Static analysis is provided by a few tools:
* Bandit: security analysis;
* Flake8: code style and indentation analysis;
* Pre-commit: runs static analysis before every commit;
* autopep8: called automatically by pre-commit to static errors.

Pre-commit needs to be activated in the root directory of the project using:
```bash
pre-commit install
```
To activate commit lint (mandatory) then:
```bash
pre-commit install --hook-type commit-msg
```
check for test coverage locally:
```bash
coverage run --source app/ -m unittest discover && coverage report
```
To run unit tests manually, run the following command from the project folder:
```bash
python -m unittest discover -v -s "./tests/" -p "test*"
```




## Tech Stack 💾

All the python libraries used can be found on the "requirements.txt" file.

LibreOffice is used to convert documents to pdf and pdfs to images.

## License
Official Preview-ce backend service for Zextras Carbonio.

Released under the AGPL-3.0-only license as specified here: [COPYING](COPYING).

Copyright (C) 2022 Zextras <https://www.zextras.com>

    This program is free software: you can redistribute it and/or modify
    it under the terms of the GNU Affero General Public License as published by
    the Free Software Foundation, either version 3 of the License, or
    (at your option) any later version.

    This program is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    GNU Affero General Public License for more details.

    You should have received a copy of the GNU Affero General Public License
    along with this program.  If not, see <https://www.gnu.org/licenses/>.

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