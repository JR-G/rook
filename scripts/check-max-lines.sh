#!/usr/bin/env bash

set -euo pipefail

limit="${1:-500}"
violations=0

while IFS= read -r path; do
  lines="$(wc -l < "$path" | tr -d ' ')"
  if [ "$lines" -gt "$limit" ]; then
    printf 'file exceeds %s lines: %s (%s lines)\n' "$limit" "$path" "$lines"
    violations=1
  fi
done < <(
  find . -type f \
    \( -name '*.go' -o -name '*.sh' -o -name '*.md' -o -name '*.yml' -o -name '*.yaml' -o -name '*.toml' -o -name '*.plist' \) \
    ! -path './.git/*' \
    ! -path './bin/*' \
    ! -path './data/*' \
    ! -path './go.sum'
)

exit "$violations"
