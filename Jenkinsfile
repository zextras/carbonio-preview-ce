// SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

library(
    identifier: 'jenkins-lib-common@v4.5.0',
    retriever: modernSCM([$class: 'GitSCMSource',
        credentialsId: 'jenkins-integration-with-github-account',
        remote: 'git@github.com:zextras/jenkins-lib-common.git'])
)

// PKGBUILD's source=() needs a tarball, not a git checkout, so preBuildScript packages one.
dt3_pipeline(
    repoName: 'carbonio-preview-ce',
    mavenPublish: ['sdk'],
    nonJavaSdkPublish: true,
    goGenerate: [
        paths: ['docs/'],
    ],
    packaging: [
        addCarbonioRepos: true,
        preBuildScript: '''
            set -e
            tar czf package/carbonio-preview-ce-src.tar.gz \
                --exclude='.git' \
                --exclude='venv' \
                --exclude='package' \
                --exclude='*.pyc' \
                --exclude='__pycache__' \
                .
        ''',
    ],
    docker: [[
        dockerfile: 'docker/Dockerfile',
        imageName: 'carbonio-preview-ce',
        title: 'Carbonio Preview CE',
        description: 'Carbonio Preview Community Edition',
        platforms: ['linux/amd64', 'linux/arm64'] as Set,
    ]],
    reuse: [projectType: 'CE'],
    flywayGuard: [
        migrationPaths: ['db/migration'],
    ],
)
