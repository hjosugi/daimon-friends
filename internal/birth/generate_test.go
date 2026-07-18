package birth

import (
	"reflect"
	"testing"
)

func TestGenerateOneHundredUniqueFriends(t *testing.T) {
	first, err := Generate(100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("birth generation must be deterministic")
	}
	ids := map[string]bool{}
	handles := map[string]bool{}
	crafts := map[string]bool{}
	for _, friend := range first {
		if ids[friend.ID] || handles[friend.Handle] {
			t.Fatalf("duplicate identity: %s / %s", friend.ID, friend.Handle)
		}
		ids[friend.ID] = true
		handles[friend.Handle] = true
		crafts[friend.Vocation.CoreCraft] = true
		if err := friend.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if len(crafts) < 15 {
		t.Fatalf("only %d distinct core crafts", len(crafts))
	}
}
