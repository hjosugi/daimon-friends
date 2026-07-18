package state

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hjosugi/daimon-friends/internal/birth"
)

func TestLearnRecallAndQuantizedStorage(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Learn(ctx, Memory{
		ID:         "paper-repair",
		Kind:       MemorySkill,
		Summary:    "Reversible paper repair protects original fibers and records every intervention.",
		SourceID:   "source-conservation",
		Provenance: "conservation handbook",
		Confidence: 0.9,
		Salience:   0.8,
		Novelty:    0.7,
		CreatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Learn(ctx, Memory{
		ID:         "garden-water",
		Kind:       MemorySemantic,
		Summary:    "Morning soil moisture predicts whether a rooftop garden needs water.",
		SourceID:   "source-garden",
		Provenance: "personal field notes",
		Confidence: 0.7,
		Salience:   0.6,
		Novelty:    0.5,
		CreatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}

	var storedBytes, dimensions int
	if err := store.db.QueryRowContext(ctx,
		`SELECT length(embedding),embedding_dims
		 FROM memory_nodes WHERE id='paper-repair'`).
		Scan(&storedBytes, &dimensions); err != nil {
		t.Fatal(err)
	}
	if dimensions != DefaultEmbeddingDimensions || storedBytes != dimensions {
		t.Fatalf("quantized bytes=%d dimensions=%d", storedBytes, dimensions)
	}

	recalled, err := store.Recall(ctx, "How should I do reversible paper repair?", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 2 || recalled[0].Memory.ID != "paper-repair" {
		t.Fatalf("unexpected recall order: %+v", recalled)
	}
	if recalled[0].Memory.RecallCount != 0 {
		t.Fatalf("returned memory should describe pre-recall state: %+v", recalled[0])
	}
	var recallCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT recall_count FROM memory_nodes WHERE id='paper-repair'`).
		Scan(&recallCount); err != nil {
		t.Fatal(err)
	}
	if recallCount != 1 {
		t.Fatalf("recall count=%d", recallCount)
	}
}

func TestConsolidationBoundsActiveGraphWithoutDeletingHistory(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for index := 0; index < 20; index++ {
		id := fmt.Sprintf("memory-%02d", index)
		if err := store.Learn(ctx, Memory{
			ID:         id,
			Kind:       MemorySemantic,
			Summary:    fmt.Sprintf("A small learned concept number %d", index),
			Provenance: "test observation",
			Confidence: 0.4 + float64(index%3)*0.1,
			Salience:   0.2 + float64(index)*0.02,
			Novelty:    0.5,
			CreatedAt:  now.Add(-time.Duration(index) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 1; index < 20; index++ {
		if err := store.Connect(ctx, Edge{
			FromID:   "memory-00",
			ToID:     fmt.Sprintf("memory-%02d", index),
			Relation: "similar",
			Weight:   float64(index) / 20,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	report, err := store.Consolidate(ctx, now, RetentionPolicy{
		MaxActiveNodes:  5,
		MaxEdgesPerNode: 3,
		EpisodeTTL:      30 * 24 * time.Hour,
		DormantAfter:    365 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ActiveNodes != 5 || report.DormantTotal != 15 {
		t.Fatalf("unexpected report: %+v", report)
	}
	var total int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_nodes`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 20 {
		t.Fatalf("history was deleted: %d", total)
	}
	var maximumEdges int
	if err := store.db.QueryRowContext(ctx,
		`SELECT coalesce(max(edge_count),0) FROM (
			SELECT count(*) AS edge_count FROM memory_edges GROUP BY from_id
		 )`).Scan(&maximumEdges); err != nil {
		t.Fatal(err)
	}
	if maximumEdges > 3 {
		t.Fatalf("graph remained dense: %d edges from one node", maximumEdges)
	}
}

func TestCompactConversationRequiresAndKeepsInspectableSummary(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	for index := 0; index < 3; index++ {
		at := start.Add(time.Duration(index) * time.Minute)
		if err := store.RecordConversation(
			ctx,
			fmt.Sprintf("message-%d", index),
			"thread-1",
			"user-1",
			"user",
			fmt.Sprintf("message %d", index),
			at,
		); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.CompactConversation(
		ctx,
		"thread-1",
		"user-1",
		"The user is comparing two careful approaches and values reversibility.",
		start.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d", removed)
	}
	var messages, summaries int
	if err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM conversations WHERE conversation_id='thread-1'`).
		Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM memory_nodes
		 WHERE source_id='conversation:thread-1'`).
		Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || summaries != 1 {
		t.Fatalf("messages=%d summaries=%d", messages, summaries)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	friends, err := birth.Generate(1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(
		context.Background(),
		filepath.Join(t.TempDir(), "friend.sqlite"),
		friends[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}
