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

## Thread Handling

When rook replies in a Slack thread, it retrieves up to 4 prior turns and passes them as proper user/assistant chat messages to the model. This gives the model native conversational context rather than flattened text summaries.

A reply guard runs after every response: it fingerprints the new reply against all prior assistant turns in the thread and triggers a repair call if token overlap exceeds 70%. If repair also repeats, a fallback message is posted instead.

## Model Posture

`rook` defaults to `gemma4:e4b` with `qwen3:4b` as a local fallback for reliability on small Apple Silicon machines. The design assumes the model is capable but capacity-constrained: it should reason locally, but it should not be trusted as the sole factual store. For that reason:

- durable personal context lives in SQLite memory
- relevant memory is retrieved into the prompt
- fresh or high-volatility questions can use the optional web tool layer
- raw retrieved material is never sent directly to Slack

## Autonomy

Autonomy is opt-in and local-first:

- ambient agent messages can be observed and stored as episodes without auto-replying
- scheduled jobs run inside the same long-lived service
- the autonomy loop runs background persona consolidation on each tick, so rook's identity evolves from observation even when idle
- a Friday 10:00 weeknote summary aggregates observed agent activity for a configured Slack channel
- a daily self-reflection loop reviews recent episodes and records observations about conversational patterns and gaps
- proactive behavior is constrained by the same persona, output, and memory boundaries as direct replies
