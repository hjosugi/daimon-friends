package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	friends, err := birth.LoadDirectory(*birthDir)
	if err != nil {
		log.Fatal(err)
	}
	var selected *birth.Certificate
	for index := range friends {
		if friends[index].ID == *id {
			selected = &friends[index]
			break
		}
	}
	if selected == nil {
		log.Fatalf("friend not found: %s", *id)
	}
	store, err := state.Open(context.Background(), filepath.Join(*stateDir, selected.ID+".sqlite"), *selected)
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

func usage() {
	fmt.Fprintln(os.Stderr, "usage: friends <birth|validate|init|inspect> [flags]")
	os.Exit(2)
}
