#!/usr/bin/env bash

set -euo pipefail

threshold="${1:-95}"
go test -coverprofile=coverage.out ./... >/dev/null
coverage="$(go tool cover -func=coverage.out | awk '/^total:/ { sub("%", "", $3); print $3 }')"

awk -v threshold="$threshold" -v coverage="$coverage" '
BEGIN {
  if (coverage + 0 < threshold + 0) {
    printf("coverage %.1f%% is below threshold %.1f%%\n", coverage, threshold)
    exit 1
  }
  printf("coverage %.1f%% meets threshold %.1f%%\n", coverage, threshold)
}'
