# Configuration

Configuration is read from TOML and then overlaid with selected `ROOK_*` environment variables.

Key sections:

- `service`: log level, data directory, timezone
- `slack`: app token, bot token, channel controls, DM support
- `ollama`: host, chat model, fallback chat models, embedding model, temperature, timeouts
- `memory`: database path, retrieval limits, consolidation interval
- `persona`: file paths for constitution and seed identity layers
- `web`: optional search provider settings
- `squad0`: Slack-level observation settings

See [`config/rook.example.toml`](../config/rook.example.toml) for the full example.
