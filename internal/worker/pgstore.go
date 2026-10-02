package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// PGStore is the Postgres-backed JobStore. It talks to the DB via database/sql so
// callers aren't coupled to pgx's native API — only this file imports it.
type PGStore struct {
	db *sql.DB
}

// OpenPGStore opens a connection pool and applies pending migrations before returning,
// so every caller sees an up-to-date schema without a separate migrate step.
func OpenPGStore(ctx context.Context, dsn string) (*PGStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &PGStore{db: db}, nil
}

func (s *PGStore) Close() error { return s.db.Close() }

// ClaimNextJob uses FOR UPDATE SKIP LOCKED so concurrent workers each grab a distinct
// row instead of blocking on or double-claiming the same one. Ordering puts tier='paid'
// jobs first regardless of when they were queued — Phase 5's "bán tốc độ" ("sells speed,
// not access"): a paid job submitted after a busy free-tier queue still jumps ahead,
// then both tiers fall back to FIFO within themselves via created_at.
func (s *PGStore) ClaimNextJob(workerID string) (*Job, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var j Job
	err = tx.QueryRowContext(ctx, `
		SELECT j.id, j.novel_id, n.prompt, n.workspace_dir, j.tier, j.budget_usd, COALESCE(j.max_chapters, 0)
		FROM generation_jobs j
		JOIN novels n ON n.id = j.novel_id
		WHERE j.id = (
			SELECT id FROM generation_jobs
			WHERE status = 'queued'
			ORDER BY (tier = 'paid') DESC, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
	`).Scan(&j.ID, &j.NovelID, &j.Prompt, &j.WorkspaceDir, &j.Tier, &j.BudgetUSD, &j.MaxChapters)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Status = JobRunning

	if _, err := tx.ExecContext(ctx, `
		UPDATE generation_jobs
		SET status = 'running', claimed_by = $1, claimed_at = now(), heartbeat_at = now(), updated_at = now()
		WHERE id = $2
	`, workerID, j.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE novels SET status = 'generating', updated_at = now() WHERE id = $1`, j.NovelID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *PGStore) Heartbeat(jobID string) error {
	_, err := s.db.Exec(`UPDATE generation_jobs SET heartbeat_at = now() WHERE id = $1 AND status = 'running'`, jobID)
	return err
}

func (s *PGStore) CompleteJob(jobID string) error {
	_, err := s.db.Exec(`UPDATE generation_jobs SET status = 'done', updated_at = now() WHERE id = $1`, jobID)
	return err
}

func (s *PGStore) FailJob(jobID, reason string) error {
	_, err := s.db.Exec(`UPDATE generation_jobs SET status = 'failed', error = $2, updated_at = now() WHERE id = $1`, jobID, reason)
	return err
}

// RequeueStale is how a job survives its worker dying mid-run: on any worker's next
// sweep, a running job whose heartbeat has gone silent goes back to queued and picks
// up wherever the engine's own checkpoints left it (Resume(), not a restart from zero).
func (s *PGStore) RequeueStale(staleAfter time.Duration) (int, error) {
	cutoff := time.Now().Add(-staleAfter)
	res, err := s.db.Exec(`
		UPDATE generation_jobs
		SET status = 'queued', claimed_by = NULL, claimed_at = NULL, heartbeat_at = NULL, updated_at = now()
		WHERE status = 'running' AND heartbeat_at < $1
	`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (s *PGStore) RecordUsage(rec UsageRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO usage_ledger (job_id, novel_id, input_tokens, output_tokens, cache_read_tokens, cost_usd)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, rec.JobID, rec.NovelID, rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CostUSD)
	return err
}

func (s *PGStore) SaveChapter(rec ChapterRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO chapters (novel_id, number, title, storage_key, word_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (novel_id, number) DO UPDATE
		SET title = EXCLUDED.title, storage_key = EXCLUDED.storage_key, word_count = EXCLUDED.word_count
	`, rec.NovelID, rec.Number, rec.Title, rec.StorageKey, rec.WordCount)
	return err
}

func (s *PGStore) UpdateNovelStatus(novelID, status string) error {
	_, err := s.db.Exec(`UPDATE novels SET status = $2, updated_at = now() WHERE id = $1`, novelID, status)
	return err
}

func (s *PGStore) SaveChapterReview(rec ChapterReviewRecord) error {
	dims, err := json.Marshal(rec.Dimensions)
	if err != nil {
		return fmt.Errorf("marshal dimensions: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO chapter_reviews (novel_id, chapter, scope, verdict, summary, dimensions)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (novel_id, chapter, scope) DO UPDATE
		SET verdict = EXCLUDED.verdict, summary = EXCLUDED.summary, dimensions = EXCLUDED.dimensions,
		    recorded_at = now()
	`, rec.NovelID, rec.Chapter, rec.Scope, rec.Verdict, rec.Summary, dims)
	return err
}

func (s *PGStore) SaveForeshadow(rec ForeshadowRecord) error {
	var resolvedAt *int
	if rec.ResolvedAt > 0 {
		resolvedAt = &rec.ResolvedAt
	}
	_, err := s.db.Exec(`
		INSERT INTO foreshadows (novel_id, foreshadow_id, description, planted_at, status, resolved_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (novel_id, foreshadow_id) DO UPDATE
		SET description = EXCLUDED.description, status = EXCLUDED.status,
		    resolved_at = EXCLUDED.resolved_at, updated_at = now()
	`, rec.NovelID, rec.ForeshadowID, rec.Description, rec.PlantedAt, rec.Status, resolvedAt)
	return err
}

func (s *PGStore) SetQualityGate(novelID string, score float64, publish bool) error {
	if publish {
		_, err := s.db.Exec(`
			UPDATE novels SET quality_score = $2, published_at = now(), updated_at = now() WHERE id = $1
		`, novelID, score)
		return err
	}
	_, err := s.db.Exec(`
		UPDATE novels SET quality_score = $2, updated_at = now() WHERE id = $1
	`, novelID, score)
	return err
}

// TodaySpendUSD uses the DB's own "now()" (not the worker process's clock) so every
// worker instance agrees on the same day boundary regardless of its own timezone.
func (s *PGStore) TodaySpendUSD() (float64, error) {
	var total float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0) FROM usage_ledger WHERE recorded_at >= date_trunc('day', now())
	`).Scan(&total)
	return total, err
}
