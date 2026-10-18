#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for script in scripts/*.sh .githooks/pre-commit; do
  bash -n "$script"
done
# Compile in memory: do not leave __pycache__ in the worktree.
python3 -I -c 'from pathlib import Path; [compile(p.read_bytes(), str(p), "exec") for directory in ("tests/abi", "scripts") for p in Path(directory).glob("*.py")]'
