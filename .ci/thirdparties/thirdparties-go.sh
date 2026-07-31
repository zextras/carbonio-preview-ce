#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Generates the Go THIRDPARTIES dependency-licence manifest, grouped by SPDX licence id.
# Vendored copy of jenkins-lib-common's dt3_thirdparties Go engine (resources/thirdparties-go.sh),
# so it can also run from a local pre-commit hook, not just the Jenkins agent.
#
# The allowlist in thirdparties-go-rules.json FAILS this script on an unrecognised/unlisted
# licence on purpose — licence governance, not a bug; a human must vet and add it.
#
# Usage: thirdparties-go.sh [output-file] (defaults to THIRDPARTIES)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OUT="${1:-THIRDPARTIES}"

# Pinned for reproducibility — bump here AND in jenkins-lib-common's resources/thirdparties-go.sh
# to upgrade go-licence-detector.
GLD_PKG='go.elastic.co/go-licence-detector@v0.10.0'

RULES="${SCRIPT_DIR}/thirdparties-go-rules.json"
TMPL="${SCRIPT_DIR}/thirdparties-go-deps.tmpl"
FLAT="$(mktemp)"
trap 'rm -f "$FLAT"' EXIT

# GOWORK=off: a go.work in a PARENT dir turns this repo's own deps into main modules and drops them from the manifest — a licence-compliance hole, not drift.
export GOWORK=off
# GOFLAGS must be non-empty: empty falls through to the $GOENV file, so a persisted -tags=... there would still silently apply.
export GOFLAGS=-tags=
# CGO_ENABLED=1: the shipped binary links libvips/pdfium with cgo on — this is the superset module set THIRDPARTIES must describe.
export CGO_ENABLED=1

# CGO_ENABLED=0 here only affects how go-licence-detector's OWN binary links, never this repo's package graph — do not "harmonise" this with the export above.
CGO_ENABLED=0 go install "$GLD_PKG"

GLD_BIN="$(go env GOBIN)"
[ -n "$GLD_BIN" ] || GLD_BIN="$(go env GOPATH)/bin"
GLD="${GLD_BIN}/go-licence-detector"

# -test: a testify-only dependency (imported only from _test.go) must still be reported; plain `go list -deps ./...` would drop it.
# -buildvcs=false: without it, `go list -deps` probes VCS status and dies with "exit status 128" in CI containers.
# LC_ALL=C here too, for the same reason as the sort before the final awk below.
if ! MODS_RAW="$(go list -deps -test -buildvcs=false -f '{{with .Module}}{{if not .Main}}{{.Path}}{{end}}{{end}}' ./... | LC_ALL=C sort -u | sed '/^$/d')"; then
    echo "thirdparties-go.sh: 'go list -deps -test ./...' failed — check for a genuine compile error, private-module auth, or GOPROXY issues (GOFLAGS/go.work are already neutralised above)." >&2
    exit 1
fi

MODS=()
if [ -n "$MODS_RAW" ]; then
    mapfile -t MODS <<< "$MODS_RAW"
fi

if [ "${#MODS[@]}" -eq 0 ]; then
    # Short-circuit explicitly: `go list -m -json` with no module args silently describes the MAIN module instead of returning nothing.
    echo "thirdparties-go.sh: no external modules in the build+test graph — nothing to report, skipping go-licence-detector." >&2
    : > "$OUT"
    exit 0
fi

# go-licence-detector enforces the allowlist in thirdparties-go-rules.json: an unlisted/unknown licence fails here on purpose (a human must vet and add it).
# Feed it these scoped MODS, never `go list -m -json all`: `all` keys off the ambient module cache and leaked 75 entries instead of 26 on a warm ~/go. Do not revert this.
go list -m -json "${MODS[@]}" | "$GLD" \
    -includeIndirect \
    -rules="$RULES" \
    -depsTemplate="$TMPL" \
    -depsOut="$FLAT"

# LC_ALL=C: THIS sort fixes the manifest's byte order — a dev shell (UTF-8) and CI (usually C) collate capitalised module paths (BurntSushi, Masterminds, Azure) differently, so an unpinned sort makes the same dependencies produce a different file depending on $LANG.
# Groups lines the way license-maven-plugin's own third-party-file-groupByLicense.ftl does, so Java and Go manifests read alike.
# KEEP THIS AWK BYTE-FOR-BYTE IDENTICAL to jenkins-lib-common's resources/thirdparties-go.sh — Jenkins re-runs that copy and diffs it against what is committed here.
LC_ALL=C sort "$FLAT" | sed '/^$/d' | awk -F'|' '
    NR == 1 {
        print ""
        print "List of third-party dependencies grouped by their license type."
    }
    $1 != prev {
        print ""
        print "    " $1 ":"
        print ""
        prev = $1
    }
    { printf "        * %s (%s)\n", $2, $3 }' > "$OUT"
