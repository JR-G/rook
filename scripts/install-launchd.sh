#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
config_path="${1:-$repo_root/config/rook.toml}"
log_dir="$repo_root/data/logs"
label="io.rook.agent"
launch_agents_dir="$HOME/Library/LaunchAgents"
plist_template="$repo_root/launchd/$label.plist.template"
plist_path="$launch_agents_dir/$label.plist"

mkdir -p "$launch_agents_dir" "$log_dir"

if [ ! -f "$config_path" ]; then
  echo "config file not found: $config_path" >&2
  exit 1
fi

sed \
  -e "s|__REPO_ROOT__|$repo_root|g" \
  -e "s|__CONFIG_PATH__|$config_path|g" \
  -e "s|__LOG_DIR__|$log_dir|g" \
  "$plist_template" > "$plist_path"

launchctl unload "$plist_path" >/dev/null 2>&1 || true
launchctl load "$plist_path"

echo "installed launchd agent: $plist_path"
