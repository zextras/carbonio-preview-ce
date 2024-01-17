<!--
SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com

SPDX-License-Identifier: AGPL-3.0-only
-->

# Changelog

All notable changes to this project will be documented in this file. See [standard-version](https://github.com/conventional-changelog/standard-version) for commit guidelines.

### [0.3.8](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.7...v0.3.8) (2024-01-17)

### [0.3.7](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.6...v0.3.7) (2024-01-17)


### Bug Fixes

* update RHEL provided dependencies in PKGBUILD ([#53](https://github.com/Zextras/carbonio-preview-ce/issues/53)) ([ed9908d](https://github.com/Zextras/carbonio-preview-ce/commit/ed9908d2c691933ede8c97e6143b0bfc9c0cd395))

### [0.3.6](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.5...v0.3.6) (2024-01-16)


### Features

* move to yap agent and add rhel9 support ([#50](https://github.com/Zextras/carbonio-preview-ce/issues/50)) ([a54c111](https://github.com/Zextras/carbonio-preview-ce/commit/a54c1112029dd758ffc38094b59b35b4ccb7b165))


## v0.3.5 (2023-08-31)

#### New Features

* Validate config using pydantic ([#47](https://github.com/Zextras/carbonio-preview-ce/issues/47))
#### Others

* Validate with ruff ([#46](https://github.com/Zextras/carbonio-preview-ce/issues/46))
* Update dependencies, update rest yaml([#45](https://github.com/Zextras/carbonio-preview-ce/issues/45))

Full set of changes: [`v0.3.4...29e4eda`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.4...29e4eda)

## v0.3.4 (2023-07-06)

#### New Features

* Implement async fetch from storage ([#43](https://github.com/Zextras/carbonio-preview-ce/issues/43))
* Support GIF ([#42](https://github.com/Zextras/carbonio-preview-ce/issues/42))
* Validate preview with mypy ([#41](https://github.com/Zextras/carbonio-preview-ce/issues/41))
#### Fixes

* Allow preview of new PDF versions ([#40](https://github.com/Zextras/carbonio-preview-ce/issues/40))
#### Others

* (release): 0.3.4 ([#44](https://github.com/Zextras/carbonio-preview-ce/issues/44))

Full set of changes: [`v0.3.3...v0.3.4`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.3...v0.3.4)

## v0.3.3 (2023-05-26)

#### Fixes

* Return image type enum value and not name ([#38](https://github.com/Zextras/carbonio-preview-ce/issues/38))
#### Others

* (release): 0.3.3 ([#39](https://github.com/Zextras/carbonio-preview-ce/issues/39))

Full set of changes: [`v0.3.2...v0.3.3`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.2...v0.3.3)

## v0.3.2 (2023-05-25)

#### Fixes

* Allow versions greater or equals than 0 ([#36](https://github.com/Zextras/carbonio-preview-ce/issues/36))
#### Others

* (release): 0.3.2 ([#37](https://github.com/Zextras/carbonio-preview-ce/issues/37))

Full set of changes: [`v0.3.1...v0.3.2`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.1...v0.3.2)

## v0.3.1 (2023-05-08)

#### Fixes

* Swap deprecated render_topil  ([#34](https://github.com/Zextras/carbonio-preview-ce/issues/34))
#### Others

* (release): 0.3.1 ([#35](https://github.com/Zextras/carbonio-preview-ce/issues/35))

Full set of changes: [`v0.3.0...v0.3.1`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.3.0...v0.3.1)

## v0.3.0 (2023-04-27)

#### New Features

* Introduce docs editor ([#30](https://github.com/Zextras/carbonio-preview-ce/issues/30))
#### Others

* (release): 0.3.0 ([#33](https://github.com/Zextras/carbonio-preview-ce/issues/33))
* upgrade dependencies ([#32](https://github.com/Zextras/carbonio-preview-ce/issues/32))
* (infra): update intentions.json aligning it to new chats naming ([#31](https://github.com/Zextras/carbonio-preview-ce/issues/31))

Full set of changes: [`v0.2.15...v0.3.0`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.15...v0.3.0)

## v0.2.15 (2023-02-28)


Full set of changes: [`v0.2.14...v0.2.15`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.14...v0.2.15)

## v0.2.14 (2023-02-01)

#### Fixes

* PREV-100 - Sanitize pattern MUST be of the same type of the buffer ([#28](https://github.com/Zextras/carbonio-preview-ce/issues/28))
* PREV-100 : Remove extra headers at the beginning of pdfs ([#27](https://github.com/Zextras/carbonio-preview-ce/issues/27))
* PREV-97 - Update RHEL provided dependencies in PKGBUILD ([#26](https://github.com/Zextras/carbonio-preview-ce/issues/26))

Full set of changes: [`v0.2.13...v0.2.14`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.13...v0.2.14)

## v0.2.13 (2022-12-01)

#### New Features

* PREV-96 - Disable Docs-Core via config ([#24](https://github.com/Zextras/carbonio-preview-ce/issues/24))
#### Fixes

* PREV-95 - Kill Docs-Core process ([#23](https://github.com/Zextras/carbonio-preview-ce/issues/23))
#### Others

* bump version, fix catalog-info.yaml, add fastapi to THIRDPARTIES ([#25](https://github.com/Zextras/carbonio-preview-ce/issues/25))
* PREV-93 - Add backstage catalog-info.yaml ([#21](https://github.com/Zextras/carbonio-preview-ce/issues/21))

Full set of changes: [`v0.2.12...v0.2.13`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.12...v0.2.13)

## v0.2.12 (2022-10-26)

#### Others

* bump version ([#22](https://github.com/Zextras/carbonio-preview-ce/issues/22))
* usage of internal docs resources ([#20](https://github.com/Zextras/carbonio-preview-ce/issues/20))

Full set of changes: [`v0.2.11...v0.2.12`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.11...v0.2.12)

## v0.2.11 (2022-09-01)

#### Fixes

* PREV-63 - Rotate image with EXIF metadata ([#19](https://github.com/Zextras/carbonio-preview-ce/issues/19))
* PREV-85 - Add libre office watchdog ([#18](https://github.com/Zextras/carbonio-preview-ce/issues/18))
#### Others

* IN-497 - Add missing rhel8 provides ([#17](https://github.com/Zextras/carbonio-preview-ce/issues/17))

Full set of changes: [`v0.2.10...v0.2.11`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.10...v0.2.11)

## v0.2.10 (2022-07-20)

#### New Features

* add python build facilities aimed at packaging simplification ([#12](https://github.com/Zextras/carbonio-preview-ce/issues/12))
#### Fixes

* PREV-72 - Thumbnail generation goes in timeout ([#13](https://github.com/Zextras/carbonio-preview-ce/issues/13))
* PREV-80 - add missing group at user creation ([#15](https://github.com/Zextras/carbonio-preview-ce/issues/15))
* PREV-71 - Thumbnail of encrypted PDFs are returned blank ([#11](https://github.com/Zextras/carbonio-preview-ce/issues/11))
#### Others

* bump version ([#16](https://github.com/Zextras/carbonio-preview-ce/issues/16))

Full set of changes: [`v0.2.9...v0.2.10`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.9...v0.2.10)

## v0.2.9 (2022-06-09)

#### Fixes

* PREV-70 - Rotate logs daily ([#10](https://github.com/Zextras/carbonio-preview-ce/issues/10))
* PREV-68 - LibreOffice is spiking Memory Usage ([#9](https://github.com/Zextras/carbonio-preview-ce/issues/9))

Full set of changes: [`v0.2.8...v0.2.9`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.8...v0.2.9)

## v0.2.8 (2022-05-16)

#### Fixes

* PREV-51 - Fix 500 when input file empty ([#8](https://github.com/Zextras/carbonio-preview-ce/issues/8))

Full set of changes: [`v0.2.7...v0.2.8`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.7...v0.2.8)

## v0.2.7 (2022-05-09)

#### New Features

* PREV-12 - configure logging ([#4](https://github.com/Zextras/carbonio-preview-ce/issues/4))
#### Fixes

* PREV-55 - Change libreoffice-calc package ([#6](https://github.com/Zextras/carbonio-preview-ce/issues/6))
* PREV-54 - restart LibreOffice instance when worker restarts ([#5](https://github.com/Zextras/carbonio-preview-ce/issues/5))
#### Others

* update requirements ([#7](https://github.com/Zextras/carbonio-preview-ce/issues/7))

Full set of changes: [`v0.2.4...v0.2.7`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.4...v0.2.7)

## v0.2.4 (2022-04-08)

#### New Features

* PREV-48 - document's preview and thumbnail ([#3](https://github.com/Zextras/carbonio-preview-ce/issues/3))
* PREV-19 - crop thumbnail from the top for documents ([#2](https://github.com/Zextras/carbonio-preview-ce/issues/2))

Full set of changes: [`v0.2.2...v0.2.4`](https://github.com/Zextras/carbonio-preview-ce/compare/v0.2.2...v0.2.4)

## v0.2.2 (2022-03-23)

#### New Features

* carbonio release
#### Fixes

* PREV-46 / PREV-47 - fix requirements
