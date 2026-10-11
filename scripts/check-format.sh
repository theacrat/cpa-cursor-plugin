#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
unformatted=$(gofmt -l .)
if [[ -n "$unformatted" ]]; then
  printf 'Run mise run fmt to format:\n%s\n' "$unformatted" >&2
  exit 1
fi
