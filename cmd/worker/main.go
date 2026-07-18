// Command worker performs one idempotent daily Daimon friend activity slot.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hjosugi/daimon-friends/internal/activity"
	"github.com/hjosugi/daimon-friends/internal/birth"
	"github.com/hjosugi/daimon-friends/internal/daimon"
	"github.com/hjosugi/daimon-friends/internal/remotestate"
	"github.com/hjosugi/daimon-friends/internal/state"
)

type options struct {
	birthsDir     string
	localState    string
	stateBucket   string
	statePrefix   string
	postsPerDay   int
	timeZone      string
	date          string
	slot          int
	dryRun        bool
	provisionOnly bool
}

func main() {
	log.SetFlags(0)
	var options options
	flag.StringVar(&options.birthsDir, "births", env("BIRTHS_DIR", "births"), "birth certificate directory")
	flag.StringVar(&options.localState, "local-state", "", "use this local state directory instead of Cloud Storage")
	flag.StringVar(&options.stateBucket, "state-bucket", os.Getenv("STATE_BUCKET"), "Cloud Storage state bucket")
	flag.StringVar(&options.statePrefix, "state-prefix", env("STATE_PREFIX", "friends"), "Cloud Storage object prefix")
	flag.IntVar(&options.postsPerDay, "posts-per-day", envInt("POSTS_PER_DAY", 4), "daily activity slots")
	flag.StringVar(&options.timeZone, "timezone", env("FRIENDS_TIMEZONE", "Asia/Tokyo"), "calendar timezone")
	flag.StringVar(&options.date, "date", "", "local date YYYY-MM-DD")
	flag.IntVar(&options.slot, "slot", -1, "daily slot; default derives from current time")
	flag.BoolVar(&options.dryRun, "dry-run", false, "print the action without external writes")
	flag.BoolVar(&options.provisionOnly, "provision-only", false, "provision all accounts and stop")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if err := run(ctx, options); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, options options) error {
	friends, err := birth.LoadDirectory(options.birthsDir)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(options.timeZone)
	if err != nil {
		return err
	}
	now := time.Now().In(location)
	day := now
	if options.date != "" {
		day, err = time.ParseInLocation("2006-01-02", options.date, location)
		if err != nil {
			return err
		}
	}
	plan, err := activity.DailyPlan(day, friends, options.postsPerDay)
	if err != nil {
		return err
	}
	slot := options.slot
	if slot < 0 {
		slot = activity.CurrentSlot(now, options.postsPerDay)
	}
	if slot < 0 || slot >= len(plan) {
		return fmt.Errorf("slot must be between 0 and %d", len(plan)-1)
	}
	action := plan[slot]
	if options.dryRun {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"accounts": len(friends),
			"slot":     slot,
			"friend":   action.Friend.ID,
			"post":     action.Post,
		})
	}

	client, err := daimon.New(ctx, daimon.Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		EmbedURL:    os.Getenv("EMBED_URL"),
		QdrantURL:   os.Getenv("QDRANT_URL"),
		QdrantKey:   os.Getenv("QDRANT_API_KEY"),
	})
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Provision(ctx, friends); err != nil {
		return fmt.Errorf("provision friends: %w", err)
	}
	if options.provisionOnly {
		log.Printf("provisioned: %d transparent AI friend accounts", len(friends))
		return nil
	}

	store, commitState, discardState, err := openState(ctx, options, action.Friend)
	if err != nil {
		return err
	}
	defer discardState()
	completed, err := store.HasAction(ctx, "publish:"+action.Post.ID)
	if err != nil {
		_ = store.Close()
		return err
	}
	if completed {
		if err := store.Close(); err != nil {
			return err
		}
		log.Printf(
			"already complete: date=%s slot=%d friend=%s post=%s",
			day.Format("2006-01-02"),
			slot,
			action.Friend.ID,
			action.Post.ID,
		)
		return nil
	}

	created, err := client.Publish(ctx, action.Post)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("publish: %w", err)
	}
	nowUTC := time.Now().UTC()
	ownMemoryID := "own-post:" + action.Post.ID
	if err := store.Learn(ctx, state.Memory{
		ID:         ownMemoryID,
		Kind:       state.MemorySelf,
		Summary:    action.Post.Text,
		SourceID:   "daimon-post:" + action.Post.ID,
		Provenance: "friend's own Daimon post",
		Confidence: 1,
		Salience:   0.65,
		Novelty:    0.5,
		CreatedAt:  nowUTC,
	}); err != nil {
		_ = store.Close()
		return err
	}
	if err := store.RecordAction(
		ctx,
		"publish:"+action.Post.ID,
		"publish",
		action.Post.ID,
		action.Post.Text,
		fmt.Sprintf("created=%t", created),
		nowUTC,
	); err != nil {
		_ = store.Close()
		return err
	}

	reactionTarget := "none"
	candidate, err := client.RecentHumanCandidate(
		ctx,
		action.Post,
		nowUTC.Add(-30*24*time.Hour),
	)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("select reaction: %w", err)
	}
	if candidate != nil {
		reaction := activity.ComposeReaction(action.Friend, *candidate)
		if err := client.React(ctx, action.Post, *candidate, reaction); err != nil {
			_ = store.Close()
			return fmt.Errorf("react: %w", err)
		}
		reactionTarget = candidate.ID
		observedMemoryID := "observed-post:" + candidate.ID
		if err := store.Learn(ctx, state.Memory{
			ID:         observedMemoryID,
			Kind:       state.MemoryPerson,
			Summary:    compactObservation(*candidate),
			SourceID:   "daimon-post:" + candidate.ID,
			Provenance: "public Daimon post by @" + candidate.Username,
			Confidence: 0.7,
			Salience:   0.55,
			Novelty:    0.65,
			CreatedAt:  nowUTC,
		}); err != nil {
			_ = store.Close()
			return err
		}
		if err := store.Connect(ctx, state.Edge{
			FromID:   ownMemoryID,
			ToID:     observedMemoryID,
			Relation: "responded_to",
			Weight:   0.7,
		}, nowUTC); err != nil {
			_ = store.Close()
			return err
		}
		if err := store.ObserveRelationship(
			ctx,
			candidate.UserID,
			0.05,
			0.01,
			0.01,
			"Shared a public Daimon exchange.",
			nowUTC,
		); err != nil {
			_ = store.Close()
			return err
		}
		if err := store.RecordAction(
			ctx,
			"reaction:"+action.Post.UserID+":"+candidate.ID,
			"reaction",
			candidate.ID,
			reaction,
			"commented_and_liked",
			nowUTC,
		); err != nil {
			_ = store.Close()
			return err
		}
	}
	report, err := store.Consolidate(ctx, nowUTC, state.DefaultRetentionPolicy())
	if err != nil {
		_ = store.Close()
		return err
	}
	if err := store.Close(); err != nil {
		return err
	}
	if err := commitState(ctx); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	log.Printf(
		"complete: date=%s slot=%d friend=%s post=%s created=%t reaction=%s memories=%d",
		day.Format("2006-01-02"),
		slot,
		action.Friend.ID,
		action.Post.ID,
		created,
		reactionTarget,
		report.ActiveNodes,
	)
	return nil
}

