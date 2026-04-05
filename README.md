# rook

`rook` is a local always-on Slack agent for a Mac mini. It runs as a separate Go service, uses a local Ollama model for inference, stores memory in SQLite, and stays operationally independent from `squad0`.

## Prerequisites

- macOS on Apple Silicon
- [Go 1.22+](https://go.dev/dl/)
- [Task](https://taskfile.dev/) (task runner)
- [Ollama](https://ollama.com/download) (local model server)

## Quick start

### 1. Install Ollama and pull models

```bash
# Install or update Ollama (gemma4 requires a recent version).
brew install ollama

# Start the Ollama server (runs in the background).
ollama serve &

# Pull the default chat and embedding models.
ollama pull gemma4:e4b
ollama pull nomic-embed-text
```

### 2. Set up rook

```bash
cd rook
./scripts/install.sh

# Edit config/rook.toml with your timezone and any other preferences.
# Slack tokens and channel IDs are the main things to configure.
$EDITOR config/rook.toml
```

### 3. Configure Slack tokens

```bash
# Store Slack bot and app tokens in macOS Keychain.
task slack-keychain-store

# If Keychain is locked:
security unlock-keychain ~/Library/Keychains/login.keychain-db
```

### 4. Run

```bash
task run
```

This builds the binary and starts rook via `scripts/run-rook.sh`, which loads Slack tokens from macOS Keychain automatically.

### 5. (Optional) Install as a launchd service

```bash
task launchd-install
```

This starts rook automatically on login and restarts it on failure.

## Design

- Persistent Slack presence through Socket Mode
- Local-first inference through Ollama over `localhost`
- SQLite memory with embeddings, consolidation, decay, and persona layers
- Ambient observation of other Slack agents without depending on their internals
- Autonomous self-reflection loop and scheduled weeknote summaries
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

- Chat model: `gemma4:e4b` (Google Gemma 4, 4.5B effective parameters)
- Chat fallback model: `qwen3:4b`
- Embedding model: `nomic-embed-text`
- Web provider: disabled by default, optional `duckduckgo`

`gemma4:e4b` runs comfortably on an 8 GB Mac mini and offers native structured output, function calling, and strong conversational quality for its size. `qwen3:4b` is kept as a fallback.

## Docs

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Memory](docs/memory.md)
- [Operations](docs/operations.md)

## Status

This repo is intended to run locally on macOS Apple Silicon. The default chat model fits an M-series Mac mini with 8 GB RAM and can be changed in the config.
