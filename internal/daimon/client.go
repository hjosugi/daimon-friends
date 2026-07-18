// Package daimon integrates friends with Daimon's PostgreSQL system of record
// and its compact, rebuildable semantic index.
package daimon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/hjosugi/daimon-friends/internal/activity"
	"github.com/hjosugi/daimon-friends/internal/birth"
)

const vectorDimensions = 384

type Config struct {
	DatabaseURL string
	EmbedURL    string
	EmbedAuth   bool
}

type Client struct {
	pool       *pgxpool.Pool
	embedURL   string
	embedToken *identityTokenSource
	httpClient *http.Client
}

func New(ctx context.Context, config Config) (*Client, error) {
	if config.DatabaseURL == "" || config.EmbedURL == "" {
		return nil, fmt.Errorf("database and embedding URLs are required")
	}
	pool, err := pgxpool.New(ctx, config.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := ensureVectorStore(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Client{
		pool:       pool,
		embedURL:   strings.TrimRight(config.EmbedURL, "/"),
		embedToken: newIdentityTokenSource(config.EmbedURL, config.EmbedAuth),
		httpClient: &http.Client{Timeout: 6 * time.Minute},
	}, nil
}

func (c *Client) Close() {
	c.pool.Close()
}

func (c *Client) Provision(
	ctx context.Context,
	friends []birth.Certificate,
) error {
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword(
		[]byte(base64.RawURLEncoding.EncodeToString(password)),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return err
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	for _, friend := range friends {
		account := activity.AccountFor(friend)
		if _, err := tx.Exec(ctx,
			`INSERT INTO users(
				id,username,email,password_hash,avatar_url,bio,created_at,updated_at
			 ) VALUES($1,$2,$3,$4,NULL,$5,$6,$6)
			 ON CONFLICT (id) DO UPDATE SET
				username=EXCLUDED.username,
				email=EXCLUDED.email,
				bio=EXCLUDED.bio,
				updated_at=EXCLUDED.updated_at`,
			account.ID, account.Username, account.Email, string(hash), account.Bio, now); err != nil {
			return fmt.Errorf("%s: %w", friend.ID, err)
		}
	}
	return tx.Commit(ctx)
}

func (c *Client) Publish(
	ctx context.Context,
	post activity.Post,
) (bool, error) {
	vector, err := c.embed(ctx, post.Text)
	if err != nil {
		return false, err
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx,
		`INSERT INTO posts(id,user_id,username,text,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$5) ON CONFLICT(id) DO NOTHING`,
		post.ID, post.UserID, post.Username, post.Text, post.CreatedAt)
	if err != nil {
		return false, err
	}
	for _, pov := range post.POVs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO povs(id,post_id,pov,is_auto,created_at)
			 VALUES($1,$2,$3,true,$4) ON CONFLICT(post_id,pov) DO NOTHING`,
			namedID("pov/"+post.ID+"/"+pov), post.ID, pov, post.CreatedAt); err != nil {
			return false, err
		}
	}
	if err := upsertPoint(ctx, tx, post, vector); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (c *Client) RecentHumanCandidate(
	ctx context.Context,
	actor activity.Post,
	since time.Time,
) (*activity.Candidate, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT
			p.id,p.user_id,coalesce(p.username,''),p.text,p.created_at,
			coalesce(
				array_agg(DISTINCT pv.pov) FILTER (WHERE pv.pov IS NOT NULL),
				ARRAY[]::varchar[]
			)
		 FROM posts p
		 JOIN users author ON author.id=p.user_id
		 LEFT JOIN povs pv ON pv.post_id=p.id
		 WHERE author.email NOT LIKE '%@bots.daimon.local'
		   AND p.created_at >= $1
		   AND NOT EXISTS (
			SELECT 1 FROM comments c
			JOIN users reactor ON reactor.id=c.user_id
			WHERE c.post_id=p.id
			  AND reactor.email LIKE '%@bots.daimon.local'
			  AND c.created_at >= $1
		   )
		 GROUP BY p.id,p.user_id,p.username,p.text,p.created_at
		 ORDER BY p.created_at DESC
		 LIMIT 30`,
		since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]activity.Candidate, 0)
	for rows.Next() {
		var candidate activity.Candidate
		if err := rows.Scan(
			&candidate.ID,
			&candidate.UserID,
			&candidate.Username,
			&candidate.Text,
			&candidate.CreatedAt,
			&candidate.POVs,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	index := int(stableSeed(actor.ID) % uint64(len(candidates)))
	return &candidates[index], nil
}

func (c *Client) React(
	ctx context.Context,
	actor activity.Post,
	target activity.Candidate,
	text string,
) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("reaction text is required")
	}
	now := time.Now().UTC()
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO likes(id,post_id,user_id,created_at)
		 VALUES($1,$2,$3,$4) ON CONFLICT(post_id,user_id) DO NOTHING`,
		namedID("like/"+actor.UserID+"/"+target.ID), target.ID, actor.UserID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO comments(id,post_id,user_id,text,created_at)
		 VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`,
		namedID("comment/"+actor.UserID+"/"+target.ID), target.ID, actor.UserID, text, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (c *Client) embed(ctx context.Context, text string) ([]float32, error) {
	var output struct {
		Vector []float32 `json:"vector"`
	}
	if err := c.doJSON(
		ctx,
		http.MethodPost,
		c.embedURL+"/embed",
		map[string]string{"text": text},
		&output,
	); err != nil {
		return nil, err
	}
	if len(output.Vector) != vectorDimensions {
		return nil, fmt.Errorf(
			"embedding dimensions=%d want=%d",
			len(output.Vector),
			vectorDimensions,
		)
	}
	return output.Vector, nil
}

func upsertPoint(
	ctx context.Context,
	tx pgx.Tx,
	post activity.Post,
	vector []float32,
) error {
	payload, err := json.Marshal(map[string]any{
		"post_id":    post.ID,
		"user_id":    post.UserID,
		"tags":       post.POVs,
		"created_at": post.CreatedAt.Unix(),
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO post_vectors(post_id, vector, payload, updated_at)
		VALUES($1, $2, $3::jsonb, now())
		ON CONFLICT(post_id) DO UPDATE SET
			vector=EXCLUDED.vector,
			payload=EXCLUDED.payload,
			updated_at=EXCLUDED.updated_at
	`, post.ID, vector, string(payload))
	return err
}

func (c *Client) doJSON(
	ctx context.Context,
	method, url string,
	input, output any,
) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.embedToken != nil {
		token, err := c.embedToken.Token(ctx)
		if err != nil {
			return fmt.Errorf("embedding identity token: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s %s: status %d: %s",
			method, url, response.StatusCode, strings.TrimSpace(string(detail)))
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}

func ensureVectorStore(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS post_vectors (
			post_id varchar PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
			vector real[] NOT NULL,
			payload jsonb NOT NULL DEFAULT '{}'::jsonb,
			updated_at timestamptz NOT NULL DEFAULT now(),
			CONSTRAINT post_vectors_dimensions
				CHECK (array_length(vector, 1) = 384)
		);
		CREATE INDEX IF NOT EXISTS ix_post_vectors_user
			ON post_vectors ((payload->>'user_id'));
		ALTER TABLE post_vectors ENABLE ROW LEVEL SECURITY;
	`)
	if err != nil {
		return fmt.Errorf("ensure vector store: %w", err)
	}
	return nil
}

func namedID(name string) string {
	return uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte("https://daimon.app/friends/"+name),
	).String()
}

func stableSeed(value string) uint64 {
	var result uint64 = 14695981039346656037
	for index := 0; index < len(value); index++ {
		result ^= uint64(value[index])
		result *= 1099511628211
	}
	return result
}
