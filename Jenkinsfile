// SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

library(
    identifier: 'jenkins-lib-common@dt3-pipeline',
    retriever: modernSCM([
        $class: 'GitSCMSource',
        credentialsId: 'jenkins-integration-with-github-account',
        remote: 'git@github.com:zextras/jenkins-lib-common.git',
    ])
)

// carbonio-preview-ce is a Python project.
// PKGBUILD source=() expects a carbonio-preview-src.tar.gz tarball;
// preBuildScript creates it inside the YAP container before `yap build`.
//
// addCarbonioRepos wires the Carbonio repos so yap build can resolve the
// PKGBUILD runtime deps (pending-setups, service-discover, libcairo2-dev)
// that live in the Zextras repo.
dt3_pipeline(
    repoName: 'carbonio-preview-ce',
    packaging: [
        pkgbuildPath: 'package/preview/PKGBUILD',
        addCarbonioRepos: true,
        overrides: [
            ubuntu: [
                preBuildScript: '''
                    tar czf package/preview/carbonio-preview-src.tar.gz \
                        app package requirements.txt README.md setup.py
                ''',
            ],
            rocky: [
                preBuildScript: '''
                    tar czf package/preview/carbonio-preview-src.tar.gz \
                        app package requirements.txt README.md setup.py
                ''',
            ],
        ],
    ],
    docker: [[
        dockerfile: 'docker/minimal/carbonio-preview/Dockerfile',
        imageName: 'carbonio-preview-ce',
        title: 'Carbonio Preview CE',
        description: 'Carbonio Preview Community Edition',
        platforms: ['linux/amd64', 'linux/arm64'] as Set,
    ]],
    reuse: [projectType: 'CE'],
)
