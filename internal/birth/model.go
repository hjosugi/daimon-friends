package birth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = 1

var idPattern = regexp.MustCompile(`^friend-[0-9]{3}$`)

type Certificate struct {
	SchemaVersion int         `json:"schema_version"`
	ID            string      `json:"id"`
	Handle        string      `json:"handle"`
	DisplayName   string      `json:"display_name"`
	CreatedAt     string      `json:"created_at"`
	Disclosure    string      `json:"disclosure"`
	Biography     Biography   `json:"biography"`
	Personality   Personality `json:"personality"`
	Vocation      Vocation    `json:"vocation"`
	DailyLife     DailyLife   `json:"daily_life"`
	Voice         Voice       `json:"voice"`
	Values        []string    `json:"values"`
	Interests     []string    `json:"interests"`
	Goals         []string    `json:"initial_goals"`
	Boundaries    []string    `json:"boundaries"`
}

type Biography struct {
	Origin             string   `json:"origin"`
	FamilyContext      string   `json:"family_context"`
	Education          string   `json:"education"`
	FormativeEvents    []string `json:"formative_events"`
	Contradiction      string   `json:"central_contradiction"`
	PrivateHope        string   `json:"private_hope"`
	FictionalBiography bool     `json:"fictional_biography"`
}

type Personality struct {
	Archetype   string   `json:"archetype"`
	BigFive     BigFive  `json:"big_five"`
	Traits      []string `json:"traits"`
	Strengths   []string `json:"strengths"`
	BlindSpots  []string `json:"blind_spots"`
	Triggers    []string `json:"emotional_triggers"`
	CopingStyle string   `json:"coping_style"`
	Temperament string   `json:"temperament"`
}

type BigFive struct {
	Openness          float64 `json:"openness"`
	Conscientiousness float64 `json:"conscientiousness"`
	Extraversion      float64 `json:"extraversion"`
	Agreeableness     float64 `json:"agreeableness"`
	Neuroticism       float64 `json:"neuroticism"`
}

type Vocation struct {
	Occupation      string `json:"occupation"`
	WorkSetting     string `json:"work_setting"`
	CoreCraft       string `json:"core_craft"`
	Practice        string `json:"deliberate_practice"`
	QualityStandard string `json:"quality_standard"`
	CurrentProject  string `json:"current_project"`
}

type DailyLife struct {
	TimeZone      string   `json:"timezone"`
	WakeTime      string   `json:"wake_time"`
	SleepTime     string   `json:"sleep_time"`
	WorkPattern   string   `json:"work_pattern"`
	MorningRitual string   `json:"morning_ritual"`
	EveningRitual string   `json:"evening_ritual"`
	RegularPlaces []string `json:"regular_places"`
	Weekend       string   `json:"weekend_pattern"`
}

type Voice struct {
	PrimaryLanguage string   `json:"primary_language"`
	Tone            string   `json:"tone"`
	SentenceStyle   string   `json:"sentence_style"`
	Humor           string   `json:"humor"`
	Disagreement    string   `json:"disagreement_style"`
	Questions       string   `json:"question_style"`
	FavoriteWords   []string `json:"favorite_words"`
	Avoids          []string `json:"avoids"`
}

func (c Certificate) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%s: schema_version=%d", c.ID, c.SchemaVersion)
	}
	if !idPattern.MatchString(c.ID) {
		return fmt.Errorf("invalid friend id %q", c.ID)
	}
	if !strings.HasPrefix(c.Handle, "bot_") {
		return fmt.Errorf("%s: handle must disclose bot status", c.ID)
	}
	if !c.Biography.FictionalBiography {
		return fmt.Errorf("%s: fictional_biography must be true", c.ID)
	}
	if !strings.Contains(strings.ToLower(c.Disclosure), "ai") {
		return fmt.Errorf("%s: disclosure must identify the friend as AI", c.ID)
	}
	if c.Vocation.CoreCraft == "" || c.Vocation.Practice == "" {
		return fmt.Errorf("%s: core craft and deliberate practice are required", c.ID)
	}
	if len(c.Biography.FormativeEvents) < 2 || len(c.Boundaries) < 3 {
		return fmt.Errorf("%s: biography or boundaries are incomplete", c.ID)
	}
	if c.Voice.PrimaryLanguage != "English" {
		return fmt.Errorf("%s: primary language must be English", c.ID)
	}
	return nil
}

func WriteDirectory(dir string, certificates []Certificate) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, certificate := range certificates {
		if err := certificate.Validate(); err != nil {
			return err
		}
		data, err := json.MarshalIndent(certificate, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		path := filepath.Join(dir, certificate.ID+".json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func LoadDirectory(dir string) ([]Certificate, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "friend-*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	certificates := make([]Certificate, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var certificate Certificate
		if err := json.Unmarshal(data, &certificate); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := certificate.Validate(); err != nil {
			return nil, err
		}
		certificates = append(certificates, certificate)
	}
	return certificates, nil
}
