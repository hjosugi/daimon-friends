package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hjosugi/daimon-friends/internal/birth"
	"github.com/hjosugi/daimon-friends/internal/state"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "birth":
		birthCommand(os.Args[2:])
	case "validate":
		validateCommand(os.Args[2:])
	case "init":
		initCommand(os.Args[2:])
	case "inspect":
		inspectCommand(os.Args[2:])
	case "learn":
		learnCommand(os.Args[2:])
	case "recall":
		recallCommand(os.Args[2:])
	case "consolidate":
		consolidateCommand(os.Args[2:])
	default:
		usage()
	}
}

func birthCommand(args []string) {
	flags := flag.NewFlagSet("birth", flag.ExitOnError)
	count := flags.Int("count", 100, "number of friends to create")
	out := flags.String("out", "births", "birth certificate directory")
	flags.Parse(args)
	friends, err := birth.Generate(*count)
	if err != nil {
		log.Fatal(err)
	}
	if err := birth.WriteDirectory(*out, friends); err != nil {
		log.Fatal(err)
	}
	log.Printf("born: %d friends in %s", len(friends), *out)
}

func validateCommand(args []string) {
	flags := flag.NewFlagSet("validate", flag.ExitOnError)
	dir := flags.String("births", "births", "birth certificate directory")
	flags.Parse(args)
	friends, err := birth.LoadDirectory(*dir)
	if err != nil {
		log.Fatal(err)
	}
	ids := map[string]bool{}
	handles := map[string]bool{}
	for _, friend := range friends {
		if ids[friend.ID] || handles[friend.Handle] {
			log.Fatalf("duplicate friend: %s / %s", friend.ID, friend.Handle)
		}
		ids[friend.ID] = true
		handles[friend.Handle] = true
	}
	log.Printf("valid: %d unique birth certificates", len(friends))
}

