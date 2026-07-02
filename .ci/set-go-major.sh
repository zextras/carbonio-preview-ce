#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Keep the Go module's /vN major-version suffix in sync with the released major.
#
# Go's semantic import versioning requires a module at major version >= 2 to carry
# a "/vN" suffix in its module path (and in every internal import). Otherwise a
# consumer cannot `require github.com/.../carbonio-preview-ce/vN@vN.Y.Z` — it is
# forced onto a commit pseudo-version. semantic-release computes the next version;
# this script makes the module path follow it, so a `feat!:` that bumps the major
# never silently leaves the path behind (no manual step to forget).
#
# Invoked from .releaserc.json prepareCmd:  bash .ci/set-go-major.sh <next-version>
#
# Fires ONLY when the major actually changes (no-op on minor/patch). Pure sed/grep,
# so it needs no Go toolchain in the semantic-release pod.
#
# NOTE: this runs at release time, AFTER the CI build stages. The rewrite is a
# deterministic, anchored sed (no import can be missed the way a hand-edit can),
# but the exact /vN commit is only compiled by the *next* build. A follow-up
# hardening is to do this rewrite pre-build in the pipeline, gated on a
# semantic-release dry-run — see the PR discussion.
set -euo pipefail

next="${1:?usage: set-go-major.sh <next-version>}"
new_major="${next%%.*}"

base="github.com/zextras/carbonio-preview-ce"
cur="$(awk '/^module /{print $2; exit}' go.mod)"

# Desired module path: bare for v0/v1, "$base/vN" for N >= 2.
if [ "${new_major}" -ge 2 ]; then
  desired="${base}/v${new_major}"
else
  desired="${base}"
fi

if [ "${cur}" = "${desired}" ]; then
  echo "[set-go-major] module path already '${desired}' (release ${next}) — nothing to do"
  exit 0
fi

echo "[set-go-major] major changed for release ${next}: '${cur}' -> '${desired}'"

# 1) go.mod module directive.
sed -i "s#^module[[:space:]].*#module ${desired}#" go.mod

# 2) internal imports. Anchor on a closing quote OR a trailing slash so we only
#    touch this module's own import paths and never a longer, unrelated path
#    (e.g. a hypothetical ...-rest-sdk) or a bare substring in a comment.
grep -rlZ --include='*.go' "${cur}" . | while IFS= read -r -d '' f; do
  sed -i \
    -e "s#\"${cur}\"#\"${desired}\"#g" \
    -e "s#${cur}/#${desired}/#g" \
    "${f}"
done

echo "[set-go-major] rewrote module path + internal imports to '${desired}'"
