#!/usr/bin/env bash

set -euo pipefail

mkdir -p bin data

if [ ! -f config/rook.toml ]; then
  cp config/rook.example.toml config/rook.toml
fi

lefthook install

echo 'rook development environment is ready'
