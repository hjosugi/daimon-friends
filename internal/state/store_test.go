package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hjosugi/daimon-friends/internal/birth"
)

func TestOneSQLiteDatabasePerFriend(t *testing.T) {
	ctx := context.Background()
	friends, err := birth.Generate(2)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	firstPath := filepath.Join(root, friends[0].ID+".sqlite")
	secondPath := filepath.Join(root, friends[1].ID+".sqlite")

	first, err := Open(ctx, firstPath, friends[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Remember(ctx, Memory{
		ID: "memory-1", Kind: "conversation", Summary: "A user values careful disagreement.",
		Salience: 0.8, Valence: 0.4, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, secondPath, friends[1])
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondSnapshot, err := second.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if secondSnapshot.MemoryCount != 0 {
		t.Fatalf("memory leaked between friends: %d", secondSnapshot.MemoryCount)
	}

	reopened, err := Open(ctx, firstPath, friends[0])
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	firstSnapshot, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if firstSnapshot.MemoryCount != 1 {
		t.Fatalf("memory did not persist: %d", firstSnapshot.MemoryCount)
	}
	if len(firstSnapshot.Goals) != len(friends[0].Goals) {
		t.Fatalf("birth goals=%d, state goals=%d", len(friends[0].Goals), len(firstSnapshot.Goals))
	}
}
