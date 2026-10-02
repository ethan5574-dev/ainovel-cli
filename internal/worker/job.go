package worker

import "time"

// JobStatus mirrors the generation_jobs.status column.
type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

// Job is one row of generation_jobs, joined with the fields RunJob needs from novels.
type Job struct {
	ID           string
	NovelID      string
	Prompt       string
	WorkspaceDir string
	Status       JobStatus
	Tier         string
	BudgetUSD    float64
	MaxChapters  int // 0 means unset (no override)
}

// UsageRecord is one entry appended to usage_ledger after a run.
type UsageRecord struct {
	JobID           string
	NovelID         string
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	CostUSD         float64
}

// ChapterRecord is one entry appended to chapters after a chapter is committed.
type ChapterRecord struct {
	NovelID    string
	Number     int
	Title      string
	StorageKey string
	WordCount  int
}

// DimensionScoreRecord is one entry of a ChapterReviewRecord's Dimensions — mirrors
// domain.DimensionScore, kept as its own type so worker doesn't otherwise need to
// import internal/domain in job.go's public surface.
type DimensionScoreRecord struct {
	Dimension string `json:"dimension"`
	Score     int    `json:"score"`
	Comment   string `json:"comment,omitempty"`
}

// ChapterReviewRecord is one Editor review (chapter/arc/global scope), upserted into
// chapter_reviews — the data the Phase 3 provenance page reads.
type ChapterReviewRecord struct {
	NovelID    string
	Chapter    int
	Scope      string
	Verdict    string
	Summary    string
	Dimensions []DimensionScoreRecord
}

// ForeshadowRecord is one foreshadow-ledger entry, upserted into foreshadows.
type ForeshadowRecord struct {
	NovelID      string
	ForeshadowID string
	Description  string
	PlantedAt    int
	Status       string
	ResolvedAt   int // 0 means unresolved
}

// JobStore is the persistence boundary between the worker loop and Postgres.
// A fake implementation backs unit tests for the harvest logic without a real DB;
// see memstore_test.go.
type JobStore interface {
	// ClaimNextJob atomically takes the oldest queued job (FOR UPDATE SKIP LOCKED,
	// so N worker processes never race on the same row) and marks it running.
	// Returns nil, nil when the queue is empty.
	ClaimNextJob(workerID string) (*Job, error)
	Heartbeat(jobID string) error
	CompleteJob(jobID string) error
	FailJob(jobID, reason string) error
	// RequeueStale puts running jobs whose heartbeat is older than staleAfter back to
	// queued — the recovery path for a worker that crashed mid-job. Returns the count requeued.
	RequeueStale(staleAfter time.Duration) (int, error)
	RecordUsage(rec UsageRecord) error
	SaveChapter(rec ChapterRecord) error
	UpdateNovelStatus(novelID, status string) error
	SaveChapterReview(rec ChapterReviewRecord) error
	SaveForeshadow(rec ForeshadowRecord) error
	// SetQualityGate records the book's average Editor dimension score and, if publish
	// is true, stamps published_at — the only thing that makes a novel visible on the
	// public read page (Phase 3).
	SetQualityGate(novelID string, score float64, publish bool) error
	// TodaySpendUSD sums usage_ledger.cost_usd recorded since local midnight — the
	// system-wide daily budget cap (plan's cost layer 3): the worker checks this before
	// claiming a new job, so a busy day queues jobs instead of spending past the cap.
	TodaySpendUSD() (float64, error)
}
