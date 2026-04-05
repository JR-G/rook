#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
config_path="${1:-$repo_root/config/rook.toml}"
keychain_service="io.rook.agent.slack"
keychain_path="${ROOK_KEYCHAIN_PATH:-$HOME/Library/Keychains/login.keychain-db}"

if [ ! -f "$keychain_path" ]; then
  keychain_path=""
fi

load_keychain_secret() {
  local account="$1"

  security find-generic-password \
    -s "$keychain_service" \
    -a "$account" \
    ${keychain_path:+"$keychain_path"} \
    -w 2>/dev/null
}

export_if_unset() {
  local env_name="$1"
  local account="$2"
  local secret=""

  if [ -n "${!env_name:-}" ]; then
    return
  fi

  if secret="$(load_keychain_secret "$account")"; then
    export "$env_name=$secret"
  fi
}

if [ ! -f "$config_path" ]; then
  echo "config file not found: $config_path" >&2
  exit 1
fi

if [ -n "$keychain_path" ]; then
  security unlock-keychain "$keychain_path" 2>/dev/null || true
fi

export_if_unset ROOK_SLACK_BOT_TOKEN bot_token
export_if_unset ROOK_SLACK_APP_TOKEN app_token

exec "$repo_root/bin/rook" serve -config "$config_path"
