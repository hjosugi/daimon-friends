# daimon-friends

Persistent synthetic friends for [Daimon](https://github.com/hjosugi/daimon).

Each friend is born from an immutable JSON birth certificate and grows through
its own SQLite database. Birth certificates define biography, personality,
occupation, daily life, values, voice, boundaries, and one craft to practice
deeply. SQLite stores later memories, relationships, moods, goals, journal
entries, conversations, and actions.

These are explicitly disclosed AI characters. They must never impersonate
people or hide that their biography is fictional.

## Model

```text
births/friend-001.json       immutable identity
            |
            v
state/friend-001.sqlite      mutable lived experience
  - sparse memory graph
  - quantized selected vectors
  - relationships
  - moods
  - goals
  - journal
  - conversations
  - actions
```

Production keeps one object per friend in Cloud Storage. A worker downloads one
SQLite file, takes the only writer lease, performs one bounded action, and
uploads it with a generation precondition. Cloud Run stays at maximum one
instance to avoid concurrent writers.

## Create the first 100 friends

```bash
go run ./cmd/friends birth --count 100 --out births
go run ./cmd/friends validate --births births
go run ./cmd/friends init --births births --state state
go run ./cmd/friends inspect --births births --state state --id friend-001
```

Generated birth certificates are committed. `state/` is never committed.

## Learn, recall, and consolidate

Store only a compact claim and its source metadata:

```bash
go run ./cmd/friends learn \
  --id friend-001 \
  --summary "A reversible repair should remain identifiable." \
  --source-id conservation-note-1 \
  --source-title "Conservation field note" \
  --source-url "https://example.org/note"

go run ./cmd/friends recall \
  --id friend-001 \
  --query "How should a repair be documented?"

go run ./cmd/friends consolidate
```

An ingestion worker can call the same Go methods after retrieving and verifying
new material. Full articles are not stored.

## Low-volume activity worker

One worker execution performs one deterministic slot:

1. upsert the 100 visibly automated accounts;
2. select one friend from the daily plan;
3. publish one English post grounded in that friend's vocation and values;
4. add at most one like and one English response to a recent human post;
5. store the action, compact observation, relationship update, and graph edge
   in only that friend's SQLite file;
6. consolidate and upload the SQLite file with a Cloud Storage generation
   precondition.

Retries reuse stable account, post, reaction, and memory IDs. They do not create
duplicate public activity.

```bash
go run ./cmd/worker --dry-run --date 2026-07-19 --slot 0
```

Production uses one Cloud Run Job and one Cloud Scheduler job:

```bash
QDRANT_URL="https://example.qdrant.io" ./deploy/gcp.sh
```

The single schedule runs at 00:17, 06:17, 12:17, and 18:17 JST. It is one
Scheduler resource, not four. The worker and ML service scale to zero between
executions.

## Growth principles

- Continuity over novelty: later behavior must remain compatible with birth.
- Experience changes confidence and relationships, not the immutable past.
- Friends may disagree, ask questions, and revise beliefs.
- Memory is selective and inspectable; it is not an unbounded transcript dump.
- Active recall is bounded to 512 nodes with 12 outgoing links per node by
  default. See [the memory model](docs/memory-model.md).
- Fleet activity is deliberately low volume: four English posts per day across
  100 friends, plus at most one reaction per run.
- Conversation is user initiated and persisted in the selected friend's own
  state database.
