# rook

`rook` is a local always-on Slack agent for a Mac mini. It runs as a separate Go service, uses a local Ollama model for inference, stores memory in SQLite, and stays operationally independent from `squad0`.

## Quick start

```bash
cd rook
./scripts/install.sh

# Fill in Slack and Ollama settings.
cp config/rook.example.toml config/rook.toml

task build
./bin/rook serve -config config/rook.toml
```

## Design

- Persistent Slack presence through Socket Mode
- Local-first inference through Ollama over `localhost`
- SQLite memory with embeddings, consolidation, decay, and persona layers
- Explicit tool boundary for web retrieval
- Slack-level observation boundary for `squad0`
- launchd-friendly single binary deployment

## Docs

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Memory](docs/memory.md)
- [Operations](docs/operations.md)

## Status

This repo is intended to run locally on macOS Apple Silicon. The default chat model is conservative for an M1 Mac mini with 8 GB RAM, and can be changed in the config.
