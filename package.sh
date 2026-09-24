#!/usr/bin/env bash
#
# Package goft for a release.
#
#   ./package.sh                            # every default platform
#   ./package.sh linux/amd64                # just one
#   ./package.sh linux/amd64 windows/amd64  # a few
#
# Each platform becomes one archive under dist/, with a SHA256SUMS covering all
# of them:
#
#   dist/goft_v0.1.0_linux_amd64.tar.gz
#   dist/goft_v0.1.0_windows_amd64.zip
#   dist/SHA256SUMS
#
# An archive holds the binary, both READMEs, the licence and the annotated
# example configuration, so an unpacked release is usable without the
# repository.
#
# Environment:
#   VERSION            version stamped into the binaries and the archive names
#                      (default: git describe against the v* tags, or "dev";
#                      the main-* tags CI adds are never used for a name)
#   OUT_DIR            where the binaries are built (default: build); it is
#                      emptied first, so that a stale binary from an earlier
#                      build cannot end up in a release
#   DIST_DIR           where the archives go (default: dist)
#   SOURCE_DATE_EPOCH  timestamp for the archive contents, so that building the
#                      same commit twice produces the same bytes (default: the
#                      commit's own date)

set -euo pipefail

readonly BINARY=goft
readonly OUT_DIR=${OUT_DIR:-build}
readonly DIST_DIR=${DIST_DIR:-dist}
readonly EXTRA_FILES=(README.md README-ja.md LICENSE goft.example.yaml)

usage() {
    awk 'NR < 3 { next } !/^#/ { exit } { sub(/^# ?/, ""); print }' "$0"
    exit "${1:-0}"
}

case "${1:-}" in
-h | --help) usage ;;
esac

cd "$(dirname "$0")"

# The version and the timestamp are resolved here rather than left to build.sh,
# because the archive names and the file dates have to agree with what was
# stamped into the binaries.
if [ -z "${VERSION:-}" ]; then
    VERSION=$(git describe --tags --match 'v*' --always --dirty 2>/dev/null || echo dev)
fi
if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
    SOURCE_DATE_EPOCH=$(git log -1 --format=%ct 2>/dev/null || date +%s)
fi
export VERSION SOURCE_DATE_EPOCH

rm -rf "$DIST_DIR" "$OUT_DIR"
mkdir -p "$DIST_DIR"

./build.sh "$@"

# The platforms are taken from what was built rather than listed again here, so
# the two scripts cannot drift apart.
archive() {
    local binary=$1
    local rel=${binary#"$OUT_DIR"/}
    local os=${rel%%/*}
    local arch=$(basename "$(dirname "$binary")")
    local name="${BINARY}_${VERSION}_${os}_${arch}"
    local stage="$DIST_DIR/$name"

    mkdir -p "$stage"
    cp "$binary" "$stage/"
    cp "${EXTRA_FILES[@]}" "$stage/"
    find "$stage" -exec touch -d "@$SOURCE_DATE_EPOCH" {} +

    if [ "$os" = windows ]; then
        zip_dir "$name"
    else
        tar --sort=name --owner=0 --group=0 --numeric-owner \
            --mtime="@$SOURCE_DATE_EPOCH" \
            -czf "$DIST_DIR/$name.tar.gz" -C "$DIST_DIR" "$name"
    fi
    rm -rf "$stage"
}

# zip_dir prefers the zip command and falls back to Python, which every machine
# that can run the tests already has.
zip_dir() {
    local name=$1
    if command -v zip >/dev/null 2>&1; then
        (cd "$DIST_DIR" && zip -q -X -r "$name.zip" "$name")
    else
        (cd "$DIST_DIR" && python3 -m zipfile -c "$name.zip" "$name")
    fi
}

while IFS= read -r binary; do
    archive "$binary"
done < <(find "$OUT_DIR" -type f \( -name "$BINARY" -o -name "$BINARY.exe" \) | sort)

checksums() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$@"
    else
        shasum -a 256 "$@"
    fi
}

(cd "$DIST_DIR" && checksums ${BINARY}_* >SHA256SUMS)

echo
for f in "$DIST_DIR"/*; do
    printf '%-46s %s\n' "$f" "$(du -h "$f" | cut -f1)"
done
