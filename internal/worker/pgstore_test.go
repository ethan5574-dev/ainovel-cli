package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

// TestPGStore exercises PGStore against a real Postgres. It's skipped unless
// WORKER_TEST_DSN is set (e.g. postgres://postgres:test@localhost:55432/ainovel_platform
// ?sslmode=disable) — no DB is spun up automatically so `go test ./...` stays hermetic
// everywhere else.
func mustDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("WORKER_TEST_DSN")
	if dsn == "" {
		t.Skip("WORKER_TEST_DSN not set; skipping Postgres integration test")
	}
	return dsn
}

// seedNovel inserts a user + novel row directly (no worker-side API for this yet —
// that's the platform web app's job in Phase 2) and returns their IDs.
func seedNovel(t *testing.T, db *sql.DB, workspaceDir string) (userID, novelID string) {
	t.Helper()
	email := "test-" + time.Now().Format("150405.000000") + "@example.com"
	if err := db.QueryRow(`INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO novels (user_id, prompt, workspace_dir) VALUES ($1, $2, $3) RETURNING id
	`, userID, "a test prompt", workspaceDir).Scan(&novelID); err != nil {
		t.Fatalf("seed novel: %v", err)
	}
	return userID, novelID
}

func seedJob(t *testing.T, db *sql.DB, novelID string, budget float64) string {
	t.Helper()
	var jobID string
	if err := db.QueryRow(`
		INSERT INTO generation_jobs (novel_id, budget_usd) VALUES ($1, $2) RETURNING id
	`, novelID, budget).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return jobID
}

func seedJobTier(t *testing.T, db *sql.DB, novelID, tier string, budget float64) string {
	t.Helper()
	var jobID string
	if err := db.QueryRow(`
		INSERT INTO generation_jobs (novel_id, tier, budget_usd) VALUES ($1, $2, $3) RETURNING id
	`, novelID, tier, budget).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return jobID
}

func TestPGStore_MigrateIsIdempotent(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s1, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s1.Close() }()

	s2, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen (re-migrate): %v", err)
	}
	defer func() { _ = s2.Close() }()
}

func TestPGStore_ClaimNextJob_SkipsLockedAndSetsRunning(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-a")
	jobID := seedJob(t, s.db, novelID, 3.0)

	got, err := s.ClaimNextJob("worker-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got == nil {
		t.Fatal("expected a job, got nil")
	}
	if got.ID != jobID {
		t.Fatalf("claimed job %s, want %s", got.ID, jobID)
	}
	if got.Status != JobRunning {
		t.Fatalf("claimed job status = %s, want running", got.Status)
	}
	if got.NovelID != novelID || got.Prompt != "a test prompt" || got.WorkspaceDir != "/tmp/novel-a" {
		t.Fatalf("unexpected job fields: %+v", got)
	}

	// Queue is now empty.
	again, err := s.ClaimNextJob("worker-1")
	if err != nil {
		t.Fatalf("claim (empty queue): %v", err)
	}
	if again != nil {
		t.Fatalf("expected nil on empty queue, got %+v", again)
	}
}

// TestPGStore_ClaimNextJob_PaidTierJumpsQueue is Phase 5's "bán tốc độ" mechanism: a
// paid job queued after two older free-tier jobs still gets claimed first.
func TestPGStore_ClaimNextJob_PaidTierJumpsQueue(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-j")
	freeJob1 := seedJobTier(t, s.db, novelID, "free", 1.0)
	freeJob2 := seedJobTier(t, s.db, novelID, "free", 1.0)
	paidJob := seedJobTier(t, s.db, novelID, "paid", 1.0)

	got, err := s.ClaimNextJob("worker-1")
	if err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if got == nil || got.ID != paidJob || got.Tier != "paid" {
		t.Fatalf("claim 1 = %+v, want the paid job despite being queued last", got)
	}

	// Paid jobs exhausted; falls back to FIFO among the remaining free-tier jobs.
	got, err = s.ClaimNextJob("worker-1")
	if err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	if got == nil || got.ID != freeJob1 {
		t.Fatalf("claim 2 = %+v, want the older free job (%s)", got, freeJob1)
	}

	got, err = s.ClaimNextJob("worker-1")
	if err != nil {
		t.Fatalf("claim 3: %v", err)
	}
	if got == nil || got.ID != freeJob2 {
		t.Fatalf("claim 3 = %+v, want the remaining free job (%s)", got, freeJob2)
	}
}

