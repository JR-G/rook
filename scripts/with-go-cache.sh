#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
export GOCACHE="${GOCACHE:-$repo_root/.cache/go-build}"
mkdir -p "$GOCACHE"

exec "$@"
