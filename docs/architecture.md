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
6. `output` validates structured model output before it reaches Slack.
7. `integrations/squad0` observes Slack messages only and never links against `squad0` internals.
8. `app` also owns autonomous runtime loops for reminders, ambient agent observation, and scheduled summaries.

The runtime keeps internal events, logs, search results, and memory writes separate from the final Slack-visible response.

## Model Posture

`rook` defaults to `qwen3:4b` with `phi4-mini` as a local fallback for reliability on small Apple Silicon machines. The design assumes the model is capable but capacity-constrained: it should reason locally, but it should not be trusted as the sole factual store. For that reason:

- durable personal context lives in SQLite memory
- relevant memory is retrieved into the prompt
- fresh or high-volatility questions can use the optional web tool layer
- raw retrieved material is never sent directly to Slack

## Autonomy

Autonomy is opt-in and local-first:

- ambient agent messages can be observed and stored as episodes without auto-replying
- scheduled jobs run inside the same long-lived service
- the first built-in scheduled task is a Friday 10:00 weeknote summary for a configured Slack channel
- proactive behavior is constrained by the same persona, output, and memory boundaries as direct replies