// TestPGStore_ClaimNextJob_ConcurrentWorkersNeverDoubleClaim is the actual point of
// FOR UPDATE SKIP LOCKED: N queued jobs, N concurrent claimers, every job claimed by
// exactly one worker.
func TestPGStore_ClaimNextJob_ConcurrentWorkersNeverDoubleClaim(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-b")
	const n = 8
	want := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		want[seedJob(t, s.db, novelID, 1.0)] = true
	}

	var mu sync.Mutex
	claimed := make(map[string]int) // jobID -> claim count

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(workerID string) {
			defer wg.Done()
			j, err := s.ClaimNextJob(workerID)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if j == nil {
				return
			}
			mu.Lock()
			claimed[j.ID]++
			mu.Unlock()
		}(string(rune('a' + i)))
	}
	wg.Wait()

	if len(claimed) != n {
		t.Fatalf("expected %d distinct jobs claimed, got %d: %v", n, len(claimed), claimed)
	}
	for id, count := range claimed {
		if count != 1 {
			t.Errorf("job %s claimed %d times, want 1", id, count)
		}
		if !want[id] {
			t.Errorf("claimed unexpected job id %s", id)
		}
	}
}

func TestPGStore_Heartbeat_CompleteJob_FailJob(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-c")
	jobID := seedJob(t, s.db, novelID, 1.0)
	if _, err := s.ClaimNextJob("worker-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := s.Heartbeat(jobID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	var hb time.Time
	if err := s.db.QueryRow(`SELECT heartbeat_at FROM generation_jobs WHERE id = $1`, jobID).Scan(&hb); err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if hb.IsZero() {
		t.Fatal("heartbeat_at not set")
	}

	if err := s.CompleteJob(jobID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM generation_jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "done" {
		t.Fatalf("status = %s, want done", status)
	}

	jobID2 := seedJob(t, s.db, novelID, 1.0)
	if _, err := s.ClaimNextJob("worker-1"); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	if err := s.FailJob(jobID2, "budget exceeded"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	var status2, reason string
	if err := s.db.QueryRow(`SELECT status, error FROM generation_jobs WHERE id = $1`, jobID2).Scan(&status2, &reason); err != nil {
		t.Fatalf("read status 2: %v", err)
	}
	if status2 != "failed" || reason != "budget exceeded" {
		t.Fatalf("status=%s error=%q, want failed/budget exceeded", status2, reason)
	}
}

func TestPGStore_RequeueStale(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-d")
	jobID := seedJob(t, s.db, novelID, 1.0)
	if _, err := s.ClaimNextJob("worker-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Simulate a heartbeat from 1 hour ago (a crashed worker).
	if _, err := s.db.Exec(`UPDATE generation_jobs SET heartbeat_at = now() - interval '1 hour' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}

	n, err := s.RequeueStale(10 * time.Minute)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 {
		t.Fatalf("requeued %d jobs, want 1", n)
	}

	var status string
	var claimedBy sql.NullString
	if err := s.db.QueryRow(`SELECT status, claimed_by FROM generation_jobs WHERE id = $1`, jobID).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "queued" || claimedBy.Valid {
		t.Fatalf("status=%s claimed_by=%v, want queued/NULL", status, claimedBy)
	}
}

func TestPGStore_RecordUsage_SaveChapter_UpdateNovelStatus(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-e")
	jobID := seedJob(t, s.db, novelID, 1.0)

	if err := s.RecordUsage(UsageRecord{JobID: jobID, NovelID: novelID, InputTokens: 100, OutputTokens: 50, CacheReadTokens: 5, CostUSD: 0.01}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	var cost float64
	if err := s.db.QueryRow(`SELECT cost_usd FROM usage_ledger WHERE job_id = $1`, jobID).Scan(&cost); err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if cost != 0.01 {
		t.Fatalf("cost_usd = %v, want 0.01", cost)
	}

	if err := s.SaveChapter(ChapterRecord{NovelID: novelID, Number: 1, Title: "Chương một", StorageKey: "k1", WordCount: 500}); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	// Upsert: re-saving the same (novel_id, number) updates in place instead of erroring.
	if err := s.SaveChapter(ChapterRecord{NovelID: novelID, Number: 1, Title: "Chương một (sửa)", StorageKey: "k1v2", WordCount: 520}); err != nil {
		t.Fatalf("save chapter (upsert): %v", err)
	}
	var title, key string
	var wc int
	if err := s.db.QueryRow(`SELECT title, storage_key, word_count FROM chapters WHERE novel_id = $1 AND number = 1`, novelID).Scan(&title, &key, &wc); err != nil {
		t.Fatalf("read chapter: %v", err)
	}
	if title != "Chương một (sửa)" || key != "k1v2" || wc != 520 {
		t.Fatalf("chapter not upserted correctly: title=%q key=%q wc=%d", title, key, wc)
	}

	if err := s.UpdateNovelStatus(novelID, "complete"); err != nil {
		t.Fatalf("update novel status: %v", err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM novels WHERE id = $1`, novelID).Scan(&status); err != nil {
		t.Fatalf("read novel status: %v", err)
	}
	if status != "complete" {
		t.Fatalf("novel status = %s, want complete", status)
	}
}

func TestPGStore_SaveChapterReview_UpsertsOnConflict(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-f")

	rec := ChapterReviewRecord{
		NovelID: novelID,
		Chapter: 1,
		Scope:   "chapter",
		Verdict: "polish",
		Summary: "Needs pacing work.",
		Dimensions: []DimensionScoreRecord{
			{Dimension: "consistency", Score: 80},
			{Dimension: "hook", Score: 70, Comment: "weak opener"},
		},
	}
	if err := s.SaveChapterReview(rec); err != nil {
		t.Fatalf("save review: %v", err)
	}

	rec.Verdict = "accept"
	rec.Summary = "Fixed on revision."
	if err := s.SaveChapterReview(rec); err != nil {
		t.Fatalf("save review (upsert): %v", err)
	}

	var verdict, summary string
	var dimsJSON []byte
	if err := s.db.QueryRow(`
		SELECT verdict, summary, dimensions FROM chapter_reviews WHERE novel_id = $1 AND chapter = 1 AND scope = 'chapter'
	`, novelID).Scan(&verdict, &summary, &dimsJSON); err != nil {
		t.Fatalf("read review: %v", err)
	}
	if verdict != "accept" || summary != "Fixed on revision." {
		t.Fatalf("review not upserted: verdict=%q summary=%q", verdict, summary)
	}
	var dims []DimensionScoreRecord
	if err := json.Unmarshal(dimsJSON, &dims); err != nil {
		t.Fatalf("unmarshal dimensions: %v", err)
	}
	if len(dims) != 2 || dims[1].Comment != "weak opener" {
		t.Fatalf("unexpected dimensions: %+v", dims)
	}

	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM chapter_reviews WHERE novel_id = $1`, novelID).Scan(&count); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", count)
	}
}

func TestPGStore_SaveForeshadow_UpsertsOnConflict(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-g")

	rec := ForeshadowRecord{NovelID: novelID, ForeshadowID: "fs-1", Description: "The locket", PlantedAt: 1, Status: "planted"}
	if err := s.SaveForeshadow(rec); err != nil {
		t.Fatalf("save foreshadow: %v", err)
	}

	rec.Status = "resolved"
	rec.ResolvedAt = 9
	if err := s.SaveForeshadow(rec); err != nil {
		t.Fatalf("save foreshadow (upsert): %v", err)
	}

	var status string
	var resolvedAt sql.NullInt64
	if err := s.db.QueryRow(`
		SELECT status, resolved_at FROM foreshadows WHERE novel_id = $1 AND foreshadow_id = 'fs-1'
	`, novelID).Scan(&status, &resolvedAt); err != nil {
		t.Fatalf("read foreshadow: %v", err)
	}
	if status != "resolved" || !resolvedAt.Valid || resolvedAt.Int64 != 9 {
		t.Fatalf("foreshadow not upserted: status=%q resolved_at=%v", status, resolvedAt)
	}
}

func TestPGStore_SetQualityGate(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-h")

	if err := s.SetQualityGate(novelID, 55.5, false); err != nil {
		t.Fatalf("set quality gate (below threshold): %v", err)
	}
	var score float64
	var publishedAt sql.NullTime
	if err := s.db.QueryRow(`SELECT quality_score, published_at FROM novels WHERE id = $1`, novelID).Scan(&score, &publishedAt); err != nil {
		t.Fatalf("read novel: %v", err)
	}
	if score != 55.5 || publishedAt.Valid {
		t.Fatalf("expected score=55.5, unpublished; got score=%v published_at=%v", score, publishedAt)
	}

	if err := s.SetQualityGate(novelID, 82, true); err != nil {
		t.Fatalf("set quality gate (publish): %v", err)
	}
	if err := s.db.QueryRow(`SELECT quality_score, published_at FROM novels WHERE id = $1`, novelID).Scan(&score, &publishedAt); err != nil {
		t.Fatalf("read novel: %v", err)
	}
	if score != 82 || !publishedAt.Valid {
		t.Fatalf("expected score=82, published; got score=%v published_at=%v", score, publishedAt)
	}
}

func TestPGStore_TodaySpendUSD(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()
	s, err := OpenPGStore(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, novelID := seedNovel(t, s.db, "/tmp/novel-i")
	jobID := seedJob(t, s.db, novelID, 5.0)

	// Other tests in this file share the same DB and may have recorded usage earlier
	// today, so this asserts the delta TodaySpendUSD reports, not an absolute value.
	before, err := s.TodaySpendUSD()
	if err != nil {
		t.Fatalf("today spend (baseline): %v", err)
	}

	if err := s.RecordUsage(UsageRecord{JobID: jobID, NovelID: novelID, CostUSD: 1.25}); err != nil {
		t.Fatalf("record usage 1: %v", err)
	}
	if err := s.RecordUsage(UsageRecord{JobID: jobID, NovelID: novelID, CostUSD: 0.75}); err != nil {
		t.Fatalf("record usage 2: %v", err)
	}

	spent, err := s.TodaySpendUSD()
	if err != nil {
		t.Fatalf("today spend: %v", err)
	}
	if diff := spent - before; diff < 1.999 || diff > 2.001 {
		t.Fatalf("today spend delta = %v, want ~2.0", diff)
	}

	// A row recorded yesterday must not count toward today's total — this is the whole
	// point of gating on date_trunc('day', now()) instead of summing everything.
	if _, err := s.db.Exec(`
		INSERT INTO usage_ledger (job_id, novel_id, cost_usd, recorded_at) VALUES ($1, $2, 99, now() - interval '1 day')
	`, jobID, novelID); err != nil {
		t.Fatalf("seed yesterday's usage: %v", err)
	}
	spentAfter, err := s.TodaySpendUSD()
	if err != nil {
		t.Fatalf("today spend after yesterday row: %v", err)
	}
	if spentAfter != spent {
		t.Fatalf("today spend changed after inserting a yesterday-dated row: %v -> %v (yesterday's 99 must not count)", spent, spentAfter)
	}
}
