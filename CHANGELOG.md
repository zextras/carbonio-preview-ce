## [3.0.2](https://github.com/zextras/carbonio-preview-ce/compare/v3.0.1...v3.0.2) (2026-07-09)

## [3.0.1](https://github.com/zextras/carbonio-preview-ce/compare/v3.0.0...v3.0.1) (2026-07-08)

## [3.0.0](https://github.com/zextras/carbonio-preview-ce/compare/v2.0.2...v3.0.0) (2026-07-03)

## [2.0.2](https://github.com/zextras/carbonio-preview-ce/compare/v2.0.1...v2.0.2) (2026-07-03)

## [2.0.1](https://github.com/zextras/carbonio-preview-ce/compare/v2.0.0...v2.0.1) (2026-07-02)

## [2.0.0](https://github.com/zextras/carbonio-preview-ce/compare/v1.3.1...v2.0.0) (2026-07-02)

<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

## [1.3.0](https://github.com/zextras/carbonio-preview-ce/compare/v1.2.0...v1.3.0) (2026-06-18)

### Features

* **ci:** [IN-951] add arm64 platform to docker image builds ([#143](https://github.com/zextras/carbonio-preview-ce/issues/143)) ([5585011](https://github.com/zextras/carbonio-preview-ce/commit/55850119e96801e5b00a71283704a2b2b63d99c3))

### Bug Fixes

* **reuse:** project-owned REUSE.toml (drop catch-all + legacy dep5) ([#162](https://github.com/zextras/carbonio-preview-ce/issues/162)) ([9c70325](https://github.com/zextras/carbonio-preview-ce/commit/9c7032538ed1843dfb366718d4e1bd8bd0e80b0d))

## [1.2.0](https://github.com/zextras/carbonio-preview-ce/compare/v1.1.1...v1.2.0) (2026-05-27)

### Features

* **packaging:** use arch=('any') for architecture-independent package ([#140](https://github.com/zextras/carbonio-preview-ce/issues/140)) ([1061c6b](https://github.com/zextras/carbonio-preview-ce/commit/1061c6b1cb632f8d6c2efab5756ed8f618c27536))

### Bug Fixes

* **deps:** add explicit service-discover-base dependency ([#142](https://github.com/zextras/carbonio-preview-ce/issues/142)) ([ec784a0](https://github.com/zextras/carbonio-preview-ce/commit/ec784a07f47a29af0849ff7d7d27fcc2b1f4acac))

## [1.1.1](https://github.com/zextras/carbonio-preview-ce/compare/v1.1.0...v1.1.1) (2026-05-06)

### Bug Fixes

* restore buildPackages() to fix pkgrel on tag builds ([#136](https://github.com/zextras/carbonio-preview-ce/issues/136)) ([6e62ec4](https://github.com/zextras/carbonio-preview-ce/commit/6e62ec413b3ff6e9b17408aea8d1ea1c9e13abeb))

## [1.1.0](https://github.com/zextras/carbonio-preview-ce/compare/v1.0.1...v1.1.0) (2026-05-04)

### Features

* systemd hardening and service-discover.target orchestration ([#131](https://github.com/zextras/carbonio-preview-ce/issues/131)) ([2818405](https://github.com/zextras/carbonio-preview-ce/commit/2818405449e7f8eede817fcee36ce085d9adb98b))

## [1.0.1](https://github.com/zextras/carbonio-preview-ce/compare/v1.0.0...v1.0.1) (2026-02-23)

## [1.0.0](https://github.com/zextras/carbonio-preview-ce/compare/v0.6.4...v1.0.0) (2025-11-14)

### ⚠ BREAKING CHANGES

* update release config and trigger first major bump (#100)

### Bug Fixes

* update release config and trigger first major bump ([#100](https://github.com/zextras/carbonio-preview-ce/issues/100)) ([cf421ed](https://github.com/zextras/carbonio-preview-ce/commit/cf421ed82f7707f8df9561ab59d317b7d13facce))

## [0.6.4](https://github.com/zextras/carbonio-preview-ce/compare/v0.6.3...v0.6.4) (2025-08-21)

### Features

* build packages via Docker and fix build ([#95](https://github.com/zextras/carbonio-preview-ce/issues/95)) ([704c576](https://github.com/zextras/carbonio-preview-ce/commit/704c57631471ee0d53726ef677433f2d9485fa5f))

### Bug Fixes

* adapted jenkinsfile to publish on docker registry ([#89](https://github.com/zextras/carbonio-preview-ce/issues/89)) ([e82d30c](https://github.com/zextras/carbonio-preview-ce/commit/e82d30c3b76efa516f10674640426061f44e2238))
## [0.6.3](https://github.com/zextras/carbonio-preview-ce/compare/v0.6.2...v0.6.3) (2025-06-10)

### Bug Fixes

* correct config file path on ubuntu 24 ([#83](https://github.com/zextras/carbonio-preview-ce/issues/83)) ([5fe0d5b](https://github.com/zextras/carbonio-preview-ce/commit/5fe0d5b42ade9bfd55b451b500f8da758b5afa4d))
## [0.6.2](https://github.com/zextras/carbonio-preview-ce/compare/v0.6.1...v0.6.2) (2025-05-15)

### Bug Fixes

* rename locale with lang_tag ([#80](https://github.com/zextras/carbonio-preview-ce/issues/80)) ([68f492d](https://github.com/zextras/carbonio-preview-ce/commit/68f492d06577439ac86acda4be322a8584556674))
## [0.6.1](https://github.com/zextras/carbonio-preview-ce/compare/v0.6.0...v0.6.1) (2025-02-03)
## [0.6.0](https://github.com/zextras/carbonio-preview-ce/compare/v0.5.1...v0.6.0) (2024-11-18)

### Features

* replace health checks from ready to live ([#75](https://github.com/zextras/carbonio-preview-ce/issues/75)) ([40d1900](https://github.com/zextras/carbonio-preview-ce/commit/40d1900d08a67f3bc8d85d28880ca70d38aef696))

### Bug Fixes

* return a 404 or 422 status code when storages APIs fails  ([#74](https://github.com/zextras/carbonio-preview-ce/issues/74)) ([a7f4d34](https://github.com/zextras/carbonio-preview-ce/commit/a7f4d34c4b00e70f35d29497dd766f5b0f0ce630)), closes [storage_communication#retrieve_data](https://github.com/zextras/storage_communication/issues/retrieve_data) [data_validator#check_for_storage_response_error](https://github.com/zextras/data_validator/issues/check_for_storage_response_error)
## [0.5.1](https://github.com/zextras/carbonio-preview-ce/compare/v0.5.0...v0.5.1) (2024-09-10)

### Bug Fixes

* fixed memory leak with each new request ([#70](https://github.com/zextras/carbonio-preview-ce/issues/70)) ([264c745](https://github.com/zextras/carbonio-preview-ce/commit/264c745edcd9eb8d7fe0d4670c9e420b7ebd222b))
## [0.5.0](https://github.com/zextras/carbonio-preview-ce/compare/v0.4.0...v0.5.0) (2024-08-27)

### Features

* add ubuntu 24.04 (ubuntu-noble) support ([0c0d154](https://github.com/zextras/carbonio-preview-ce/commit/0c0d154ad4dd282c7457d4c3ff2ca4bff2026328))
* let users preview SVGs ([#68](https://github.com/zextras/carbonio-preview-ce/issues/68)) ([289ad5b](https://github.com/zextras/carbonio-preview-ce/commit/289ad5bcd000300b05480de47d2b1842b6eaa195))

### Bug Fixes

* keep background trasparent when adding borders ([#66](https://github.com/zextras/carbonio-preview-ce/issues/66)) ([ed1bfd7](https://github.com/zextras/carbonio-preview-ce/commit/ed1bfd7903cb8f00c48c720217ea1a5fd4d2b811))
* remove SNAPSHOT label from setup.py version ([78aadab](https://github.com/zextras/carbonio-preview-ce/commit/78aadabb57039a0945603d9aeba4a30bc7e7daa2))
## [0.4.0](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.9...v0.4.0) (2024-06-18)

### Features

* use custom language to return preview of documents ([#61](https://github.com/zextras/carbonio-preview-ce/issues/61)) ([384468e](https://github.com/zextras/carbonio-preview-ce/commit/384468ec68a253b75449837c1ceeef5cf6896a51))
## [0.3.9](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.8...v0.3.9) (2024-02-15)

### Bug Fixes

* *.hcl: apply corrections to validate with hclfmt ([#58](https://github.com/zextras/carbonio-preview-ce/issues/58)) ([2ed7c6f](https://github.com/zextras/carbonio-preview-ce/commit/2ed7c6f74855b3f9de514869361fffe64bc81b5e))
* incorrect handling of different python interps ([#57](https://github.com/zextras/carbonio-preview-ce/issues/57)) ([32b65fb](https://github.com/zextras/carbonio-preview-ce/commit/32b65fb0c26c610eb40057f013a412421457b8e2))
## [0.3.8](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.7...v0.3.8) (2024-01-17)
## [0.3.7](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.6...v0.3.7) (2024-01-17)

### Bug Fixes

* update RHEL provided dependencies in PKGBUILD ([#53](https://github.com/zextras/carbonio-preview-ce/issues/53)) ([ed9908d](https://github.com/zextras/carbonio-preview-ce/commit/ed9908d2c691933ede8c97e6143b0bfc9c0cd395))
## [0.3.6](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.5...v0.3.6) (2024-01-16)

### Features

* move to yap agent and add rhel9 support ([#50](https://github.com/zextras/carbonio-preview-ce/issues/50)) ([a54c111](https://github.com/zextras/carbonio-preview-ce/commit/a54c1112029dd758ffc38094b59b35b4ccb7b165))
## [0.3.5](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.4...v0.3.5) (2023-08-31)

### Features

* Validate config using pydantic ([#47](https://github.com/zextras/carbonio-preview-ce/issues/47)) ([29e4eda](https://github.com/zextras/carbonio-preview-ce/commit/29e4eda33f4fc8451083320ad4bd2633a083b4f7))
## [0.3.4](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.3...v0.3.4) (2023-07-06)

### Features

* Implement async fetch from storage ([#43](https://github.com/zextras/carbonio-preview-ce/issues/43)) ([277ae52](https://github.com/zextras/carbonio-preview-ce/commit/277ae525a8d4e6ec5f1b9c14b978896b1a7ae7ca))
* Support GIF ([#42](https://github.com/zextras/carbonio-preview-ce/issues/42)) ([56c6a38](https://github.com/zextras/carbonio-preview-ce/commit/56c6a38c9b67dc2d17dd20737cb616b3ec15fd35))
* Validate preview with mypy ([#41](https://github.com/zextras/carbonio-preview-ce/issues/41)) ([b69ee25](https://github.com/zextras/carbonio-preview-ce/commit/b69ee25e1ad9bb8f66e9d173cac820b38b70c10f))

### Bug Fixes

* Allow preview of new PDF versions ([#40](https://github.com/zextras/carbonio-preview-ce/issues/40)) ([0d360d0](https://github.com/zextras/carbonio-preview-ce/commit/0d360d04f864da9916866a58a202af998519d290))
## [0.3.3](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.2...v0.3.3) (2023-05-26)

### Bug Fixes

* Return image type enum value and not name ([#38](https://github.com/zextras/carbonio-preview-ce/issues/38)) ([fa542aa](https://github.com/zextras/carbonio-preview-ce/commit/fa542aa8df99c696bd9f6ec17ffaef3f4764d357))
## [0.3.2](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.1...v0.3.2) (2023-05-25)

### Bug Fixes

* Allow versions greater or equals than 0 ([#36](https://github.com/zextras/carbonio-preview-ce/issues/36)) ([aa90e5b](https://github.com/zextras/carbonio-preview-ce/commit/aa90e5b94a62984dc937ba096b34f84121a08f97))
## [0.3.1](https://github.com/zextras/carbonio-preview-ce/compare/v0.3.0...v0.3.1) (2023-05-08)

### Bug Fixes

* Swap deprecated render_topil  ([#34](https://github.com/zextras/carbonio-preview-ce/issues/34)) ([2b41e23](https://github.com/zextras/carbonio-preview-ce/commit/2b41e23db1ad1d5939e54c9f83936b35c98c33da))
## [0.3.0](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.15...v0.3.0) (2023-04-27)

### Features

* Introduce docs editor ([#30](https://github.com/zextras/carbonio-preview-ce/issues/30)) ([049a2be](https://github.com/zextras/carbonio-preview-ce/commit/049a2beb56016c3558018a329acbe351ddd7d41d))
## [0.2.15](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.14...v0.2.15) (2023-02-28)
## [0.2.14](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.13...v0.2.14) (2023-02-01)

### Bug Fixes

* PREV-100 - Sanitize pattern MUST be of the same type of the buffer ([#28](https://github.com/zextras/carbonio-preview-ce/issues/28)) ([6a13d39](https://github.com/zextras/carbonio-preview-ce/commit/6a13d3991daecf64ff2b2d68b3d30ef90b956625))
* PREV-100 : Remove extra headers at the beginning of pdfs ([#27](https://github.com/zextras/carbonio-preview-ce/issues/27)) ([5494ae2](https://github.com/zextras/carbonio-preview-ce/commit/5494ae23a5b38b868008abce081cd146edb2ca7b))
## [0.2.13](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.12...v0.2.13) (2022-12-06)

### Features

* PREV-96 - Disable Docs-Core via config ([#24](https://github.com/zextras/carbonio-preview-ce/issues/24)) ([9efdee3](https://github.com/zextras/carbonio-preview-ce/commit/9efdee3002c865bc424f23f71f8ab9961759e847))

### Bug Fixes

* PREV-95 - Kill Docs-Core process ([#23](https://github.com/zextras/carbonio-preview-ce/issues/23)) ([ccb1657](https://github.com/zextras/carbonio-preview-ce/commit/ccb1657e47abb4a14b84e8ad2a4439070ca439e3))
* PREV-97 - Update RHEL provided dependencies in PKGBUILD ([#26](https://github.com/zextras/carbonio-preview-ce/issues/26)) ([174d730](https://github.com/zextras/carbonio-preview-ce/commit/174d7305c128e798a7a427910ef058103d8b9730))
## [0.2.12](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.11...v0.2.12) (2022-10-26)
## [0.2.11](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.10...v0.2.11) (2022-09-01)

### Bug Fixes

* PREV-63 - Rotate image with EXIF metadata ([#19](https://github.com/zextras/carbonio-preview-ce/issues/19)) ([1a492ff](https://github.com/zextras/carbonio-preview-ce/commit/1a492ff4fd37ecb07e8e70c34428ce2f8812b80c))
* PREV-85 - Add libre office watchdog ([#18](https://github.com/zextras/carbonio-preview-ce/issues/18)) ([49d6875](https://github.com/zextras/carbonio-preview-ce/commit/49d68750573d017d0b3472ce6953e4cbe822f702))
## [0.2.10](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.9...v0.2.10) (2022-07-20)

### Features

* add python build facilities aimed at packaging simplification ([#12](https://github.com/zextras/carbonio-preview-ce/issues/12)) ([d684154](https://github.com/zextras/carbonio-preview-ce/commit/d684154734920ccc8b5b4b767da8b45690eae23c))

### Bug Fixes

* PREV-71 - Thumbnail of encrypted PDFs are returned blank ([#11](https://github.com/zextras/carbonio-preview-ce/issues/11)) ([c831057](https://github.com/zextras/carbonio-preview-ce/commit/c831057972210aef728e49defa9c812e3a242429))
* PREV-72 - Thumbnail generation goes in timeout ([#13](https://github.com/zextras/carbonio-preview-ce/issues/13)) ([eb1e37e](https://github.com/zextras/carbonio-preview-ce/commit/eb1e37e6953d85a3c4af9d515b64b4d695389c0b))
* PREV-80 - add missing group at user creation ([#15](https://github.com/zextras/carbonio-preview-ce/issues/15)) ([d132562](https://github.com/zextras/carbonio-preview-ce/commit/d1325629533e154eaa5cd7014f3bbf1583844a40))
## [0.2.9](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.8...v0.2.9) (2022-06-09)

### Bug Fixes

* PREV-68 - LibreOffice is spiking Memory Usage ([#9](https://github.com/zextras/carbonio-preview-ce/issues/9)) ([f197ddd](https://github.com/zextras/carbonio-preview-ce/commit/f197ddded20f0d84c3c6b00694457a126965c813))
* PREV-70 - Rotate logs daily ([#10](https://github.com/zextras/carbonio-preview-ce/issues/10)) ([6754f6f](https://github.com/zextras/carbonio-preview-ce/commit/6754f6f65de6039a2902269165844cf6b6c1319a))
## [0.2.8](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.7...v0.2.8) (2022-05-16)

### Bug Fixes

* PREV-51 - Fix 500 when input file empty ([#8](https://github.com/zextras/carbonio-preview-ce/issues/8)) ([60d3cdc](https://github.com/zextras/carbonio-preview-ce/commit/60d3cdcf86474802bd82dfe56d92673e723726a5))
## [0.2.7](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.4...v0.2.7) (2022-05-09)

### Features

* PREV-12 - configure logging ([#4](https://github.com/zextras/carbonio-preview-ce/issues/4)) ([0cc5b35](https://github.com/zextras/carbonio-preview-ce/commit/0cc5b352a502f4db1d10699e549c2dba1df6d30a))

### Bug Fixes

* PREV-54 - restart LibreOffice instance when worker restarts ([#5](https://github.com/zextras/carbonio-preview-ce/issues/5)) ([2d4a51f](https://github.com/zextras/carbonio-preview-ce/commit/2d4a51ff2b17218d9f0886839888ff77c4846b60))
* PREV-55 - Change libreoffice-calc package ([#6](https://github.com/zextras/carbonio-preview-ce/issues/6)) ([a45e99f](https://github.com/zextras/carbonio-preview-ce/commit/a45e99f815401a3b66bc87ca19aaf1b6154542c6))
## [0.2.4](https://github.com/zextras/carbonio-preview-ce/compare/v0.2.2...v0.2.4) (2022-04-08)

### Features

* PREV-19 - crop thumbnail from the top for documents ([#2](https://github.com/zextras/carbonio-preview-ce/issues/2)) ([d0a94a0](https://github.com/zextras/carbonio-preview-ce/commit/d0a94a0d19005f4e9a51bb62b5d27cab5eca11d3))
* PREV-48 - document's preview and thumbnail ([#3](https://github.com/zextras/carbonio-preview-ce/issues/3)) ([346c9a4](https://github.com/zextras/carbonio-preview-ce/commit/346c9a44c6fc93ba3d5dec272b25e14d92245fea))
## [0.2.2](https://github.com/zextras/carbonio-preview-ce/compare/c2112b9620a8baaa3277e9171e7acdefcb5c4745...v0.2.2) (2022-03-23)

### Features

* carbonio release ([c2112b9](https://github.com/zextras/carbonio-preview-ce/commit/c2112b9620a8baaa3277e9171e7acdefcb5c4745))

### Bug Fixes

* PREV-46 / PREV-47 - fix requirements ([3f83243](https://github.com/zextras/carbonio-preview-ce/commit/3f83243bf6fa4bd07b144ed06dd947ed3383e644))
