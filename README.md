# rook

A local always-on Slack agent for a Mac mini. Go service, Ollama inference, SQLite memory.

## Prerequisites

- macOS on Apple Silicon
- [Go 1.22+](https://go.dev/dl/)
- [Task](https://taskfile.dev/)
- [Ollama](https://ollama.com/download)

## Quick start

```bash
# Install Ollama and pull models.
brew install ollama
ollama serve &
ollama pull gemma4:e4b
ollama pull nomic-embed-text

# Set up rook.
cd rook
./scripts/install.sh
$EDITOR config/rook.toml        # set timezone, Slack channel IDs

# Store Slack tokens in macOS Keychain.
task slack-keychain-store

# Run.
task run
```

Optionally install as a launchd service with `task launchd-install`.

## Defaults

| Setting | Value |
|---|---|
| Chat model | `gemma4:e4b` |
| Fallback | `qwen3:4b` |
| Embeddings | `nomic-embed-text` |
| Web search | disabled |

On an 8 GB Mac mini `gemma4:e4b` will use swap. If that causes sluggishness, switch to `gemma4:e2b` or `qwen3:4b` in the config.

## Docs

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Memory](docs/memory.md)
- [Operations](docs/operations.md)
