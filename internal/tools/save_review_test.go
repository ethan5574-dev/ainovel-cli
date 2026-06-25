package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestSaveReviewPersistsContractAssessment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":           3,
		"scope":             "chapter",
		"dimensions":        []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "mostly consistent"}, {"dimension": "character", "score": 82, "verdict": "pass", "comment": "stable characterization"}, {"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "slightly slow"}, {"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "coherent"}, {"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "normal"}, {"dimension": "hook", "score": 76, "verdict": "warning", "comment": "hook is average"}, {"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "prose is mostly acceptable"}},
		"issues":            []map[string]any{},
		"contract_status":   "partial",
		"contract_misses":   []string{"did not clearly foreshadow the inner sect trial invitation"},
		"contract_notes":    "Mainline progressed, but the second progression item in contract was not implemented.",
		"verdict":           "polish",
		"summary":           "This chapter basically achieved the goal, but contract still has missing items.",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review == nil {
		t.Fatal("expected review saved, got nil")
	}
	if review.ContractStatus != "partial" {
		t.Fatalf("unexpected contract status: %q", review.ContractStatus)
	}
	if len(review.ContractMisses) != 1 || review.ContractMisses[0] != "did not clearly foreshadow the inner sect trial invitation" {
		t.Fatalf("unexpected contract misses: %+v", review.ContractMisses)
	}
	if review.Dimension("aesthetic") == nil {
		t.Fatalf("expected aesthetic dimension persisted, got %+v", review.Dimensions)
	}
}

func TestSaveReviewRejectsMissingDimensions(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "mostly consistent"}},
		"issues":     []map[string]any{},
		"verdict":    "accept",
		"summary":    "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimensions must contain exactly") {
		t.Fatalf("expected dimensions validation error, got %v", err)
	}
}

func TestSaveReviewRejectsDimensionWithoutComment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "mostly consistent"},
			{"dimension": "character", "score": 82, "comment": "stable characterization"},
			{"dimension": "pacing", "score": 78},
			{"dimension": "continuity", "score": 84, "comment": "coherent"},
			{"dimension": "foreshadow", "score": 80, "comment": "normal"},
			{"dimension": "hook", "score": 76, "comment": "hook is average"},
			{"dimension": "aesthetic", "score": 81, "comment": "prose is mostly acceptable"},
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimension comment is required: pacing") {
		t.Fatalf("expected dimension comment validation error, got %v", err)
	}
}

func TestSaveReviewRejectsUnfinishedAffectedChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 80); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	for ch := 1; ch <= 58; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 58,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "mostly consistent"},
			{"dimension": "character", "score": 82, "comment": "stable characterization"},
			{"dimension": "pacing", "score": 58, "comment": "pacing needs rewrite"},
			{"dimension": "continuity", "score": 84, "comment": "coherent"},
			{"dimension": "foreshadow", "score": 80, "comment": "normal"},
			{"dimension": "hook", "score": 76, "comment": "hook is average"},
			{"dimension": "aesthetic", "score": 81, "comment": "prose is mostly acceptable"},
		},
		"issues":            []map[string]any{},
		"contract_status":   "partial",
		"verdict":           "polish",
		"summary":           "Need to polish chapter 58, cannot queue unfinished chapters.",
		"affected_chapters": []int{65},
		"contract_misses":   []string{"pacing goes beyond the responsibility of this chapter"},
		"contract_notes":    "Should only process completed chapters.",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "pending_rewrites can only contain completed chapters") {
		t.Fatalf("expected unfinished affected chapter rejection, got %v", err)
	}
	review, err := s.World.LoadReview(58)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review != nil {
		t.Fatalf("review should not be saved when pending rewrite validation fails: %+v", review)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowWriting && p.Flow != "" {
		t.Fatalf("flow should not enter rewrite/polish, got %s", p.Flow)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites should remain empty, got %v", p.PendingRewrites)
	}
}

// TestSaveReviewDerivesVerdictFromScore verifies: verdict is deterministically derived from score, and inconsistent
// verdict from the model (e.g. score=85 but filling in warning) no longer errors out, but is overridden with the correct value (pass).
// Regression prevention issue: mismatch between score/verdict in weak models previously caused save_review to fail repeatedly.
func TestSaveReviewDerivesVerdictFromScore(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "consistent"},
			{"dimension": "character", "score": 82, "comment": "stable"}, // omit verdict
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "slightly slow"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "coherent"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "normal"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "hook is average"},
			{"dimension": "aesthetic", "score": 85, "verdict": "warning", "comment": "prose is acceptable"}, // inconsistent: 85 but filled warning
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute should succeed (verdict auto-derived), got %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	// 85 → pass (overwriting the warning returned by the model); 82 omitted → pass.
	if d := review.Dimension("aesthetic"); d == nil || d.Verdict != "pass" {
		t.Fatalf("aesthetic verdict should be derived to pass, got %+v", d)
	}
	if d := review.Dimension("character"); d == nil || d.Verdict != "pass" {
		t.Fatalf("character verdict should be derived to pass, got %+v", d)
	}
}

func TestSaveReviewRejectsMissingAffectedChaptersForRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "mostly consistent"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "stable characterization"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "slightly slow"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "coherent"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "normal"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "hook is average"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "prose is mostly acceptable"},
		},
		"issues":  []map[string]any{},
		"verdict": "rewrite",
		"summary": "needs rewrite",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "affected_chapters is required") {
		t.Fatalf("expected affected_chapters validation error, got %v", err)
	}
}

func TestSaveReviewRejectsIssueWithoutEvidence(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "mostly consistent"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "stable characterization"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "slightly slow"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "coherent"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "normal"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "hook is average"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "prose is mostly acceptable"},
		},
		"issues": []map[string]any{
			{"type": "hook", "severity": "warning", "description": "hook at the end of chapter is weak"},
		},
		"verdict":           "polish",
		"summary":           "Need to strengthen the hook.",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "issue evidence is required") {
		t.Fatalf("expected issue evidence validation error, got %v", err)
	}
}
