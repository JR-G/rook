#!/usr/bin/env bash

set -euo pipefail

limit_bytes=$((2 * 1024 * 1024))
violations=0

while IFS= read -r path; do
  size="$(stat -f%z "$path")"
  if [ "$size" -gt "$limit_bytes" ]; then
    printf 'file too large: %s (%s bytes)\n' "$path" "$size"
    violations=1
  fi
done < <(find . -type f \
  ! -path './.git/*' \
  ! -path './bin/*' \
  ! -path './data/*')

exit "$violations"
