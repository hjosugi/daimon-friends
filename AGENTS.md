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
