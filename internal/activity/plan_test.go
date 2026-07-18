package activity

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hjosugi/daimon-friends/internal/birth"
)

func TestDailyPlanUsesRichStableFriendIdentity(t *testing.T) {
	friends, err := birth.Generate(100)
	if err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("JST", 9*60*60)
	day := time.Date(2026, 7, 19, 12, 0, 0, 0, location)
	first, err := DailyPlan(day, friends, 4)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DailyPlan(day, friends, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same date must produce the same idempotent plan")
	}
	if len(first) != 4 {
		t.Fatalf("actions=%d", len(first))
	}
	selected := map[string]bool{}
	for _, action := range first {
		if selected[action.Friend.ID] {
			t.Fatalf("friend selected twice: %s", action.Friend.ID)
		}
		selected[action.Friend.ID] = true
		if !strings.HasPrefix(action.Post.Username, "bot_") {
			t.Fatalf("account is not transparent: %s", action.Post.Username)
		}
		if len(action.Post.POVs) != 2 || len(action.Post.Text) < 40 {
			t.Fatalf("thin post: %+v", action.Post)
		}
	}
}

func TestAccountAndReactionDiscloseAndStayEnglish(t *testing.T) {
	friends, err := birth.Generate(1)
	if err != nil {
		t.Fatal(err)
	}
	account := AccountFor(friends[0])
	if !strings.Contains(account.Bio, "AI friend") {
		t.Fatalf("bio does not disclose automation: %q", account.Bio)
	}
	reaction := ComposeReaction(friends[0], Candidate{
		ID:   "post-1",
		POVs: []string{"constructive disagreement"},
	})
	if !strings.Contains(reaction, "constructive disagreement") {
		t.Fatalf("reaction ignores shared POV: %q", reaction)
	}
	for _, character := range reaction {
		if character >= '\u3040' && character <= '\u30ff' {
			t.Fatalf("reaction contains Japanese kana: %q", reaction)
		}
	}
}
