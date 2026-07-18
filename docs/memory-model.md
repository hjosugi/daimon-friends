# Memory model

Daimon friends use a bounded associative memory. It borrows useful ideas from
human memory and neural association without claiming to simulate a brain.

## Layers

1. **Working context** is the small recent conversation window supplied to the
   language model. It is not copied into long-term memory automatically.
2. **Episodes** are events with salience, emotion, provenance, and an expiry
   time. Only important or emotionally strong episodes receive a vector.
3. **Semantic memory** stores compact learned concepts and claims.
4. **Person and relationship memory** stores what a friend has learned about a
   conversation partner, separately from global factual knowledge.
5. **Self and skill memory** stores changes in the friend's current beliefs,
   goals, and deliberate practice. The immutable birth certificate remains the
   origin, not mutable memory.

## Sparse graph

Memories are nodes. Directed edges describe `supports`, `contradicts`,
`caused_by`, `about`, `similar`, `generalized_from`, `revised_by`, or
`co_recalled` relationships. Recalling several nodes together slightly
reinforces a `co_recalled` edge.

The graph stays sparse:

- at most 512 active searchable nodes per friend by default;
- at most 12 outgoing edges per node;
- weak edges decay and are pruned;
- low-value old nodes become dormant instead of being deleted;
- dormant nodes retain their text and provenance but release vector storage.

This gives each friend continuity and inspectability without an all-to-all
graph or an ever-growing vector index.

## Compact vectors

Only selected long-term memories are embedded. Embeddings are normalized and
stored as signed 8-bit values plus one scale value. A 256-dimensional memory
therefore uses 256 bytes for its vector instead of 1,024 bytes as float32.

The included feature-hash embedding is an offline fallback. A production
semantic embedding provider can replace it without changing SQLite storage.
Recall combines vector similarity with salience, confidence, emotional
intensity, novelty, and recency.

## Learning new knowledge

Factual learning must include provenance and confidence. Source metadata holds
its URI, author, retrieval time, trust, and content hash. New claims connect to
existing knowledge rather than silently overwriting it:

- agreement adds a `supports` edge;
- a conflict adds a `contradicts` edge;
- a later correction adds a `revised_by` edge;
- uncertainty remains explicit in confidence.

A future ingestion worker should retrieve a small number of trusted sources,
extract claims, compare them with recalled memory, and commit only the compact
claims and source metadata. It should not store entire web pages.

## Consolidation and privacy

Consolidation expires ordinary episodes, makes weak unused memories dormant,
and prunes excess edges. It never deletes inspectable memory nodes.

Conversation messages are compacted only after a non-empty summary has been
stored. Only the explicitly summarized prefix is removed. User-facing deletion
must remove both raw messages and derived summaries for that conversation.
