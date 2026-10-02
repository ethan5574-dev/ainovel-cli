package worker

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestNewlyCompletedChapters(t *testing.T) {
	prev := &domain.Progress{CompletedChapters: []int{1}}
	cur := &domain.Progress{CompletedChapters: []int{1, 2, 3}}

	got := newlyCompletedChapters(prev, cur)
	want := []int{2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	if got := newlyCompletedChapters(nil, nil); got != nil {
		t.Fatalf("nil cur: got %v, want nil", got)
	}
	if got := newlyCompletedChapters(nil, &domain.Progress{CompletedChapters: []int{5}}); len(got) != 1 || got[0] != 5 {
		t.Fatalf("first poll: got %v, want [5]", got)
	}
}

func TestHarvestChapter(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)

	if err := st.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		TotalChapters:     3,
		CompletedChapters: []int{1},
		ChapterWordCounts: map[int]int{1: 500},
	}); err != nil {
		t.Fatalf("seed progress: %v", err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "Chương một nội dung."); err != nil {
		t.Fatalf("seed chapter text: %v", err)
	}

	chapters := newMemChapterStore()
	jobs := newMemStore(Job{ID: "job-1", NovelID: "novel-1"})

	if err := harvestChapter(st, chapters, jobs, "novel-1", 1); err != nil {
		t.Fatalf("harvestChapter: %v", err)
	}

	if len(jobs.chapters) != 1 {
		t.Fatalf("expected 1 saved chapter record, got %d", len(jobs.chapters))
	}
	rec := jobs.chapters[0]
	if rec.NovelID != "novel-1" || rec.Number != 1 || rec.WordCount != 500 {
		t.Fatalf("unexpected chapter record: %+v", rec)
	}
	if content, ok := chapters.saved[rec.StorageKey]; !ok || content != "Chương một nội dung." {
		t.Fatalf("chapter text not persisted under key %q: %q", rec.StorageKey, content)
	}
}

func TestHarvestChapter_EmptyTextFails(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Progress.Save(&domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}}); err != nil {
		t.Fatalf("seed progress: %v", err)
	}
	// No chapters/01.md written — simulates a progress/draft race.
	if err := harvestChapter(st, newMemChapterStore(), newMemStore(), "novel-1", 1); err == nil {
		t.Fatal("expected error when chapter text is missing, got nil")
	}
}

func TestHarvestUsage(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Usage.Save(domain.UsageState{
		Schema: domain.UsageSchemaVersion,
		Overall: domain.AgentUsageTotals{
			Input:     10000,
			Output:    2000,
			CacheRead: 500,
			Cost:      0.42,
		},
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	jobs := newMemStore(Job{ID: "job-1", NovelID: "novel-1"})
	if err := harvestUsage(st, jobs, "job-1", "novel-1"); err != nil {
		t.Fatalf("harvestUsage: %v", err)
	}

	if len(jobs.usage) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(jobs.usage))
	}
	got := jobs.usage[0]
	want := UsageRecord{JobID: "job-1", NovelID: "novel-1", InputTokens: 10000, OutputTokens: 2000, CacheReadTokens: 500, CostUSD: 0.42}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestHarvestReviews(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)

	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 1,
		Scope:   "chapter",
		Verdict: "accept",
		Summary: "Solid opening.",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 90, Comment: "clean"},
			{Dimension: "hook", Score: 80},
		},
	}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 5,
		Scope:   "arc",
		Verdict: "polish",
		Summary: "Arc needs pacing work.",
	}); err != nil {
		t.Fatalf("seed arc review: %v", err)
	}

	jobs := newMemStore()
	if err := harvestReviews(st, jobs, "novel-1"); err != nil {
		t.Fatalf("harvestReviews: %v", err)
	}
	if len(jobs.reviews) != 2 {
		t.Fatalf("expected 2 saved reviews, got %d", len(jobs.reviews))
	}
	rec := jobs.reviews[0]
	if rec.NovelID != "novel-1" || rec.Chapter != 1 || rec.Scope != "chapter" || len(rec.Dimensions) != 2 {
		t.Fatalf("unexpected review record: %+v", rec)
	}
}

func TestHarvestForeshadows(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)

	if err := st.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "fs-1", Description: "The locket", PlantedAt: 1, Status: "planted"},
		{ID: "fs-2", Description: "The letter", PlantedAt: 2, Status: "resolved", ResolvedAt: 9},
	}); err != nil {
		t.Fatalf("seed foreshadow ledger: %v", err)
	}

	jobs := newMemStore()
	if err := harvestForeshadows(st, jobs, "novel-1"); err != nil {
		t.Fatalf("harvestForeshadows: %v", err)
	}
	if len(jobs.foreshadows) != 2 {
		t.Fatalf("expected 2 saved foreshadows, got %d", len(jobs.foreshadows))
	}
	if jobs.foreshadows[1].Status != "resolved" || jobs.foreshadows[1].ResolvedAt != 9 {
		t.Fatalf("unexpected foreshadow record: %+v", jobs.foreshadows[1])
	}
}

func TestComputeQualityGate(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)

	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", Verdict: "accept",
		Dimensions: []domain.DimensionScore{{Dimension: "consistency", Score: 90}, {Dimension: "hook", Score: 90}},
	}); err != nil {
		t.Fatalf("seed review: %v", err)
	}

	jobs := newMemStore()
	if err := computeQualityGate(st, jobs, "novel-1"); err != nil {
		t.Fatalf("computeQualityGate: %v", err)
	}
	if score := jobs.qualityScore["novel-1"]; score != 90 {
		t.Fatalf("expected quality score 90, got %v", score)
	}
	if !jobs.published["novel-1"] {
		t.Fatal("expected novel to be published at score 90")
	}
}

func TestComputeQualityGate_BelowThresholdDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)

	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", Verdict: "rewrite",
		Dimensions: []domain.DimensionScore{{Dimension: "consistency", Score: 40}},
	}); err != nil {
		t.Fatalf("seed review: %v", err)
	}

	jobs := newMemStore()
	if err := computeQualityGate(st, jobs, "novel-1"); err != nil {
		t.Fatalf("computeQualityGate: %v", err)
	}
	if jobs.published["novel-1"] {
		t.Fatal("expected novel to remain unpublished below threshold")
	}
}

func TestComputeQualityGate_NoReviewsDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)

	jobs := newMemStore()
	if err := computeQualityGate(st, jobs, "novel-1"); err != nil {
		t.Fatalf("computeQualityGate: %v", err)
	}
	if jobs.published["novel-1"] {
		t.Fatal("expected no publish with zero reviews")
	}
}

func TestHarvestUsage_NoUsageFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	jobs := newMemStore(Job{ID: "job-1", NovelID: "novel-1"})
	if err := harvestUsage(st, jobs, "job-1", "novel-1"); err != nil {
		t.Fatalf("harvestUsage with no usage.json: %v", err)
	}
	if len(jobs.usage) != 0 {
		t.Fatalf("expected no usage record, got %v", jobs.usage)
	}
}
