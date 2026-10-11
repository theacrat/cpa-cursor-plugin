#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
# Sequential execution avoids competing native builds and coverage writes.
bash scripts/check-format.sh
bash scripts/check-scripts.sh
CGO_ENABLED=1 go vet ./...
CGO_ENABLED=0 go test ./...
CGO_ENABLED=1 go test -race -coverprofile=coverage.out ./...
bash scripts/abi-smoke.sh
