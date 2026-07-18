package state

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	MemoryEpisode  = "episode"
	MemorySemantic = "semantic"
	MemoryPerson   = "person"
	MemorySelf     = "self"
	MemorySkill    = "skill"
)

type Edge struct {
	FromID   string  `json:"from_id"`
	ToID     string  `json:"to_id"`
	Relation string  `json:"relation"`
	Weight   float64 `json:"weight"`
}

type RecallResult struct {
	Memory Memory  `json:"memory"`
	Score  float64 `json:"score"`
}

type KnowledgeSource struct {
	ID          string
	URI         string
	Title       string
	Author      string
	PublishedAt *time.Time
	RetrievedAt time.Time
	Trust       float64
	ContentHash string
}

type RetentionPolicy struct {
	MaxActiveNodes  int
	MaxEdgesPerNode int
	EpisodeTTL      time.Duration
	DormantAfter    time.Duration
}

type ConsolidationReport struct {
	ExpiredNodes int `json:"expired_nodes"`
	DormantNodes int `json:"dormant_nodes"`
	PrunedEdges  int `json:"pruned_edges"`
	ActiveNodes  int `json:"active_nodes"`
	DormantTotal int `json:"dormant_total"`
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		MaxActiveNodes:  512,
		MaxEdgesPerNode: 12,
		EpisodeTTL:      30 * 24 * time.Hour,
		DormantAfter:    60 * 24 * time.Hour,
	}
}

// Learn stores a new observation with provenance. It vectorizes durable
// semantic memory and only sufficiently salient episodes.
func (s *Store) Learn(ctx context.Context, memory Memory) error {
	if memory.Provenance == "" {
		return fmt.Errorf("learned memory requires provenance")
	}
	if len(memory.Embedding) == 0 && shouldEmbed(memory) {
		memory.Embedding = FeatureHashEmbedding(memory.Summary, DefaultEmbeddingDimensions)
	}
	if memory.Kind == MemoryEpisode && memory.ExpiresAt == nil {
		expiresAt := time.Now().UTC().Add(DefaultRetentionPolicy().EpisodeTTL)
		memory.ExpiresAt = &expiresAt
	}
	return s.Remember(ctx, memory)
}

func shouldEmbed(memory Memory) bool {
	switch memory.Kind {
	case MemorySemantic, MemoryPerson, MemorySelf, MemorySkill:
		return true
	case MemoryEpisode:
		return memory.Salience >= 0.55 || math.Abs(memory.Valence) >= 0.65
	default:
		return memory.Salience >= 0.7
	}
}

