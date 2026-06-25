package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestCommitChapterRejectsNonPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := store.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := store.Progress.SetPendingRewrites([]int{2}, "test rewrite"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}
	if err := store.Drafts.SaveDraft(3, "This is the body of the incorrect chapter."); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(store)
	args, err := json.Marshal(map[string]any{
		"chapter":         3,
		"summary":         "incorrect submit",
		"characters":      []string{"protagonist"},
		"key_events":      []string{"mistaken submit"},
		"timeline_events": []any{},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected commit to be rejected during rewrite flow")
	}

	if _, err := os.Stat(dir + "/chapters/03.md"); !os.IsNotExist(err) {
		t.Fatalf("chapter should not be persisted, stat err=%v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 2 {
		t.Fatalf("completed chapters should only contain original chapter 2, got %v", progress.CompletedChapters)
	}
	if progress.CurrentChapter != 3 {
		t.Fatalf("current chapter should not advance beyond original progress, got %d", progress.CurrentChapter)
	}
}

func TestCommitChapterAllowsPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := store.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := store.Progress.SetPendingRewrites([]int{2}, "test rewrite"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}
	if err := store.Drafts.SaveDraft(2, "This is the body of the correct chapter to be rewritten."); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(store)
	args, err := json.Marshal(map[string]any{
		"chapter":         2,
		"summary":         "correct submit",
		"characters":      []string{"protagonist"},
		"key_events":      []string{"completed rewrite"},
		"timeline_events": []any{},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(dir + "/chapters/02.md"); err != nil {
		t.Fatalf("chapter should be persisted: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 2 {
		t.Fatalf("unexpected completed chapters: %v", progress.CompletedChapters)
	}
	pending, err := store.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("LoadPendingCommit: %v", err)
	}
	if pending != nil {
		t.Fatalf("expected pending commit cleared, got %+v", pending)
	}
}

// TestCommitChapterUpdatesCastLedger verifies: commit_chapter accumulates this chapter's characters into cast_ledger,
// brief_role provided by cast_intros is adopted, and core characters in characters.json do not enter the ledger.
func TestCommitChapterUpdatesCastLedger(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// Set core character profiles (these should not enter cast_ledger)
	if err := s.Characters.Save([]domain.Character{
		{Name: "Lin Mo", Role: "protagonist", Tier: "core"},
		{Name: "Li Qingyan", Role: "mentor", Tier: "important"},
	}); err != nil {
		t.Fatalf("Save core characters: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "Chapter 1 text: Lin Mo met innkeeper Lao Zhou and servant Ah Yun."); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    1,
		"summary":    "Lin Mo checks into the inn",
		"characters": []string{"Lin Mo", "Li Qingyan", "Lao Zhou", "Ah Yun"},
		"key_events": []string{"check-in"},
		"cast_intros": []any{
			map[string]any{"name": "Lao Zhou", "brief_role": "innkeeper"},
			map[string]any{"name": "Ah Yun", "brief_role": "inn servant"},
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, err := s.Cast.Load()
	if err != nil {
		t.Fatalf("Cast.Load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 ledger entries (Lao Zhou/Ah Yun), got %d: %+v", len(entries), entries)
	}
	byName := map[string]domain.CastEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e, ok := byName["Lao Zhou"]; !ok || e.BriefRole != "innkeeper" || e.FirstSeenChapter != 1 {
		t.Errorf("Lao Zhou entry wrong: %+v", e)
	}
	if e, ok := byName["Ah Yun"]; !ok || e.BriefRole != "inn servant" || e.AppearanceCount != 1 {
		t.Errorf("Ah Yun entry wrong: %+v", e)
	}
	if _, ok := byName["Lin Mo"]; ok {
		t.Errorf("Core character Lin Mo should not enter ledger")
	}
	if _, ok := byName["Li Qingyan"]; ok {
		t.Errorf("Core character Li Qingyan should not enter ledger")
	}
}

// TestCommitChapterNonLayeredRecompletesAfterRework verifies that for a non-layered book after completing and reopen rework,
// when the reworked chapter is committed and the queue is drained, it automatically returns to complete (covers non-layered branch determining completion after drain).
func TestCommitChapterNonLayeredRecompletesAfterRework(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// Two chapters written and completed. Chapter 2 has drafts/chapters ready for rework submission.
	ch2 := "Chapter 2 original body, used to simulate committed final draft."
	if err := s.Drafts.SaveDraft(2, ch2); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, ch2); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete(1): %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(ch2)), "", ""); err != nil {
		t.Fatalf("MarkChapterComplete(2): %v", err)
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	// reopen Chapter 2 -> phase back to writing, PendingRewrites=[2], flow=rewriting
	if err := s.Progress.Reopen([]int{2}, "rework"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// Rework commit (draft must be different from final draft to release)
	if err := s.Drafts.SaveDraft(2, ch2+"\n\nRework added paragraph."); err != nil {
		t.Fatalf("SaveDraft (reworked): %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"summary":    "Rework summary",
		"characters": []string{"protagonist"},
		"key_events": []string{"clean"},
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute rework commit: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["book_complete"] != true {
		t.Errorf("book_complete = %v, want true", payload["book_complete"])
	}

	p, _ := s.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		t.Errorf("phase = %s, want complete (should automatically re-complete)", p.Phase)
	}
	if len(p.PendingRewrites) != 0 {
		t.Errorf("PendingRewrites = %v, want empty", p.PendingRewrites)
	}
}

// TestCommitChapterLayeredReopenRecompletesDespiteOpenThread verifies closing: after a layered book is reopened
// and reworked, even if the compass still has unresolved long threads (rework might disturb them), once drained it re-completes based on "structural completeness" —
// not stuck in writing, preventing the out-of-bounds writing infinite loop at the end of the final volume.
// Counterevidence: if the reopen path still uses quality-level layeredBookComplete, the open thread in this case would return false,
// book_complete would be false, and the test would fail.
func TestCommitChapterLayeredReopenRecompletesDespiteOpenThread(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// One volume, one arc, two chapters, all expanded
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Volume 1", "theme": "Theme",
			"arcs": []map[string]any{{
				"index": 1, "title": "Arc 1", "goal": "Goal",
				"chapters": []map[string]any{
					{"title": "First Chapter", "core_event": "Beginning", "hook": "Continue"},
					{"title": "Second Chapter", "core_event": "Transition", "hook": "End"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}

	// Two chapters written and completed.
	ch2 := "Chapter 2 original body, simulating committed final draft."
	for ch, body := range map[int]string{1: "Chapter 1 body.", 2: ch2} {
		if err := s.Drafts.SaveDraft(ch, body); err != nil {
			t.Fatalf("SaveDraft %d: %v", ch, err)
		}
		if err := s.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter %d: %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, len([]rune(body)), "", ""); err != nil {
			t.Fatalf("MarkChapterComplete %d: %v", ch, err)
		}
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	// Simulate "rework disturbed the long thread": compass still has unresolved open thread
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "protagonist returns home", OpenThreads: []string{"nemesis not eliminated"}}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}

	// reopen Chapter 2 -> rework commit (draft must be different from final draft to release)
	if err := s.Progress.Reopen([]int{2}, "rework"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, ch2+"\n\nRework added paragraph."); err != nil {
		t.Fatalf("SaveDraft reworked: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 2, "summary": "Rework summary", "characters": []string{"protagonist"}, "key_events": []string{"clean"},
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute rework commit: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if bc, _ := out["book_complete"].(bool); !bc {
		t.Error("reopen rework drained should re-complete according to structural completeness (even if long threads are not resolved)")
	}
	p, _ := s.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		t.Errorf("phase = %s, want complete", p.Phase)
	}
	if p.ReopenedFromComplete {
		t.Error("ReopenedFromComplete should be cleared after re-completing")
	}
}

// TestCommitChapterRejectsPolishWithoutDraftChange verifies: after a completed chapter enters the polish/rewrite queue,
// if writer skips draft_chapter and directly commits (drafts and chapters content are identical),
// commit_chapter must reject, forcing the writer to call draft_chapter first to write a new version.
func TestCommitChapterRejectsPolishWithoutDraftChange(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// Simulate Chapter 2 successfully completed: drafts and chapters have the same content.
	original := "Chapter 2 original body text, used to simulate committed final draft."
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	// Enter polish queue: Flow=Polishing, PendingRewrites=[2]
	if err := s.Progress.SetPendingRewrites([]int{2}, "test polish"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"summary":    "pretended to polish",
		"characters": []string{"protagonist"},
		"key_events": []string{"no changes"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected commit to be rejected when drafts equals final content")
	}

	// Write another version with different draft -> should pass
	polished := original + "\n\nPolished added paragraph."
	if err := s.Drafts.SaveDraft(2, polished); err != nil {
		t.Fatalf("SaveDraft (polished): %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute after real polish: %v", err)
	}
}

// TestCommitChapterLayeredRejectsOutOfRangeChapter verifies that in layered mode,
// commits with chapter numbers exceeding the layered_outline must fail hard, instead of warning and letting it pass.
// This is the physical brake to prevent "writer running wild after misjudgment" (the case of Fangu ch204..347).
func TestCommitChapterLayeredRejectsOutOfRangeChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// Build a layered_outline with only 1 volume, 1 arc, 1 chapter
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Volume 1", "theme": "Theme",
			"arcs": []map[string]any{{
				"index": 1, "title": "Arc 1", "goal": "Goal",
				"chapters": []map[string]any{
					{"title": "First Chapter", "core_event": "Beginning", "hook": "Continue"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	// Out-of-bounds chapter 2 commit must fail hard
	if err := s.Drafts.SaveDraft(2, "Out-of-bounds chapter content, must be blocked."); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"summary":    "out-of-bounds chapter",
		"characters": []string{"protagonist"},
		"key_events": []string{"should not be allowed"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected commit to fail when chapter out of layered outline range")
	}

	// Chapter files should not be persisted, Progress should not advance
	if _, statErr := os.Stat(dir + "/chapters/02.md"); !os.IsNotExist(statErr) {
		t.Fatalf("chapter 2 should not be persisted, stat err=%v", statErr)
	}
	progress, _ := s.Progress.Load()
	if len(progress.CompletedChapters) != 0 {
		t.Fatalf("CompletedChapters should stay empty, got %v", progress.CompletedChapters)
	}
}

// TestCommitChapterLayeredAutoCompletesWhenDone verifies the deterministic completion fallback in layered mode:
// When the outline is fully expanded and written + no skeleton arcs + no rework + active foreshadowing is zero + compass long threads are closed,
// committing the last chapter automatically pushes Phase=Complete, without depending on the architect to explicitly call complete_book.
// This fixes the livelock introduced after layered auto-completion was deleted in 9bf26a5 (where the model neither appends nor completes at the end of the final volume -> writer running wild in out-of-bounds loop).
func TestCommitChapterLayeredAutoCompletesWhenDone(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// One volume, one arc, two chapters, all expanded (no skeleton arcs)
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Volume 1", "theme": "Theme",
			"arcs": []map[string]any{{
				"index": 1, "title": "Arc 1", "goal": "Goal",
				"chapters": []map[string]any{
					{"title": "First Chapter", "core_event": "Beginning", "hook": "Continue"},
					{"title": "Second Chapter", "core_event": "Transition", "hook": "End"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}
	// Compass long threads are closed (OpenThreads is empty)
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "protagonist returns home"}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	tool := NewCommitChapterTool(s)
	commit := func(ch int) map[string]any {
		if err := s.Drafts.SaveDraft(ch, fmt.Sprintf("Chapter %d body content, used for testing deterministic completion.", ch)); err != nil {
			t.Fatalf("SaveDraft %d: %v", ch, err)
		}
		args, _ := json.Marshal(map[string]any{
			"chapter": ch, "summary": "summary", "characters": []string{"protagonist"}, "key_events": []string{"event"},
		})
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute ch%d: %v", ch, err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("Unmarshal ch%d: %v", ch, err)
		}
		return out
	}

	// Chapter 1: not fully written, should not complete
	if bc, _ := commit(1)["book_complete"].(bool); bc {
		t.Fatal("Writing Chapter 1 should not trigger completion")
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("Writing Chapter 1 phase should not be complete")
	}

	// Chapter 2 (last chapter): should auto-complete
	if bc, _ := commit(2)["book_complete"].(bool); !bc {
		t.Fatal("Writing the last chapter should auto-complete")
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseComplete {
		t.Fatalf("expected phase=complete, got %s", p.Phase)
	}
}

// TestCommitChapterLayeredNoAutoCompleteWithOpenThreads verifies conservatism: when there are still active long threads,
// do not auto-complete even if chapters are fully written, leaving the decision of "whether to continue" to the architect.
func TestCommitChapterLayeredNoAutoCompleteWithOpenThreads(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Volume 1", "theme": "Theme",
			"arcs": []map[string]any{{
				"index": 1, "title": "Arc 1", "goal": "Goal",
				"chapters": []map[string]any{{"title": "First Chapter", "core_event": "Beginning", "hook": "Continue"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}
	// There are still unresolved active long threads
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "protagonist returns home", OpenThreads: []string{"nemesis not eliminated"}}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	if err := s.Drafts.SaveDraft(1, "Body of the only chapter, but long threads are not resolved."); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "summary": "summary", "characters": []string{"protagonist"}, "key_events": []string{"event"},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("Should not auto-complete when active long threads are unresolved")
	}
}
