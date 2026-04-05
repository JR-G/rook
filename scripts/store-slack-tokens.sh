#!/usr/bin/env bash

set -euo pipefail

keychain_service="io.rook.agent.slack"
keychain_path="${ROOK_KEYCHAIN_PATH:-$HOME/Library/Keychains/login.keychain-db}"

if [ ! -f "$keychain_path" ]; then
  echo "login keychain not found: $keychain_path" >&2
  exit 1
fi


store_secret() {
  local account="$1"
  local secret="$2"

  security delete-generic-password \
    -s "$keychain_service" \
    -a "$account" \
    "$keychain_path" >/dev/null 2>&1 || true

  if security add-generic-password \
    -T /usr/bin/security \
    -s "$keychain_service" \
    -a "$account" \
    -w "$secret" \
    "$keychain_path" >/dev/null 2>&1; then
    return
  fi

  cat >&2 <<EOF
failed to write to the macOS login keychain.

Try this first:
  security unlock-keychain "$keychain_path"

Then run this script again.
EOF
  exit 1
}

read -r -s -p "Slack bot token (xoxb-...): " bot_token
echo
read -r -s -p "Slack app token (xapp-...): " app_token
echo

if [ -z "$bot_token" ] || [ -z "$app_token" ]; then
  echo "both Slack tokens are required" >&2
  exit 1
fi

store_secret bot_token "$bot_token"
store_secret app_token "$app_token"

unset bot_token
unset app_token

echo "stored Slack tokens in macOS login keychain service: $keychain_service"