func (s *Store) RegisterKnowledgeSource(ctx context.Context, source KnowledgeSource) error {
	if source.ID == "" || source.Title == "" {
		return fmt.Errorf("knowledge source id and title are required")
	}
	if source.RetrievedAt.IsZero() {
		source.RetrievedAt = time.Now().UTC()
	}
	var publishedAt any
	if source.PublishedAt != nil {
		publishedAt = source.PublishedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_sources(
			id,uri,title,author,published_at,retrieved_at,trust,content_hash
		) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			uri=excluded.uri,
			title=excluded.title,
			author=excluded.author,
			published_at=excluded.published_at,
			retrieved_at=excluded.retrieved_at,
			trust=excluded.trust,
			content_hash=excluded.content_hash`,
		source.ID, source.URI, source.Title, source.Author, publishedAt,
		source.RetrievedAt.Format(time.RFC3339Nano), clamp(source.Trust), source.ContentHash)
	return err
}

func (s *Store) Connect(ctx context.Context, edge Edge, at time.Time) error {
	if edge.FromID == "" || edge.ToID == "" || edge.Relation == "" {
		return fmt.Errorf("edge endpoints and relation are required")
	}
	if edge.FromID == edge.ToID {
		return fmt.Errorf("memory cannot connect to itself")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_edges(
			from_id,to_id,relation,weight,created_at,reinforced_at
		) VALUES(?,?,?,?,?,?)
		ON CONFLICT(from_id,to_id,relation) DO UPDATE SET
			weight=min(1,memory_edges.weight+excluded.weight*0.2),
			reinforced_at=excluded.reinforced_at`,
		edge.FromID, edge.ToID, edge.Relation, clamp(edge.Weight),
		at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
	return err
}

// Recall uses a compact quantized vector together with salience, confidence,
// emotion, and recency. Selected memories become slightly easier to recall
// together in the future, similar to sparse associative reinforcement.
func (s *Store) Recall(ctx context.Context, query string, limit int) ([]RecallResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if limit > 32 {
		limit = 32
	}
	queryVector := FeatureHashEmbedding(query, DefaultEmbeddingDimensions)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,kind,summary,source_id,provenance,confidence,salience,valence,
			novelty,embedding,embedding_scale,embedding_dims,created_at,updated_at,
			expires_at,last_recalled_at,recall_count
		 FROM memory_nodes
		 WHERE state='active' AND embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	results := make([]RecallResult, 0)
	for rows.Next() {
		var memory Memory
		var quantized []byte
		var scale float64
		var dimensions int
		var createdAt, updatedAt string
		var expiresAt, recalledAt sql.NullString
		if err := rows.Scan(
			&memory.ID, &memory.Kind, &memory.Summary, &memory.SourceID,
			&memory.Provenance, &memory.Confidence, &memory.Salience,
			&memory.Valence, &memory.Novelty, &quantized, &scale, &dimensions,
			&createdAt, &updatedAt, &expiresAt, &recalledAt, &memory.RecallCount,
		); err != nil {
			return nil, err
		}
		memory.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		memory.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		if expiresAt.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, expiresAt.String)
			if parseErr == nil {
				memory.ExpiresAt = &parsed
			}
		}
		if recalledAt.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, recalledAt.String)
			if parseErr == nil {
				memory.LastRecalledAt = &parsed
			}
		}
		if dimensions != len(quantized) || dimensions != len(queryVector) {
			continue
		}
		similarity := math.Max(0, cosineSimilarity(
			queryVector,
			dequantizeEmbedding(quantized, scale),
		))
		ageDays := math.Max(0, now.Sub(memory.UpdatedAt).Hours()/24)
		recency := math.Exp(-ageDays / 90)
		score := 0.58*similarity +
			0.17*memory.Salience +
			0.12*memory.Confidence +
			0.05*math.Abs(memory.Valence) +
			0.05*memory.Novelty +
			0.03*recency
		results = append(results, RecallResult{Memory: memory, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Memory.ID < results[j].Memory.ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	if err := s.reinforceRecall(ctx, results, now); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) reinforceRecall(ctx context.Context, results []RecallResult, at time.Time) error {
	if len(results) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, result := range results {
		if _, err := tx.ExecContext(ctx,
			`UPDATE memory_nodes
			 SET last_recalled_at=?,recall_count=recall_count+1
			 WHERE id=?`,
			at.Format(time.RFC3339Nano), result.Memory.ID); err != nil {
			return err
		}
	}
	for index := 1; index < len(results); index++ {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_edges(
				from_id,to_id,relation,weight,created_at,reinforced_at
			 ) VALUES(?,?,'co_recalled',0.08,?,?)
			 ON CONFLICT(from_id,to_id,relation) DO UPDATE SET
				weight=min(1,memory_edges.weight+0.03),
				reinforced_at=excluded.reinforced_at`,
			results[0].Memory.ID, results[index].Memory.ID,
			at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CompactConversation replaces only the explicitly summarized prefix. Raw
// messages are never removed unless a non-empty inspectable summary is stored.
func (s *Store) CompactConversation(
	ctx context.Context,
	conversationID, userID, summary string,
	through time.Time,
) (int64, error) {
	if conversationID == "" || userID == "" || summary == "" || through.IsZero() {
		return 0, fmt.Errorf("conversation, user, summary, and through are required")
	}
	now := time.Now().UTC()
	memory := Memory{
		ID:         fmt.Sprintf("conversation-summary:%s:%d", conversationID, through.Unix()),
		Kind:       MemoryEpisode,
		Summary:    summary,
		SourceID:   "conversation:" + conversationID,
		Provenance: "user conversation summarized through " + through.UTC().Format(time.RFC3339),
		Confidence: 0.8,
		Salience:   0.65,
		Novelty:    0.6,
		Embedding:  FeatureHashEmbedding(summary, DefaultEmbeddingDimensions),
		CreatedAt:  now,
	}
	if err := s.Remember(ctx, memory); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM conversations
		 WHERE conversation_id=? AND user_id=? AND created_at<=?`,
		conversationID, userID, through.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) Consolidate(
	ctx context.Context,
	at time.Time,
	policy RetentionPolicy,
) (ConsolidationReport, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if policy.MaxActiveNodes <= 0 {
		policy = DefaultRetentionPolicy()
	}
	if policy.MaxEdgesPerNode <= 0 {
		policy.MaxEdgesPerNode = 12
	}
	if policy.DormantAfter <= 0 {
		policy.DormantAfter = 60 * 24 * time.Hour
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsolidationReport{}, err
	}
	defer tx.Rollback()

	var report ConsolidationReport
	result, err := tx.ExecContext(ctx,
		`UPDATE memory_nodes
		 SET state='dormant',embedding=NULL,embedding_scale=NULL,embedding_dims=0,
		     updated_at=?
		 WHERE state='active' AND expires_at IS NOT NULL AND expires_at<=?`,
		at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
	if err != nil {
		return report, err
	}
	expired, _ := result.RowsAffected()
	report.ExpiredNodes = int(expired)

	dormantBefore := at.Add(-policy.DormantAfter).Format(time.RFC3339Nano)
	result, err = tx.ExecContext(ctx,
		`UPDATE memory_nodes
		 SET state='dormant',embedding=NULL,embedding_scale=NULL,embedding_dims=0,
		     updated_at=?
		 WHERE state='active' AND updated_at<?
		   AND salience<0.4 AND confidence<0.7 AND recall_count<3`,
		at.Format(time.RFC3339Nano), dormantBefore)
	if err != nil {
		return report, err
	}
	dormant, _ := result.RowsAffected()
	report.DormantNodes += int(dormant)

	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM memory_nodes WHERE state='active'`).Scan(&active); err != nil {
		return report, err
	}
	if overflow := active - policy.MaxActiveNodes; overflow > 0 {
		result, err = tx.ExecContext(ctx,
			`UPDATE memory_nodes
			 SET state='dormant',embedding=NULL,embedding_scale=NULL,embedding_dims=0,
			     updated_at=?
			 WHERE id IN (
				SELECT id FROM memory_nodes
				WHERE state='active'
				ORDER BY (salience*0.45 + confidence*0.3 + novelty*0.1 +
					min(recall_count,10)*0.015) ASC,
					updated_at ASC
				LIMIT ?
			 )`,
			at.Format(time.RFC3339Nano), overflow)
		if err != nil {
			return report, err
		}
		count, _ := result.RowsAffected()
		report.DormantNodes += int(count)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE memory_edges SET weight=weight*0.995`); err != nil {
		return report, err
	}
	result, err = tx.ExecContext(ctx,
		`DELETE FROM memory_edges WHERE rowid IN (
			SELECT rowid FROM (
				SELECT rowid,
					row_number() OVER (
						PARTITION BY from_id ORDER BY weight DESC,reinforced_at DESC
					) AS position
				FROM memory_edges
			) WHERE position>?
		 ) OR weight<0.03`,
		policy.MaxEdgesPerNode)
	if err != nil {
		return report, err
	}
	pruned, _ := result.RowsAffected()
	report.PrunedEdges = int(pruned)

	if err := tx.QueryRowContext(ctx,
		`SELECT
			coalesce(sum(CASE WHEN state='active' THEN 1 ELSE 0 END),0),
			coalesce(sum(CASE WHEN state='dormant' THEN 1 ELSE 0 END),0)
		 FROM memory_nodes`).Scan(&report.ActiveNodes, &report.DormantTotal); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	_, _ = s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return report, nil
}