func initCommand(args []string) {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	birthDir := flags.String("births", "births", "birth certificate directory")
	stateDir := flags.String("state", "state", "SQLite state directory")
	flags.Parse(args)
	friends, err := birth.LoadDirectory(*birthDir)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	for _, friend := range friends {
		store, err := state.Open(ctx, filepath.Join(*stateDir, friend.ID+".sqlite"), friend)
		if err != nil {
			log.Fatalf("%s: %v", friend.ID, err)
		}
		if err := store.Close(); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("initialized: %d isolated SQLite databases in %s", len(friends), *stateDir)
}

func inspectCommand(args []string) {
	flags := flag.NewFlagSet("inspect", flag.ExitOnError)
	birthDir := flags.String("births", "births", "birth certificate directory")
	stateDir := flags.String("state", "state", "SQLite state directory")
	id := flags.String("id", "", "friend id")
	flags.Parse(args)
	if *id == "" {
		log.Fatal("--id is required")
	}
	store, err := openFriendStore(context.Background(), *birthDir, *stateDir, *id)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
}

func learnCommand(args []string) {
	flags := flag.NewFlagSet("learn", flag.ExitOnError)
	birthDir := flags.String("births", "births", "birth certificate directory")
	stateDir := flags.String("state", "state", "SQLite state directory")
	id := flags.String("id", "", "friend id")
	kind := flags.String("kind", state.MemorySemantic, "memory kind")
	summary := flags.String("summary", "", "compact learned claim")
	sourceID := flags.String("source-id", "", "stable source id")
	sourceTitle := flags.String("source-title", "", "human-readable source title")
	sourceURL := flags.String("source-url", "", "source URL")
	sourceAuthor := flags.String("source-author", "", "source author")
	sourceTrust := flags.Float64("source-trust", 0.7, "source trust from 0 to 1")
	confidence := flags.Float64("confidence", 0.7, "claim confidence from 0 to 1")
	salience := flags.Float64("salience", 0.5, "importance from 0 to 1")
	flags.Parse(args)
	if *id == "" || *summary == "" || *sourceID == "" || *sourceTitle == "" {
		log.Fatal("--id, --summary, --source-id, and --source-title are required")
	}
	ctx := context.Background()
	store, err := openFriendStore(ctx, *birthDir, *stateDir, *id)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterKnowledgeSource(ctx, state.KnowledgeSource{
		ID:          *sourceID,
		URI:         *sourceURL,
		Title:       *sourceTitle,
		Author:      *sourceAuthor,
		RetrievedAt: time.Now().UTC(),
		Trust:       *sourceTrust,
	}); err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256([]byte(*id + "\x00" + *sourceID + "\x00" + *summary))
	memoryID := fmt.Sprintf("learned-%x", sum[:10])
	if err := store.Learn(ctx, state.Memory{
		ID:         memoryID,
		Kind:       *kind,
		Summary:    *summary,
		SourceID:   *sourceID,
		Provenance: *sourceTitle,
		Confidence: *confidence,
		Salience:   *salience,
		Novelty:    0.5,
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		log.Fatal(err)
	}
	log.Printf("learned: %s -> %s", *id, memoryID)
}

func recallCommand(args []string) {
	flags := flag.NewFlagSet("recall", flag.ExitOnError)
	birthDir := flags.String("births", "births", "birth certificate directory")
	stateDir := flags.String("state", "state", "SQLite state directory")
	id := flags.String("id", "", "friend id")
	query := flags.String("query", "", "recall query")
	limit := flags.Int("limit", 8, "maximum memories")
	flags.Parse(args)
	if *id == "" || *query == "" {
		log.Fatal("--id and --query are required")
	}
	ctx := context.Background()
	store, err := openFriendStore(ctx, *birthDir, *stateDir, *id)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	results, err := store.Recall(ctx, *query, *limit)
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
}

func consolidateCommand(args []string) {
	flags := flag.NewFlagSet("consolidate", flag.ExitOnError)
	birthDir := flags.String("births", "births", "birth certificate directory")
	stateDir := flags.String("state", "state", "SQLite state directory")
	id := flags.String("id", "", "friend id; omit to consolidate every friend")
	flags.Parse(args)
	friends, err := birth.LoadDirectory(*birthDir)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	count := 0
	for _, friend := range friends {
		if *id != "" && friend.ID != *id {
			continue
		}
		store, err := state.Open(ctx, filepath.Join(*stateDir, friend.ID+".sqlite"), friend)
		if err != nil {
			log.Fatalf("%s: %v", friend.ID, err)
		}
		report, consolidateErr := store.Consolidate(ctx, time.Now().UTC(), state.DefaultRetentionPolicy())
		closeErr := store.Close()
		if consolidateErr != nil {
			log.Fatalf("%s: %v", friend.ID, consolidateErr)
		}
		if closeErr != nil {
			log.Fatal(closeErr)
		}
		log.Printf(
			"%s: active=%d dormant=%d expired=%d edges_pruned=%d",
			friend.ID,
			report.ActiveNodes,
			report.DormantTotal,
			report.ExpiredNodes,
			report.PrunedEdges,
		)
		count++
	}
	if count == 0 {
		log.Fatalf("friend not found: %s", *id)
	}
	log.Printf("consolidated: %d friends", count)
}

func openFriendStore(
	ctx context.Context,
	birthDir, stateDir, id string,
) (*state.Store, error) {
	friends, err := birth.LoadDirectory(birthDir)
	if err != nil {
		return nil, err
	}
	for _, friend := range friends {
		if friend.ID == id {
			return state.Open(ctx, filepath.Join(stateDir, friend.ID+".sqlite"), friend)
		}
	}
	return nil, fmt.Errorf("friend not found: %s", id)
}

func usage() {
	fmt.Fprintln(
		os.Stderr,
		"usage: friends <birth|validate|init|inspect|learn|recall|consolidate> [flags]",
	)
	os.Exit(2)
}