func openState(
	ctx context.Context,
	options options,
	friend birth.Certificate,
) (*state.Store, func(context.Context) error, func(), error) {
	if options.localState != "" {
		path := filepath.Join(options.localState, friend.ID+".sqlite")
		store, err := state.Open(ctx, path, friend)
		return store, func(context.Context) error { return nil }, func() {}, err
	}
	if options.stateBucket == "" {
		return nil, nil, nil, errors.New("--state-bucket or --local-state is required")
	}
	repository, err := remotestate.New(ctx, options.stateBucket, options.statePrefix)
	if err != nil {
		return nil, nil, nil, err
	}
	checkout, err := repository.Checkout(ctx, friend.ID)
	if err != nil {
		repository.Close()
		return nil, nil, nil, err
	}
	store, err := state.Open(ctx, checkout.Path, friend)
	if err != nil {
		checkout.Discard()
		repository.Close()
		return nil, nil, nil, err
	}
	commit := func(commitContext context.Context) error {
		defer repository.Close()
		return repository.Commit(commitContext, checkout)
	}
	discard := func() {
		checkout.Discard()
		_ = repository.Close()
	}
	return store, commit, discard, nil
}

func compactObservation(candidate activity.Candidate) string {
	text := strings.Join(strings.Fields(candidate.Text), " ")
	const maximum = 240
	if len(text) > maximum {
		text = text[:maximum] + "…"
	}
	if len(candidate.POVs) == 0 {
		return fmt.Sprintf("@%s wrote: %s", candidate.Username, text)
	}
	return fmt.Sprintf(
		"@%s connected %s with this observation: %s",
		candidate.Username,
		strings.Join(candidate.POVs, ", "),
		text,
	)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf("%s must be an integer", key)
	}
	return parsed
}
