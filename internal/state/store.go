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

const schemaVersion = 2

type Store struct {
	friend birth.Certificate
	db     *sql.DB
}

type Memory struct {
	ID             string
	Kind           string
	Summary        string
	SourceID       string
	Provenance     string
	Confidence     float64
	Salience       float64
	Valence        float64
	Novelty        float64
	Embedding      []float32
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      *time.Time
	LastRecalledAt *time.Time
	RecallCount    int
}

type Snapshot struct {
	Friend             birth.Certificate  `json:"friend"`
	MemoryCount        int                `json:"memory_count"`
	ActiveMemoryCount  int                `json:"active_memory_count"`
	DormantMemoryCount int                `json:"dormant_memory_count"`
	MemoryEdgeCount    int                `json:"memory_edge_count"`
	Relationships      []RelationshipView `json:"relationships"`
	Goals              []GoalView         `json:"goals"`
	LatestMood         *MoodView          `json:"latest_mood,omitempty"`
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
		`CREATE TABLE IF NOT EXISTS memory_nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			source_id TEXT NOT NULL DEFAULT '',
			provenance TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0.5,
			salience REAL NOT NULL,
			valence REAL NOT NULL,
			novelty REAL NOT NULL DEFAULT 0.5,
			state TEXT NOT NULL DEFAULT 'active'
				CHECK(state IN ('active','dormant')),
			embedding BLOB,
			embedding_scale REAL,
			embedding_dims INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT,
			last_recalled_at TEXT,
			recall_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS memory_nodes_recall
		 ON memory_nodes(state,salience DESC,updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS memory_nodes_source
		 ON memory_nodes(source_id,kind)`,
		`CREATE TABLE IF NOT EXISTS memory_edges (
			from_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			relation TEXT NOT NULL,
			weight REAL NOT NULL,
			created_at TEXT NOT NULL,
			reinforced_at TEXT NOT NULL,
			PRIMARY KEY(from_id,to_id,relation),
			FOREIGN KEY(from_id) REFERENCES memory_nodes(id) ON DELETE CASCADE,
			FOREIGN KEY(to_id) REFERENCES memory_nodes(id) ON DELETE CASCADE,
			CHECK(from_id <> to_id)
		)`,
		`CREATE INDEX IF NOT EXISTS memory_edges_from
		 ON memory_edges(from_id,weight DESC)`,
		`CREATE TABLE IF NOT EXISTS knowledge_sources (
			id TEXT PRIMARY KEY,
			uri TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			author TEXT NOT NULL DEFAULT '',
			published_at TEXT,
			retrieved_at TEXT NOT NULL,
			trust REAL NOT NULL,
			content_hash TEXT NOT NULL DEFAULT ''
		)`,
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
		`INSERT OR IGNORE INTO memory_nodes(
			id,kind,summary,source_id,provenance,confidence,salience,valence,novelty,
			state,created_at,updated_at,last_recalled_at,recall_count
		)
		SELECT id,kind,summary,source_id,'legacy memory',0.5,salience,valence,0.5,
			'active',created_at,created_at,last_recalled_at,recall_count
		FROM memories`,
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
	if memory.UpdatedAt.IsZero() {
		memory.UpdatedAt = memory.CreatedAt
	}
	if memory.Confidence == 0 {
		memory.Confidence = 0.5
	}
	if memory.Novelty == 0 {
		memory.Novelty = 0.5
	}
	embedding, scale := quantizeEmbedding(memory.Embedding)
	var expiresAt any
	if memory.ExpiresAt != nil {
		expiresAt = memory.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_nodes(
			id,kind,summary,source_id,provenance,confidence,salience,valence,novelty,
			state,embedding,embedding_scale,embedding_dims,created_at,updated_at,
			expires_at,last_recalled_at,recall_count
		) VALUES(?,?,?,?,?,?,?,?,?,'active',?,?,?,?,?,?,NULL,0)
		ON CONFLICT(id) DO UPDATE SET
			summary=excluded.summary,
			source_id=excluded.source_id,
			provenance=excluded.provenance,
			confidence=max(memory_nodes.confidence,excluded.confidence),
			salience=max(memory_nodes.salience,excluded.salience),
			valence=excluded.valence,
			novelty=excluded.novelty,
			state='active',
			embedding=coalesce(excluded.embedding,memory_nodes.embedding),
			embedding_scale=coalesce(excluded.embedding_scale,memory_nodes.embedding_scale),
			embedding_dims=max(memory_nodes.embedding_dims,excluded.embedding_dims),
			updated_at=excluded.updated_at,
			expires_at=excluded.expires_at`,
		memory.ID, memory.Kind, memory.Summary, memory.SourceID, memory.Provenance,
		clamp(memory.Confidence), clamp(memory.Salience), clampSigned(memory.Valence),
		clamp(memory.Novelty), nullableBytes(embedding), nullableScale(embedding, scale),
		len(memory.Embedding), memory.CreatedAt.Format(time.RFC3339Nano),
		memory.UpdatedAt.Format(time.RFC3339Nano), expiresAt)
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

func (s *Store) RecordAction(
	ctx context.Context,
	id, kind, targetID, content, result string,
	createdAt time.Time,
) error {
	if id == "" || kind == "" {
		return fmt.Errorf("action id and kind are required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actions(id,kind,target_id,content,result,created_at)
		 VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET result=excluded.result`,
		id, kind, targetID, content, result, createdAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) HasAction(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, fmt.Errorf("action id is required")
	}
	var exists bool
	err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM actions WHERE id=?)`,
		id,
	).Scan(&exists)
	return exists, err
}

func (s *Store) ObserveRelationship(
	ctx context.Context,
	subjectID string,
	familiarityDelta, trustDelta, affinityDelta float64,
	notes string,
	observedAt time.Time,
) error {
	if subjectID == "" {
		return fmt.Errorf("relationship subject id is required")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO relationships(
			subject_id,familiarity,trust,affinity,notes,last_interaction_at
		 ) VALUES(?,max(0,?),max(0,?),max(0,?),?,?)
		 ON CONFLICT(subject_id) DO UPDATE SET
			familiarity=min(1,max(0,relationships.familiarity+excluded.familiarity)),
			trust=min(1,max(0,relationships.trust+excluded.trust)),
			affinity=min(1,max(0,relationships.affinity+excluded.affinity)),
			notes=CASE
				WHEN excluded.notes='' THEN relationships.notes
				ELSE excluded.notes
			END,
			last_interaction_at=excluded.last_interaction_at`,
		subjectID,
		clampSigned(familiarityDelta),
		clampSigned(trustDelta),
		clampSigned(affinityDelta),
		notes,
		observedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot := Snapshot{Friend: s.friend}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*),
			coalesce(sum(CASE WHEN state='active' THEN 1 ELSE 0 END),0),
			coalesce(sum(CASE WHEN state='dormant' THEN 1 ELSE 0 END),0)
		 FROM memory_nodes`).
		Scan(&snapshot.MemoryCount, &snapshot.ActiveMemoryCount, &snapshot.DormantMemoryCount); err != nil {
		return Snapshot{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_edges`).
		Scan(&snapshot.MemoryEdgeCount); err != nil {
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
