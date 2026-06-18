// SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

library(
    identifier: 'jenkins-lib-common@dt3-pipeline',
    retriever: modernSCM([$class: 'GitSCMSource',
        credentialsId: 'jenkins-integration-with-github-account',
        remote: 'git@github.com:zextras/jenkins-lib-common.git'])
)

// carbonio-preview-ce is a Go project (feat/rewrite branch).
// PKGBUILD source=() expects a carbonio-preview-ce-src.tar.gz tarball;
// preBuildScript creates it inside the YAP container before `yap build`.
// The Go build (go mod download + go build) runs entirely inside the PKGBUILD
// build__* functions — no Go toolchain is needed on the Jenkins agent itself.
//
// addCarbonioRepos wires the Carbonio repos so yap build can resolve the
// PKGBUILD runtime deps (pending-setups, service-discover, carbonio-ffmpeg)
// that live in the Zextras repo.
dt3_pipeline(
    repoName: 'carbonio-preview-ce',
    mavenPublish: ['sdk'],
    nonJavaSdkPublish: true,
    packaging: [
        pkgbuildPath: 'package/preview/PKGBUILD',
        addCarbonioRepos: true,
        preBuildScript: '''
            set -e
            tar czf package/preview/carbonio-preview-ce-src.tar.gz \
                --exclude='.git' \
                --exclude='venv' \
                --exclude='package' \
                --exclude='*.pyc' \
                --exclude='__pycache__' \
                --exclude='sdk' \
                --exclude='app' \
                --exclude='tests' \
                --exclude='coverage.out' \
                --exclude='coverage.html' \
                --exclude='carbonio-preview' \
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
)
