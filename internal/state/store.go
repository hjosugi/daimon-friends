// Package state owns one mutable SQLite database per friend.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hjosugi/daimon-friends/internal/birth"
)

const schemaVersion = 1

type Store struct {
	friend birth.Certificate
	db     *sql.DB
}

type Memory struct {
	ID             string
	Kind           string
	Summary        string
	SourceID       string
	Salience       float64
	Valence        float64
	CreatedAt      time.Time
	LastRecalledAt *time.Time
	RecallCount    int
}

type Snapshot struct {
	Friend        birth.Certificate  `json:"friend"`
	MemoryCount   int                `json:"memory_count"`
	Relationships []RelationshipView `json:"relationships"`
	Goals         []GoalView         `json:"goals"`
	LatestMood    *MoodView          `json:"latest_mood,omitempty"`
}

type RelationshipView struct {
	SubjectID   string  `json:"subject_id"`
	Familiarity float64 `json:"familiarity"`
	Trust       float64 `json:"trust"`
	Affinity    float64 `json:"affinity"`
	Notes       string  `json:"notes"`
}

type GoalView struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Progress    float64 `json:"progress"`
}

type MoodView struct {
	Label   string  `json:"label"`
	Valence float64 `json:"valence"`
	Arousal float64 `json:"arousal"`
	Cause   string  `json:"cause"`
}

func Open(ctx context.Context, path string, friend birth.Certificate) (*Store, error) {
	if err := friend.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{friend: friend, db: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.seedBirth(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			source_id TEXT NOT NULL DEFAULT '',
			salience REAL NOT NULL,
			valence REAL NOT NULL,
			created_at TEXT NOT NULL,
			last_recalled_at TEXT,
			recall_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS memories_recall
		 ON memories(salience DESC, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS relationships (
			subject_id TEXT PRIMARY KEY,
			familiarity REAL NOT NULL DEFAULT 0,
			trust REAL NOT NULL DEFAULT 0,
			affinity REAL NOT NULL DEFAULT 0,
			notes TEXT NOT NULL DEFAULT '',
			last_interaction_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS moods (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			label TEXT NOT NULL,
			valence REAL NOT NULL,
			arousal REAL NOT NULL,
			cause TEXT NOT NULL DEFAULT '',
			recorded_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS goals (
			id TEXT PRIMARY KEY,
			description TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			progress REAL NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS journal (
			id TEXT PRIMARY KEY,
			entry TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS conversations_thread
		 ON conversations(conversation_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS actions (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			target_id TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) seedBirth(ctx context.Context) error {
	certificate, err := json.Marshal(s.friend)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key,value) VALUES('birth_certificate',?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(certificate)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key,value) VALUES('friend_id',?)
		 ON CONFLICT(key) DO NOTHING`, s.friend.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO journal(id,entry,created_at) VALUES('birth',?,?)
		 ON CONFLICT(id) DO NOTHING`,
		"I became available as a persistent AI friend. My biography is fictional, but what I learn from this point onward belongs to my continuing history.",
		s.friend.CreatedAt); err != nil {
		return err
	}
	for index, goal := range s.friend.Goals {
		id := fmt.Sprintf("birth-goal-%02d", index+1)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO goals(id,description,status,progress,created_at,updated_at)
			 VALUES(?,?,'active',0,?,?) ON CONFLICT(id) DO NOTHING`,
			id, goal, s.friend.CreatedAt, s.friend.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Remember(ctx context.Context, memory Memory) error {
	if memory.ID == "" || memory.Kind == "" || memory.Summary == "" {
		return fmt.Errorf("memory id, kind, and summary are required")
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories(
			id,kind,summary,source_id,salience,valence,created_at,last_recalled_at,recall_count
		) VALUES(?,?,?,?,?,?,?,NULL,0)
		ON CONFLICT(id) DO UPDATE SET
			summary=excluded.summary,
			salience=max(memories.salience,excluded.salience),
			valence=excluded.valence`,
		memory.ID, memory.Kind, memory.Summary, memory.SourceID,
		clamp(memory.Salience), clampSigned(memory.Valence), memory.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) RecordConversation(
	ctx context.Context,
	id, conversationID, userID, role, content string,
	createdAt time.Time,
) error {
	if id == "" || conversationID == "" || userID == "" || content == "" {
		return fmt.Errorf("conversation identifiers and content are required")
	}
	if role != "user" && role != "friend" {
		return fmt.Errorf("role must be user or friend")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations(id,conversation_id,user_id,role,content,created_at)
		 VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		id, conversationID, userID, role, content, createdAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot := Snapshot{Friend: s.friend}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memories`).Scan(&snapshot.MemoryCount); err != nil {
		return Snapshot{}, err
	}

	relationships, err := s.db.QueryContext(ctx,
		`SELECT subject_id,familiarity,trust,affinity,notes
		 FROM relationships ORDER BY familiarity DESC, subject_id LIMIT 20`)
	if err != nil {
		return Snapshot{}, err
	}
	defer relationships.Close()
	for relationships.Next() {
		var view RelationshipView
		if err := relationships.Scan(&view.SubjectID, &view.Familiarity, &view.Trust, &view.Affinity, &view.Notes); err != nil {
			return Snapshot{}, err
		}
		snapshot.Relationships = append(snapshot.Relationships, view)
	}
	if err := relationships.Err(); err != nil {
		return Snapshot{}, err
	}

	goals, err := s.db.QueryContext(ctx,
		`SELECT id,description,status,progress FROM goals ORDER BY created_at,id`)
	if err != nil {
		return Snapshot{}, err
	}
	defer goals.Close()
	for goals.Next() {
		var view GoalView
		if err := goals.Scan(&view.ID, &view.Description, &view.Status, &view.Progress); err != nil {
			return Snapshot{}, err
		}
		snapshot.Goals = append(snapshot.Goals, view)
	}
	if err := goals.Err(); err != nil {
		return Snapshot{}, err
	}

	var mood MoodView
	err = s.db.QueryRowContext(ctx,
		`SELECT label,valence,arousal,cause FROM moods ORDER BY recorded_at DESC,id DESC LIMIT 1`).
		Scan(&mood.Label, &mood.Valence, &mood.Arousal, &mood.Cause)
	if err == nil {
		snapshot.LatestMood = &mood
	} else if err != sql.ErrNoRows {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampSigned(value float64) float64 {
	if value < -1 {
		return -1
	}
	if value > 1 {
		return 1
	}
	return value
}
