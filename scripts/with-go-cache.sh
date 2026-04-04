#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
export GOCACHE="${GOCACHE:-/tmp/rook-gocache}"
export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-$repo_root/.cache/golangci-lint}"
mkdir -p "$GOCACHE"
mkdir -p "$GOLANGCI_LINT_CACHE"

exec "$@"
