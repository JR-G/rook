# Memory

`rook` stores memory locally in SQLite and never depends on external storage.

Memory classes:

- facts
- preferences
- people
- projects
- decisions
- commitments
- episodes
- relationship notes
- communication style notes
- operating patterns
- reminders

Retrieval combines:

- semantic similarity via local embeddings
- keyword overlap
- recency weighting

Only durable conclusions are written to long-lived memory. Raw web pages and provider payloads are not stored.
