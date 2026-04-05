# Memory

`rook` stores memory locally in SQLite and never depends on external storage.

## Durable memory classes

- facts
- preferences
- people
- projects
- decisions
- commitments
- relationship notes
- communication style notes
- operating patterns

## Episodes

Episodes are lightweight records of conversational turns and observed activity. Sources include user messages, assistant replies, ambient agent observations, squad0 activity, weeknotes, and self-reflections. Up to 4 recent thread episodes are passed as chat history on each reply.

## Reminders

Time-based reminders stored in SQLite and delivered by a polling loop.

## Retrieval

Context retrieval for each reply combines:

- semantic similarity via local embeddings
- keyword overlap
- recency weighting

Thread episodes are retrieved separately and passed as native chat turns. Durable memory and historical episodes are deduplicated against the current thread to avoid repetition.

## Consolidation

Every 6 hours (configurable), the persona manager consolidates high-confidence durable memories into the stable identity and evolving voice layers. The autonomy loop also triggers consolidation in the background so rook evolves even when idle.

## Decay

Memories older than 120 days with importance below 0.35 are automatically archived.

Only durable conclusions are written to long-lived memory. Raw web pages and provider payloads are not stored.
