#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
config_path="${1:-$repo_root/config/rook.toml}"
keychain_service="io.rook.agent.slack"
keychain_path="${ROOK_KEYCHAIN_PATH:-$HOME/Library/Keychains/login.keychain-db}"

if [ ! -f "$keychain_path" ]; then
  keychain_path=""
fi

if [ ! -f "$config_path" ]; then
  echo "config file not found: $config_path" >&2
  exit 1
fi

if [ -z "${ROOK_SLACK_BOT_TOKEN:-}" ] && [ -n "$keychain_path" ]; then
  export ROOK_SLACK_BOT_TOKEN="$(security find-generic-password -s "$keychain_service" -a bot_token -g "$keychain_path" 2>&1 | grep '^password:' | sed 's/^password: "//' | sed 's/"$//')"
fi

if [ -z "${ROOK_SLACK_APP_TOKEN:-}" ] && [ -n "$keychain_path" ]; then
  export ROOK_SLACK_APP_TOKEN="$(security find-generic-password -s "$keychain_service" -a app_token -g "$keychain_path" 2>&1 | grep '^password:' | sed 's/^password: "//' | sed 's/"$//')"
fi

if [ -z "${ROOK_SLACK_BOT_TOKEN:-}" ] || [ -z "${ROOK_SLACK_APP_TOKEN:-}" ]; then
  echo "ERROR: Slack tokens not found. Run: task slack-keychain-store" >&2
  exit 1
fi

exec "$repo_root/bin/rook" serve -config "$config_path"
