package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// CommitChapterTool commits a chapter: load body -> save final draft -> generate summary -> update state -> update progress.
type CommitChapterTool struct {
	store     *store.Store
	rulesOpts rules.LoadOptions // Optional; no rule_violations are generated when LoadOptions is empty
}

func NewCommitChapterTool(store *store.Store) *CommitChapterTool {
	return &CommitChapterTool{store: store}
}

// WithRules injects user rule loading options, so that rule_violations includes the results of checking user rules.
// When this method is not called, only the built-in baseline Lint (mechanical residue checks, always enabled) is executed.
func (t *CommitChapterTool) WithRules(opts rules.LoadOptions) *CommitChapterTool {
	t.rulesOpts = opts
	return t
}

// commitOutput embeds extension fields on top of domain.CommitResult, keeping the domain package independent of rules.
// Since the embedded fields will be promoted by the JSON marshaler, the serialized result is equivalent to a flat structure.
type commitOutput struct {
	domain.CommitResult
	RuleViolations []rules.Violation `json:"rule_violations,omitempty"`
}

func (t *CommitChapterTool) Name() string { return "commit_chapter" }
func (t *CommitChapterTool) Description() string {
	return "Commits chapter final draft. Loads draft text to save as final draft, and updates timeline, foreshadowing, relationships, character states, and progress." +
		"Returns structured facts: next_chapter / review_required / arc_end / volume_end / needs_expansion / book_complete / flow etc."
}
func (t *CommitChapterTool) Label() string { return "Commit Chapter" }

