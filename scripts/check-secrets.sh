#!/usr/bin/env bash

set -euo pipefail

pattern='(xoxb-[A-Za-z0-9-]+|xapp-[A-Za-z0-9-]+|sk-[A-Za-z0-9]{20,}|BEGIN PRIVATE KEY)'

if rg -n --hidden --glob '!.git' --glob '!bin' --glob '!data' --glob '!coverage.out' --glob '!go.sum' "$pattern" .; then
  echo 'possible secret detected'
  exit 1
fi
