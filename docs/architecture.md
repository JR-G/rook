# Architecture

`rook` uses a small layered design:

1. `config` loads TOML and environment overrides.
2. `slack` runs Socket Mode transport and converts events to internal messages.
3. `agent` orchestrates commands, memory retrieval, optional web search, persona context, and Ollama inference.
4. `memory` persists episodes, durable memory, reminders, and persona revisions in SQLite.
5. `persona` renders the three identity layers:
   - fixed core constitution
   - stable identity
   - evolving voice
6. `output` sanitises model output before it reaches Slack.
7. `integrations/squad0` observes Slack messages only and never links against `squad0` internals.

The runtime keeps internal events, logs, search results, and memory writes separate from the final Slack-visible response.

## Model Posture

`rook` defaults to `phi4-mini` for local reliability on small Apple Silicon machines. The design assumes the model is capable but capacity-constrained: it should reason locally, but it should not be trusted as the sole factual store. For that reason:

- durable personal context lives in SQLite memory
- relevant memory is retrieved into the prompt
- fresh or high-volatility questions can use the optional web tool layer
- raw retrieved material is never sent directly to Slack
