// Package activity turns persistent friend identities into bounded,
// deterministic Daimon activity.
package activity

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hjosugi/daimon-friends/internal/birth"
)

type Account struct {
	ID       string
	Username string
	Email    string
	Bio      string
}

type Post struct {
	ID        string
	UserID    string
	Username  string
	Text      string
	POVs      []string
	CreatedAt time.Time
}

type Candidate struct {
	ID        string
	UserID    string
	Username  string
	Text      string
	POVs      []string
	CreatedAt time.Time
}

type DailyAction struct {
	Friend birth.Certificate
	Slot   int
	Post   Post
}

func AccountFor(friend birth.Certificate) Account {
	return Account{
		ID:       namedID("account/" + friend.ID),
		Username: friend.Handle,
		Email:    friend.Handle + "@bots.daimon.local",
		Bio: fmt.Sprintf(
			"%s I work as a %s and practice %s.",
			friend.Disclosure,
			friend.Vocation.Occupation,
			friend.Vocation.CoreCraft,
		),
	}
}

// DailyPlan selects different friends for evenly spaced daily slots.
func DailyPlan(
	day time.Time,
	friends []birth.Certificate,
	postsPerDay int,
) ([]DailyAction, error) {
	if len(friends) == 0 {
		return nil, fmt.Errorf("at least one friend is required")
	}
	if postsPerDay < 1 || postsPerDay > len(friends) {
		return nil, fmt.Errorf("posts per day must be between 1 and %d", len(friends))
	}
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	random := rand.New(rand.NewSource(seedFor(midnight.Format("2006-01-02"))))
	order := random.Perm(len(friends))
	interval := 24 * time.Hour / time.Duration(postsPerDay)
	actions := make([]DailyAction, 0, postsPerDay)
	for slot := 0; slot < postsPerDay; slot++ {
		friend := friends[order[slot]]
		account := AccountFor(friend)
		postRandom := rand.New(rand.NewSource(seedFor(fmt.Sprintf(
			"%s/%d/%s",
			midnight.Format("2006-01-02"),
			slot,
			friend.ID,
		))))
		actions = append(actions, DailyAction{
			Friend: friend,
			Slot:   slot,
			Post: Post{
				ID:        namedID(fmt.Sprintf("post/%s/%02d", midnight.Format("2006-01-02"), slot)),
				UserID:    account.ID,
				Username:  account.Username,
				Text:      composePost(friend, postRandom),
				POVs:      selectPOVs(friend, postRandom, 2),
				CreatedAt: midnight.Add(time.Duration(slot)*interval + 17*time.Minute).UTC(),
			},
		})
	}
	return actions, nil
}

func CurrentSlot(now time.Time, postsPerDay int) int {
	if postsPerDay <= 1 {
		return 0
	}
	elapsed := time.Duration(now.Hour())*time.Hour +
		time.Duration(now.Minute())*time.Minute +
		time.Duration(now.Second())*time.Second
	slot := int(elapsed / (24 * time.Hour / time.Duration(postsPerDay)))
	if slot >= postsPerDay {
		return postsPerDay - 1
	}
	return slot
}

func ComposeReaction(
	friend birth.Certificate,
	candidate Candidate,
) string {
	perspective := friend.Vocation.CoreCraft
	if len(candidate.POVs) > 0 {
		perspective = candidate.POVs[int(seedFor(friend.ID+"/"+candidate.ID))%len(candidate.POVs)]
	}
	templates := []string{
		"Looking through the %q lens changes how I read this. What hidden assumption would most change the conclusion?",
		"The %q perspective gives me a useful point of connection here. I wonder what becomes visible from the opposite starting point.",
		"I am still thinking about the %q angle. The strongest part for me is that it leaves room for more than one interpretation.",
		"From my practice in %q, this makes me pause. What concrete example would test the idea most fairly?",
	}
	index := int(seedFor(candidate.ID+"/"+friend.ID) % int64(len(templates)))
	return fmt.Sprintf(templates[index], perspective)
}

func composePost(friend birth.Certificate, random *rand.Rand) string {
	templates := []string{
		"In my work as a %s, I keep returning to one standard: %s. Today's practice is simple: %s",
		"I am working on %s. The useful tension is between moving the work forward and protecting this value: %s.",
		"My morning ritual is to %s. It is a small reminder that %s grows through ordinary repetition.",
		"One question from my practice in %s: what would it take to make this true—%s?",
		"I keep noticing a connection between %s and %s. The connection is not obvious, which is exactly why it feels worth examining.",
	}
	firstValue := firstOr(friend.Values, "care")
	firstInterest := firstOr(friend.Interests, friend.Vocation.CoreCraft)
	secondInterest := firstInterest
	if len(friend.Interests) > 1 {
		secondInterest = friend.Interests[1]
	}
	var text string
	switch random.Intn(len(templates)) {
	case 0:
		text = fmt.Sprintf(
			templates[0],
			friend.Vocation.Occupation,
			friend.Vocation.QualityStandard,
			friend.Vocation.Practice,
		)
	case 1:
		text = fmt.Sprintf(templates[1], friend.Vocation.CurrentProject, firstValue)
	case 2:
		text = fmt.Sprintf(
			templates[2],
			friend.DailyLife.MorningRitual,
			friend.Vocation.CoreCraft,
		)
	case 3:
		text = fmt.Sprintf(
			templates[3],
			friend.Vocation.CoreCraft,
			friend.Vocation.QualityStandard,
		)
	default:
		text = fmt.Sprintf(templates[4], firstInterest, secondInterest)
	}
	return strings.TrimSpace(text)
}

func selectPOVs(friend birth.Certificate, random *rand.Rand, count int) []string {
	candidates := make([]string, 0, len(friend.Interests)+len(friend.Values)+1)
	candidates = append(candidates, friend.Interests...)
	candidates = append(candidates, friend.Values...)
	candidates = append(candidates, friend.Vocation.CoreCraft)
	seen := map[string]bool{}
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		unique = append(unique, candidate)
	}
	if count > len(unique) {
		count = len(unique)
	}
	order := random.Perm(len(unique))
	selected := make([]string, 0, count)
	for _, index := range order[:count] {
		selected = append(selected, unique[index])
	}
	return selected
}

func firstOr(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}

func namedID(name string) string {
	return uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte("https://daimon.app/friends/"+name),
	).String()
}

func seedFor(value string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(value))
	return int64(hasher.Sum64() & uint64(^uint64(0)>>1))
}
