#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
if [[ $# -gt 2 ]]; then
  printf 'Usage: %s [linux|darwin [amd64|arm64]]\n' "$0" >&2
  exit 1
fi
host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
os=${1:-$host_os}
arch=${2:-$host_arch}
case "$os/$arch" in
  linux/amd64|linux/arm64) extension=so ;;
  darwin/amd64|darwin/arm64) extension=dylib ;;
  *) printf 'Unsupported target: %s/%s\n' "$os" "$arch" >&2; exit 1 ;;
esac

output="dist/$os-$arch/cpa-cursor.$extension"
mkdir -p "$(dirname "$output")"
if [[ "$os" == "$host_os" && "$arch" == "$host_arch" ]]; then
  CGO_ENABLED=1 GOOS="$os" GOARCH="$arch" go build -trimpath -buildmode=c-shared -o "$output" ./cmd/cpa-cursor
elif [[ "$os" == darwin ]]; then
  if [[ "$host_os" != darwin ]]; then
    printf 'macOS builds require a macOS host and its SDK; use the GitHub Actions build.\n' >&2
    exit 1
  fi
  CGO_ENABLED=1 GOOS="$os" GOARCH="$arch" CC=clang go build -trimpath -buildmode=c-shared -o "$output" ./cmd/cpa-cursor
else
  case "$arch" in
    arm64) target=aarch64-linux-gnu.2.36 ;;
    amd64) target=x86_64-linux-gnu.2.36 ;;
  esac
  CGO_ENABLED=1 GOOS=linux GOARCH="$arch" CC="zig cc -target $target" \
    go build -trimpath -buildmode=c-shared -o "$output" ./cmd/cpa-cursor
fi
printf 'Built %s\n' "$output"
