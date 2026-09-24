#!/usr/bin/env bash
#
# Build goft for one or more platforms.
#
#   ./build.sh                            # the default set of platforms
#   ./build.sh linux/amd64                # just one
#   ./build.sh linux/amd64 windows/amd64  # a few
#
# Each binary lands in build/<os>/<arch>/, so the layout mirrors what it is:
#
#   build/linux/amd64/goft
#   build/windows/amd64/goft.exe
#
# Environment:
#   VERSION   version stamped into the binary (default: git describe against
#             the v* tags, or "dev")
#   OUT_DIR   root of the output tree (default: build)
#   LDFLAGS   extra linker flags, appended to the ones set here

set -euo pipefail

readonly BINARY=goft
readonly OUT_DIR=${OUT_DIR:-build}

# Platforms goft is expected to run on. Everything is pure Go, so none of these
# need a cross toolchain.
readonly DEFAULT_PLATFORMS=(
    linux/amd64
    linux/arm64
    darwin/amd64
    darwin/arm64
    windows/amd64
    windows/arm64
)

# usage prints the header comment of this file, so the documentation and the
# help text can never drift apart.
usage() {
    awk 'NR < 3 { next } !/^#/ { exit } { sub(/^# ?/, ""); print }' "$0"
    exit "${1:-0}"
}

# stamp resolves the build metadata that goes into `goft version`.
#
# The repository is not always a git checkout (a release tarball, say), so a
# missing git is a normal case rather than an error.
stamp() {
    local version commit
    version=${VERSION:-}
    commit=none

    if git rev-parse --git-dir >/dev/null 2>&1; then
        [ -n "$version" ] || version=$(git describe --tags --match 'v*' --always --dirty 2>/dev/null || echo dev)
        commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)
    fi
    [ -n "$version" ] || version=dev

    # SOURCE_DATE_EPOCH is honoured so that a reproducible build stays
    # reproducible.
    local built
    if [ -n "${SOURCE_DATE_EPOCH:-}" ]; then
        built=$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
             || date -u -r "$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)
    else
        built=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    fi

    printf '%s\n%s\n%s\n' "$version" "$commit" "$built"
}

main() {
    case "${1:-}" in
        -h | --help) usage 0 ;;
    esac

    local platforms=("$@")
    [ ${#platforms[@]} -gt 0 ] || platforms=("${DEFAULT_PLATFORMS[@]}")

    cd "$(dirname "$0")"

    local version commit built
    { read -r version; read -r commit; read -r built; } < <(stamp)

    # -s -w drop the symbol table and DWARF; -trimpath keeps local paths out of
    # the binary, which also makes the build reproducible.
    local ldflags="-s -w"
    ldflags+=" -X ${BINARY}/cmd.version=${version}"
    ldflags+=" -X ${BINARY}/cmd.commit=${commit}"
    ldflags+=" -X ${BINARY}/cmd.date=${built}"
    ldflags+=" ${LDFLAGS:-}"

    printf 'goft %s (commit %s, built %s)\n\n' "$version" "$commit" "$built"

    local failed=0
    local platform goos goarch output
    for platform in "${platforms[@]}"; do
        if [[ "$platform" != */* ]]; then
            printf 'skipping %s: expected <os>/<arch>\n' "$platform" >&2
            failed=1
            continue
        fi
        goos=${platform%%/*}
        goarch=${platform##*/}

        output="${OUT_DIR}/${goos}/${goarch}/${BINARY}"
        [ "$goos" = windows ] && output+=".exe"

        mkdir -p "$(dirname "$output")"
        printf '%-22s ' "$platform"
        if CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
            go build -trimpath -ldflags "$ldflags" -o "$output" . ; then
            printf '%8s  %s\n' "$(du -h "$output" | cut -f1)" "$output"
        else
            printf 'FAILED\n'
            failed=1
        fi
    done

    if [ "$failed" -ne 0 ]; then
        printf '\nsome targets failed\n' >&2
        exit 1
    fi
}

main "$@"
