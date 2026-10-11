#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
if [[ $# -gt 1 ]]; then
  printf 'Usage: %s [shared-library]\n' "$0" >&2
  exit 1
fi
os=$(go env GOHOSTOS)
arch=$(go env GOHOSTARCH)
case "$os" in
  linux) extension=so; linker_flags=(-ldl) ;;
  darwin) extension=dylib; linker_flags=() ;;
  *) printf 'ABI smoke requires native Linux or macOS\n' >&2; exit 1 ;;
esac

# An explicit library tests an existing CI artefact; otherwise build locally.
library=${1:-"$root/dist/$os-$arch/cpa-cursor.$extension"}
if [[ $# == 0 ]]; then
  bash scripts/build.sh
fi
header_dir=$(cd "$(dirname "$library")" && pwd)
library="$header_dir/$(basename "$library")"
temporary=$(mktemp -d "${TMPDIR:-/tmp}/cpa-cursor-abi.XXXXXX")
trap 'rm -rf "$temporary"' EXIT
"${CC:-cc}" -std=c11 -Wall -Wextra -Werror -I "$header_dir" \
  tests/abi/host.c -o "$temporary/host" "${linker_flags[@]}"
# Isolated mode ignores PYTHONOPTIMIZE, so assertions cannot be disabled.
python3 -I tests/abi/smoke.py "$temporary/host" "$library"