// Write tool (cross-domain atomic operation: draft -> final draft -> summary -> progress -> checkpoint), concurrent execution forbidden.
func (t *CommitChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *CommitChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *CommitChapterTool) Schema() map[string]any {
	timelineSchema := schema.Object(
		schema.Property("time", schema.String("In-story time")).Required(),
		schema.Property("event", schema.String("Event description")).Required(),
		schema.Property("characters", schema.Array("Characters involved", schema.String(""))),
	)
	foreshadowSchema := schema.Object(
		schema.Property("id", schema.String("Foreshadowing ID")).Required(),
		schema.Property("action", schema.Enum("Action", "plant", "advance", "resolve")).Required(),
		schema.Property("description", schema.String("Foreshadowing description (only required when planting)")),
	)
	relationshipSchema := schema.Object(
		schema.Property("character_a", schema.String("Character A")).Required(),
		schema.Property("character_b", schema.String("Character B")).Required(),
		schema.Property("relation", schema.String("Current relationship description")).Required(),
	)
	stateChangeSchema := schema.Object(
		schema.Property("entity", schema.String("Character or entity name")).Required(),
		schema.Property("field", schema.String("Changed attribute")).Required(),
		schema.Property("old_value", schema.String("Value before change")),
		schema.Property("new_value", schema.String("Value after change")).Required(),
		schema.Property("reason", schema.String("Reason for change")),
	)
	feedbackSchema := schema.Object(
		schema.Property("deviation", schema.String("Description of deviation from outline")).Required(),
		schema.Property("suggestion", schema.String("Suggested adjustments to subsequent outline")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Chapter number")).Required(),
		schema.Property("summary", schema.String("Chapter summary (within 200 characters)")).Required(),
		schema.Property("characters", schema.Array("Names of characters appearing in this chapter", schema.String(""))).Required(),
		schema.Property("key_events", schema.Array("Key events in this chapter", schema.String(""))).Required(),
		schema.Property("timeline_events", schema.Array("Timeline events in this chapter", timelineSchema)),
		schema.Property("foreshadow_updates", schema.Array("Foreshadowing actions", foreshadowSchema)),
		schema.Property("relationship_changes", schema.Array("Relationship changes", relationshipSchema)),
		schema.Property("state_changes", schema.Array("Character/entity state changes", stateChangeSchema)),
		schema.Property("cast_intros", schema.Array("Introductions of minor characters introduced for the first time in this chapter who may reappear later (excluding main characters and characters already in characters.json)", schema.Object(
			schema.Property("name", schema.String("Character name")).Required(),
			schema.Property("brief_role", schema.String("One-line role positioning (e.g. innkeeper/gambling house thug)")).Required(),
		))),
		schema.Property("hook_type", schema.Enum("End of chapter hook type", "crisis", "mystery", "desire", "emotion", "choice")),
		schema.Property("dominant_strand", schema.Enum("Dominant narrative strand of this chapter", "quest", "fire", "constellation")),
		schema.Property("feedback", feedbackSchema),
	)
}

func (t *CommitChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter             int                        `json:"chapter"`
		Summary             string                     `json:"summary"`
		Characters          []string                   `json:"characters"`
		KeyEvents           []string                   `json:"key_events"`
		TimelineEvents      []domain.TimelineEvent     `json:"timeline_events"`
		ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates"`
		RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes"`
		StateChanges        []domain.StateChange       `json:"state_changes"`
		CastIntros          []domain.CastIntro         `json:"cast_intros"`
		HookType            string                     `json:"hook_type"`
		DominantStrand      string                     `json:"dominant_strand"`
		Feedback            *domain.OutlineFeedback    `json:"feedback"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		// Clear potentially leftover PendingCommit (crash occurred after ProgressMarked and before ClearPendingCommit)
		if pending, _ := t.store.Signals.LoadPendingCommit(); pending != nil && pending.Chapter == a.Chapter {
			_ = t.store.Signals.ClearPendingCommit()
		}
		// Polish/rewrite path: chapter is completed but still in pending_rewrites, allowed to overwrite and drain the queue
		progress, _ := t.store.Progress.Load()
		if progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter) {
			return t.executeRewriteCommit(a.Chapter, a.Summary, a.Characters, a.KeyEvents,
				a.HookType, a.DominantStrand, progress)
		}
		return t.buildSkipResult(a.Chapter, progress)
	}
	existingPending, err := t.store.Signals.LoadPendingCommit()
	if err != nil {
		return nil, fmt.Errorf("load pending commit: %w: %w", errs.ErrStoreRead, err)
	}
	if existingPending != nil && existingPending.Chapter != a.Chapter {
		return nil, fmt.Errorf("unrecovered chapter commit exists: chapter %d (stage %s), please restore or resubmit this chapter first: %w", existingPending.Chapter, existingPending.Stage, errs.ErrToolConflict)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		// Queue conflicts remain as-is (already categorized with ErrToolConflict); other IO errors default to Precondition.
		if errors.Is(err, errs.ErrToolConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("chapter is currently not allowed to be committed: %w: %w", errs.ErrToolPrecondition, err)
	}

	// Layered mode out-of-bounds interception: must happen before any write operation, otherwise out-of-bounds commit will corrupt chapter files, summaries,
	// and progress. boundary is reused for calculating the arc/volume signal in step 6b below.
	var boundary *store.ArcBoundary
	if progress, perr := t.store.Progress.Load(); perr == nil && progress != nil && progress.Layered {
		b, bErr := t.store.Outline.CheckArcBoundary(a.Chapter)
		if bErr != nil {
			return nil, fmt.Errorf("arc boundary check failed chapter=%d: %w: %w", a.Chapter, errs.ErrStoreRead, bErr)
		}
		if b == nil {
			return nil, fmt.Errorf(
				"chapter %d is not within the layered outline scope: writing must first expand_arc or append_volume; if the entire book is finished, please call save_foundation type=complete_book: %w",
				a.Chapter, errs.ErrToolPrecondition)
		}
		boundary = b
	}

	// 1. Load chapter body
	content, wordCount, err := t.store.Drafts.LoadChapterContent(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}

	now := time.Now().Format(time.RFC3339)
	pending := domain.PendingCommit{
		Chapter:        a.Chapter,
		Stage:          domain.CommitStageStarted,
		Summary:        a.Summary,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		StartedAt:      now,
		UpdatedAt:      now,
	}
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("save pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 2. Save final draft
	if err := t.store.Drafts.SaveFinalChapter(a.Chapter, content); err != nil {
		return nil, fmt.Errorf("save final chapter: %w: %w", errs.ErrStoreWrite, err)
	}

	// 3. Save summary
	summary := domain.ChapterSummary{
		Chapter:    a.Chapter,
		Summary:    a.Summary,
		Characters: a.Characters,
		KeyEvents:  a.KeyEvents,
	}
	if err := t.store.Summaries.SaveSummary(summary); err != nil {
		return nil, fmt.Errorf("save summary: %w: %w", errs.ErrStoreWrite, err)
	}

	// 4. Update status increments
	if len(a.TimelineEvents) > 0 {
		for i := range a.TimelineEvents {
			a.TimelineEvents[i].Chapter = a.Chapter
		}
		if err := t.store.World.AppendTimelineEvents(a.TimelineEvents); err != nil {
			return nil, fmt.Errorf("append timeline: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.ForeshadowUpdates) > 0 {
		if err := t.store.World.UpdateForeshadow(a.Chapter, a.ForeshadowUpdates); err != nil {
			return nil, fmt.Errorf("update foreshadow: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.RelationshipChanges) > 0 {
		for i := range a.RelationshipChanges {
			a.RelationshipChanges[i].Chapter = a.Chapter
		}
		if err := t.store.World.UpdateRelationships(a.RelationshipChanges); err != nil {
			return nil, fmt.Errorf("update relationships: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.StateChanges) > 0 {
		for i := range a.StateChanges {
			a.StateChanges[i].Chapter = a.Chapter
		}
		if err := t.store.World.AppendStateChanges(a.StateChanges); err != nil {
			return nil, fmt.Errorf("append state changes: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 4b. Accumulate supporting characters roster: non-core characters appearing in this chapter go to cast_ledger for recall by novel_context.
	// When failing, only warn and do not block commit — the roster is secondary data and can heal itself via the next chapter's commit.
	if len(a.Characters) > 0 {
		coreNames := loadCoreCharacterNameSet(t.store)
		if err := t.store.Cast.MergeAppearances(a.Chapter, a.Characters, a.CastIntros, coreNames); err != nil {
			slog.Warn("Failed to accumulate supporting characters roster, skipping", "module", "commit", "chapter", a.Chapter, "err", err)
		}
	}

	pending.Stage = domain.CommitStageStateApplied
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 5. Update progress
	if err := t.store.Progress.MarkChapterComplete(a.Chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
		return nil, fmt.Errorf("mark chapter complete: %w: %w", errs.ErrStoreWrite, err)
	}

	// 6. Determine whether review is required
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	completedCount := 0
	if progress != nil {
		completedCount = len(progress.CompletedChapters)
	}

	// 6b. Long-form mode arc/volume signals: boundary is validated at entry, guaranteed non-nil when Layered
	var arcEnd, volumeEnd, needsExpansion, needsNewVolume bool
	var vol, arc, nextVol, nextArc int
	if progress != nil && progress.Layered && boundary != nil {
		arcEnd = boundary.IsArcEnd
		volumeEnd = boundary.IsVolumeEnd
		vol = boundary.Volume
		arc = boundary.Arc
		needsExpansion = boundary.NeedsExpansion
		needsNewVolume = boundary.NeedsNewVolume
		nextVol = boundary.NextVolume
		nextArc = boundary.NextArc
		_ = t.store.Progress.UpdateVolumeArc(vol, arc)
	}

	var reviewRequired bool
	var reviewReason string
	if progress != nil && progress.Layered {
		reviewRequired, reviewReason = domain.ShouldArcReview(arcEnd, volumeEnd, vol, arc)
	} else {
		reviewRequired, reviewReason = domain.ShouldReview(completedCount)
	}

	// 7. Construct structured signals
	result := domain.CommitResult{
		Chapter:        a.Chapter,
		Committed:      true,
		WordCount:      wordCount,
		NextChapter:    a.Chapter + 1,
		ReviewRequired: reviewRequired,
		ReviewReason:   reviewReason,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		Feedback:       a.Feedback,
		ArcEnd:         arcEnd,
		VolumeEnd:      volumeEnd,
		Volume:         vol,
		Arc:            arc,
		NeedsExpansion: needsExpansion,
		NeedsNewVolume: needsNewVolume,
		NextVolume:     nextVol,
		NextArc:        nextArc,
	}

	// 8. Completion determination: non-layered finishes last chapter / layered final volume last chapter -> MarkComplete
	if t.applyCompletion(&result, progress) {
		result.BookComplete = true
	}
	if p, _ := t.store.Progress.Load(); p != nil {
		result.Flow = string(p.Flow)
	}

	pending.Stage = domain.CommitStageProgressMarked
	pending.Result = &result
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit result: %w: %w", errs.ErrStoreWrite, err)
	}

	// 9. Clear progress intermediate state
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 10. Append checkpoint
	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 11. Mechanical rules check (only returns facts, does not block)
	violations := t.checkRules(content, wordCount)
	return json.Marshal(commitOutput{CommitResult: result, RuleViolations: violations})
}

// checkRules performs mechanical checks on the chapter text: built-in product baseline Lint (mechanical residue, always executed)
// + user rules Check (when rulesOpts is completely empty, loader returns empty layers, checker returns nil).
func (t *CommitChapterTool) checkRules(text string, wordCount int) []rules.Violation {
	violations := rules.Lint(text)
	bundle := rules.Merge(rules.Load(t.rulesOpts))
	return append(violations, rules.Check(text, wordCount, bundle.Structured)...)
}

// executeRewriteCommit handles the commit of polished/rewritten chapters: overwrites final draft and summary, updates word count, drains the queue.
// Skip all world state appends (timeline / foreshadow / relationship / state_changes) and arc boundary detection,
// these have already been applied during the chapter's original commit.
func (t *CommitChapterTool) executeRewriteCommit(
	chapter int,
	summary string,
	characters, keyEvents []string,
	hookType, dominantStrand string,
	progress *domain.Progress,
) (json.RawMessage, error) {
	// 1. Load polished body
	content, wordCount, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return nil, fmt.Errorf("rewrite: load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", chapter, errs.ErrToolPrecondition)
	}

	// 2. Hard validation: drafts is identical to the current final draft -> determined as not really polished/rewritten (writer skipped draft_chapter)
	// Reject commit, forcing the writer to call draft_chapter(mode=write) first to write a new version.
	existingFinal, _ := t.store.Drafts.LoadChapterText(chapter)
	if existingFinal != "" && existingFinal == content {
		mode := "rewrite"
		if progress != nil && progress.Flow == domain.FlowPolishing {
			mode = "polish"
		}
		return nil, fmt.Errorf("chapter %d drafts and chapters contents are identical, no %s changes detected. Please call draft_chapter(mode=write, chapter=%d) first to write the new %s text before calling commit_chapter: %w",
			chapter, mode, chapter, mode, errs.ErrToolPrecondition)
	}

	// 3. Overwrite final draft
	if err := t.store.Drafts.SaveFinalChapter(chapter, content); err != nil {
		return nil, fmt.Errorf("rewrite: save final chapter: %w: %w", errs.ErrStoreWrite, err)
	}

	// 3. Overwrite summary
	if err := t.store.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    chapter,
		Summary:    summary,
		Characters: characters,
		KeyEvents:  keyEvents,
	}); err != nil {
		return nil, fmt.Errorf("rewrite: save summary: %w: %w", errs.ErrStoreWrite, err)
	}

	// 4. Update word count (MarkChapterComplete is idempotent for completed chapters: replaces word count, slice.Contains prevents duplicate queuing)
	if err := t.store.Progress.MarkChapterComplete(chapter, wordCount, hookType, dominantStrand); err != nil {
		return nil, fmt.Errorf("rewrite: update word count: %w: %w", errs.ErrStoreWrite, err)
	}

	// 5. Drain the pending queue; when the queue is empty, CompleteRewrite will automatically switch flow back to writing
	if err := t.store.Progress.CompleteRewrite(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: complete rewrite: %w: %w", errs.ErrStoreWrite, err)
	}

	// 6. Checkpoint
	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
	); err != nil {
		return nil, fmt.Errorf("rewrite: checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 7. Read the Progress snapshot after draining, and return it as fact
	mode := "rewrite"
	if progress.Flow == domain.FlowPolishing {
		mode = "polish"
	}
	latest, _ := t.store.Progress.Load()
	remaining := []int{}
	nextChapter := chapter + 1
	flow := string(domain.FlowWriting)
	if latest != nil {
		remaining = append(remaining, latest.PendingRewrites...)
		nextChapter = latest.NextChapter()
		flow = string(latest.Flow)
	}
	drained := len(remaining) == 0

	// Determine completion after queue is cleared: rework commits do not go through the main path applyCompletion, completion can only be triggered here.
	//   - Layered + forward writing: use quality-level layeredBookComplete (requires threads to close), yield to architect if not met.
	//   - Layered + reopen rework (ReopenedFromComplete): rework only changes existing chapters, does not add/remove structure, complete structurally ->
	//     i.e. re-complete — if stuck in writing because rework disturbed some thread, the end of the final volume will fall into an out-of-bounds writing infinite loop.
	//   - Non-layered: finishes when CompletedChapters reaches TotalChapters (rework does not add/remove chapters, originally full).
	bookComplete := false
	if drained && latest != nil {
		reComplete := false
		switch {
		case latest.Layered && latest.ReopenedFromComplete:
			reComplete = t.layeredStructurallyComplete(latest)
		case latest.Layered:
			reComplete = t.layeredBookComplete(latest)
		default:
			reComplete = latest.TotalChapters > 0 && len(latest.CompletedChapters) >= latest.TotalChapters
		}
		if reComplete {
			if cerr := t.store.Progress.MarkComplete(); cerr == nil {
				bookComplete = true
				if p, _ := t.store.Progress.Load(); p != nil {
					flow = string(p.Flow)
				}
			}
		}
	}

	// Same as main path: rewrite/polish also perform mechanical checks and attach rule_violations
	violations := t.checkRules(content, wordCount)
	return json.Marshal(map[string]any{
		"chapter":         chapter,
		"rewritten":       true,
		"mode":            mode,
		"word_count":      wordCount,
		"remaining_queue": remaining,
		"queue_drained":   drained,
		"next_chapter":    nextChapter,
		"flow":            flow,
		"book_complete":   bookComplete,
		"rule_violations": violations,
	})
}

// buildSkipResult constructs a fact return aligned with normal commit for "duplicate submission of completed chapters".
// Coordinator uses this to make subsequent decisions (writer/editor/architect dispatch) without hallucinating due to receiving prose prompts.
func (t *CommitChapterTool) buildSkipResult(chapter int, progress *domain.Progress) (json.RawMessage, error) {
	_, wordCount, _ := t.store.Drafts.LoadChapterContent(chapter)

	result := domain.CommitResult{
		Chapter:     chapter,
		Committed:   true,
		WordCount:   wordCount,
		NextChapter: chapter + 1,
	}

	if progress != nil && progress.Layered {
		if boundary, _ := t.store.Outline.CheckArcBoundary(chapter); boundary != nil {
			result.ArcEnd = boundary.IsArcEnd
			result.VolumeEnd = boundary.IsVolumeEnd
			result.Volume = boundary.Volume
			result.Arc = boundary.Arc
			result.NeedsExpansion = boundary.NeedsExpansion
			result.NeedsNewVolume = boundary.NeedsNewVolume
			result.NextVolume = boundary.NextVolume
			result.NextArc = boundary.NextArc
		}
		result.ReviewRequired, result.ReviewReason = domain.ShouldArcReview(result.ArcEnd, result.VolumeEnd, result.Volume, result.Arc)
	} else if progress != nil {
		result.ReviewRequired, result.ReviewReason = domain.ShouldReview(len(progress.CompletedChapters))
	}

	if progress != nil {
		if progress.Phase == domain.PhaseComplete {
			result.BookComplete = true
		}
		result.Flow = string(progress.Flow)
	}

	return json.Marshal(result)
}

// loadCoreCharacterNameSet loads the set of existing character names (including aliases) in characters.json.
// Used as the "known core" filter set for cast_ledger — core characters do not enter the secondary roster.
// When loading fails, returns nil (all characters enter the ledger during merge, which is acceptable).
func loadCoreCharacterNameSet(s *store.Store) map[string]bool {
	chars, err := s.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	set := make(map[string]bool, len(chars)*2)
	for _, c := range chars {
		if c.Name != "" {
			set[c.Name] = true
		}
		for _, alias := range c.Aliases {
			if alias != "" {
				set[alias] = true
			}
		}
	}
	return set
}

// applyCompletion determines whether this commit finishes the entire book; if so, calls MarkComplete and returns true.
//   - Non-layered: finishes when the agreed total chapters are written.
//   - Layered: architect explicitly calls save_foundation type=complete_book is the main path; here we add an
//     deterministic fallback — when the entire book objectively meets the completion conditions (see layeredBookComplete), automatically close.
//     Prevents model from neither appending volume nor calling complete_book at the end, leading to "writer running out-of-bounds chapter ->
//     out-of-bounds guard intercepting -> infinite retry" livelock (root cause of cases like Fangu ch204..347).
func (t *CommitChapterTool) applyCompletion(result *domain.CommitResult, progress *domain.Progress) bool {
	if progress == nil {
		return false
	}
	if progress.Layered {
		if t.layeredBookComplete(progress) {
			_ = t.store.Progress.MarkComplete()
			return true
		}
		return false
	}
	if progress.TotalChapters > 0 && result.NextChapter > progress.TotalChapters {
		_ = t.store.Progress.MarkComplete()
		return true
	}
	return false
}

// layeredStructurallyComplete determines if a layered long-form is "structurally complete": rework queue empty + no skeleton arcs to expand
// + all expanded chapters are written. This is a deterministic final state fact, without semantic judgments like foreshadowing/long threads — used as a "prevent loop in final state" safety net
// (rework is re-completed after being cleared based on this).
func (t *CommitChapterTool) layeredStructurallyComplete(progress *domain.Progress) bool {
	// 1. Rework queue must be empty
	if len(progress.PendingRewrites) > 0 {
		return false
	}
	volumes, err := t.store.Outline.LoadLayeredOutline()
	if err != nil || len(volumes) == 0 {
		return false
	}
	// 2. Cannot have skeleton arcs waiting to be expanded (still content planned to be written)
	for i := range volumes {
		for j := range volumes[i].Arcs {
			if !volumes[i].Arcs[j].IsExpanded() {
				return false
			}
		}
	}
	// 3. All expanded chapters must be fully written
	expanded := len(domain.FlattenOutline(volumes))
	return expanded > 0 && len(progress.CompletedChapters) >= expanded
}

// layeredBookComplete determines if a layered long-form is truly finished using objective facts, referencing the completion list in architect-long.md
// with quantifiable items + structural facts. In addition to structural completeness, it requires active foreshadowing to be zero and long threads to close — if any is not met,
// yield to architect to continue expand_arc / append_volume, never closing before the story is finished. Conservative when no compass
// is present, judging as unfinished. This is a "quality-level" completion judgment for forward writing, stricter than layeredStructurallyComplete.
func (t *CommitChapterTool) layeredBookComplete(progress *domain.Progress) bool {
	if !t.layeredStructurallyComplete(progress) {
		return false
	}
	// 4. Active foreshadowing must be zero (commitments resolved)
	if active, aerr := t.store.World.LoadActiveForeshadow(); aerr != nil || len(active) > 0 {
		return false
	}
	// 5. Compass active long threads must be closed (handed over to architect if compass missing / long threads open)
	compass, cerr := t.store.Outline.LoadCompass()
	if cerr != nil || compass == nil || len(compass.OpenThreads) > 0 {
		return false
	}
	return true
}
