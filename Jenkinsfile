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
// zextrasRepoCredentialsId is required because PKGBUILD depends (runtime)
// include pending-setups, service-discover, libcairo2-dev — these live in
// the Zextras repo and yap build must resolve them.
// Equivalent of Gen-1's addCarbonioRepos: true.
dt3_pipeline(
    repoName: 'carbonio-preview-ce',
    packaging: [
        pkgbuildPath: 'package/preview/PKGBUILD',
        zextrasRepoCredentialsId: 'artifactory-jenkins-gradle-properties-splitted',
        ubuntuSinglePkg: false,
        rockySinglePkg: false,
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
