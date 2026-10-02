package worker

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/diag"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newlyCompletedChapters returns chapters present in cur.CompletedChapters but not in
// prev's, in ascending order. prev may be nil (first poll of a run).
func newlyCompletedChapters(prev, cur *domain.Progress) []int {
	if cur == nil {
		return nil
	}
	seen := make(map[int]bool, len(cur.CompletedChapters))
	if prev != nil {
		for _, c := range prev.CompletedChapters {
			seen[c] = true
		}
	}
	var fresh []int
	for _, c := range cur.CompletedChapters {
		if !seen[c] {
			fresh = append(fresh, c)
		}
	}
	return fresh
}

// harvestChapter reads one committed chapter's text and title out of the book's local
// store, pushes the text to chapters (object storage or its local stand-in), and records
// the resulting key in jobs. Called once per chapter newlyCompletedChapters reports new.
func harvestChapter(st *store.Store, chapters ChapterStore, jobs JobStore, novelID string, chapter int) error {
	text, err := st.Drafts.LoadChapterText(chapter)
	if err != nil {
		return fmt.Errorf("load chapter %d text: %w", chapter, err)
	}
	if text == "" {
		return fmt.Errorf("chapter %d marked complete but chapters/%02d.md is empty", chapter, chapter)
	}

	title := ""
	if entry, err := st.Outline.GetChapterOutline(chapter); err == nil && entry != nil {
		title = entry.Title
	}

	key, err := chapters.Save(novelID, chapter, text)
	if err != nil {
		return fmt.Errorf("save chapter %d to storage: %w", chapter, err)
	}

	wordCount := 0
	if p, err := st.Progress.Load(); err == nil && p != nil {
		wordCount = p.ChapterWordCounts[chapter]
	}

	return jobs.SaveChapter(ChapterRecord{
		NovelID:    novelID,
		Number:     chapter,
		Title:      title,
		StorageKey: key,
		WordCount:  wordCount,
	})
}

// harvestReviews pushes every Editor review (chapter/arc/global scope) into
// chapter_reviews. Called once at the end of a run — reviews accumulate over the whole
// book, so there's no incremental "newly reviewed" signal like newlyCompletedChapters.
func harvestReviews(st *store.Store, jobs JobStore, novelID string) error {
	reviews, err := st.World.LoadAllReviews()
	if err != nil {
		return fmt.Errorf("load reviews: %w", err)
	}
	for _, r := range reviews {
		dims := make([]DimensionScoreRecord, 0, len(r.Dimensions))
		for _, d := range r.Dimensions {
			dims = append(dims, DimensionScoreRecord{
				Dimension: d.Dimension,
				Score:     d.Score,
				Comment:   d.Comment,
			})
		}
		if err := jobs.SaveChapterReview(ChapterReviewRecord{
			NovelID:    novelID,
			Chapter:    r.Chapter,
			Scope:      r.Scope,
			Verdict:    r.Verdict,
			Summary:    r.Summary,
			Dimensions: dims,
		}); err != nil {
			return fmt.Errorf("save review (chapter %d, scope %s): %w", r.Chapter, r.Scope, err)
		}
	}
	return nil
}

// harvestForeshadows pushes the full foreshadow ledger into foreshadows. Called once at
// the end of a run — same reasoning as harvestReviews, the ledger has no per-chapter diff.
func harvestForeshadows(st *store.Store, jobs JobStore, novelID string) error {
	entries, err := st.World.LoadForeshadowLedger()
	if err != nil {
		return fmt.Errorf("load foreshadow ledger: %w", err)
	}
	for _, e := range entries {
		if err := jobs.SaveForeshadow(ForeshadowRecord{
			NovelID:      novelID,
			ForeshadowID: e.ID,
			Description:  e.Description,
			PlantedAt:    e.PlantedAt,
			Status:       e.Status,
			ResolvedAt:   e.ResolvedAt,
		}); err != nil {
			return fmt.Errorf("save foreshadow %s: %w", e.ID, err)
		}
	}
	return nil
}

// computeQualityGate averages the dimension scores of all chapter-scope Editor reviews
// and records it via jobs.SetQualityGate, publishing the novel when the average meets
// diag.ThresholdDimScoreLow — reusing the engine's own existing quality bar
// (ChronicLowDimension diagnostic) rather than inventing a new threshold. A book with no
// chapter reviews yet is left unpublished (score 0, publish false).
func computeQualityGate(st *store.Store, jobs JobStore, novelID string) error {
	reviews, err := st.World.LoadAllReviews()
	if err != nil {
		return fmt.Errorf("load reviews: %w", err)
	}
	var sum, count int
	for _, r := range reviews {
		if r.Scope != "chapter" {
			continue
		}
		for _, d := range r.Dimensions {
			sum += d.Score
			count++
		}
	}
	var avg float64
	if count > 0 {
		avg = float64(sum) / float64(count)
	}
	publish := count > 0 && avg >= float64(diag.ThresholdDimScoreLow)
	if err := jobs.SetQualityGate(novelID, avg, publish); err != nil {
		return fmt.Errorf("set quality gate: %w", err)
	}
	return nil
}

// harvestUsage pushes the book's final meta/usage.json totals into usage_ledger. Called
// once at the end of a run, not per chapter — usage.json already holds cumulative totals.
func harvestUsage(st *store.Store, jobs JobStore, jobID, novelID string) error {
	u, err := st.Usage.Load()
	if err != nil {
		return fmt.Errorf("load usage: %w", err)
	}
	if u == nil {
		return nil
	}
	return jobs.RecordUsage(UsageRecord{
		JobID:           jobID,
		NovelID:         novelID,
		InputTokens:     int64(u.Overall.Input),
		OutputTokens:    int64(u.Overall.Output),
		CacheReadTokens: int64(u.Overall.CacheRead),
		CostUSD:         u.Overall.Cost,
	})
}
