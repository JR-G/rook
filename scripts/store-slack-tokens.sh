#!/usr/bin/env bash

set -euo pipefail

keychain_service="io.rook.agent.slack"

read -r -s -p "Slack bot token (xoxb-...): " bot_token
echo
read -r -s -p "Slack app token (xapp-...): " app_token
echo

if [ -z "$bot_token" ] || [ -z "$app_token" ]; then
  echo "both Slack tokens are required" >&2
  exit 1
fi

security add-generic-password -U -s "$keychain_service" -a bot_token -w "$bot_token" >/dev/null
security add-generic-password -U -s "$keychain_service" -a app_token -w "$app_token" >/dev/null

echo "stored Slack tokens in macOS Keychain service: $keychain_service"
