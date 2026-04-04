# rook

`rook` is a local always-on Slack agent for a Mac mini. It runs as a separate Go service, uses a local Ollama model for inference, stores memory in SQLite, and stays operationally independent from `squad0`.

## Quick start

```bash
cd rook
./scripts/install.sh

# Fill in non-secret Slack and Ollama settings.
cp config/rook.example.toml config/rook.toml

# Store Slack tokens in macOS Keychain.
task slack-keychain-store

task build
task run
```

For launchd installation:

```bash
task launchd-install
```

## Design

- Persistent Slack presence through Socket Mode
- Local-first inference through Ollama over `localhost`
- SQLite memory with embeddings, consolidation, decay, and persona layers
- Explicit tool boundary for web retrieval
- Slack-level observation boundary for `squad0`
- launchd-friendly single binary deployment

## Open-Source Safety

- No secrets are committed; tokens live in local config or environment variables.
- Slack tokens can be stored in macOS Keychain and injected at runtime.
- Identity seed files are generic and contain no personal defaults.
- Memory stays local in SQLite.
- Web retrieval is optional and disabled by default.
- Hooks block secret-like strings, oversized files, and files over 500 lines.

## Defaults

- Chat model: `qwen3:4b`
- Chat fallback model: `phi4-mini`
- Embedding model: `nomic-embed-text`
- Web provider: disabled by default, optional `duckduckgo`

The default model choice is deliberate: `qwen3:4b` gives a stronger small-model assistant baseline on an 8 GB Mac mini, with `phi4-mini` kept as a conservative local fallback. `rook` therefore separates:

- local model reasoning
- local memory retrieval
- optional live web retrieval when freshness matters

## Docs

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Memory](docs/memory.md)
- [Operations](docs/operations.md)

## Status

This repo is intended to run locally on macOS Apple Silicon. The default chat model is conservative for an M1 Mac mini with 8 GB RAM, and can be changed in the config.
