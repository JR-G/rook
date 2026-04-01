#!/usr/bin/env bash

set -euo pipefail

mkdir -p .cache/go-build bin data data/logs

if [ ! -f config/rook.toml ]; then
  cp config/rook.example.toml config/rook.toml
fi

lefthook install

echo 'rook development environment is ready'
