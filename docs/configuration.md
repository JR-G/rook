# Configuration

Configuration is read from TOML and then overlaid with selected `ROOK_*` environment variables.

Recommended secret handling on macOS:

- keep `slack.bot_token` and `slack.app_token` blank in `config/rook.toml`
- store both tokens in macOS Keychain with `task slack-keychain-store`
- start `rook` through `scripts/run-rook.sh`, which loads `ROOK_SLACK_BOT_TOKEN` and `ROOK_SLACK_APP_TOKEN` from Keychain if they are not already set
- if Keychain writes fail with `User interaction is not allowed`, unlock the login keychain first with `security unlock-keychain ~/Library/Keychains/login.keychain-db`

Key sections:

- `service`: log level, data directory, timezone
- `slack`: app token, bot token, channel controls, DM support
- `ollama`: host, chat model, fallback chat models, embedding model, temperature, timeouts
- `memory`: database path, retrieval limits, consolidation interval
- `persona`: file paths for constitution and seed identity layers
- `web`: optional search provider settings
- `squad0`: Slack-level observation settings
- `autonomy`: ambient observation, self-reflection, and scheduled proactive output such as weeknotes

For the weeknote scheduled task:

- set `autonomy.enabled = true`
- set `autonomy.weeknotes_enabled = true`
- set `autonomy.weeknotes_channel` to the Slack channel ID for `#weeknotes`
- keep `autonomy.weeknote_post_time = "10:00"` for a Friday 10am local post

For the daily self-reflection loop:

- set `autonomy.enabled = true`
- set `autonomy.reflection_enabled = true`
- optionally set `autonomy.reflection_channel` to a Slack channel ID to post reflections
- adjust `autonomy.reflection_interval` (default `24h`)

See [`config/rook.example.toml`](../config/rook.example.toml) for the full example.
