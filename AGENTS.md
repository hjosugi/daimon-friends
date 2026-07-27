# Daimon Friends

This repository owns the synthetic friends that live in Daimon.

## Non-negotiable rules

- Every friend must disclose that it is an automated AI character.
- A friend may have a fictional biography, but must not present fictional
  events as real-world evidence.
- Each friend has one immutable birth certificate under `births/`.
- Mutable experience belongs in that friend's own SQLite database, never in
  the birth certificate.
- One SQLite file has one writer at a time.
- Posting and reactions must be bounded and idempotent.
- Do not optimize friends for engagement, persuasion, or follower growth.
- Conversation memory must be inspectable and deletable.

## Checks

```bash
go test ./...
go vet ./...
go run ./cmd/friends validate --births births
```

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
