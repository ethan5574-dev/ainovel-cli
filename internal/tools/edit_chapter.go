package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	agentcoretools "github.com/voocel/agentcore/tools"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// EditChapterTool performs targeted string replacement on chapter drafts, suitable for polishing scenarios.
// Compared to rewriting the entire chapter with draft_chapter, token savings are 10x+.
//
// Write contract: only modify drafts/{ch:02d}.draft.md, direct modification of chapters/ is forbidden (final draft is owned exclusively by commit_chapter).
// Seed semantics: if drafts does not exist but chapters does -> automatically copy chapters to drafts as the starting point.
// Ownership check: when the chapter is completed, it must be in the PendingRewrites queue, otherwise rejected.
//
// This tool is a thin wrapper around agentcore.EditTool; search-and-replace logic (multi-level fault-tolerant matching, diff output, line ending/BOM preservation)
// are all reused from the upstream implementation.
type EditChapterTool struct {
	store *store.Store
	edit  *agentcoretools.EditTool
}

func NewEditChapterTool(s *store.Store) *EditChapterTool {
	return &EditChapterTool{
		store: s,
		edit:  agentcoretools.NewEdit(s.Dir(), nil),
	}
}

func (t *EditChapterTool) Name() string  { return "edit_chapter" }
func (t *EditChapterTool) Label() string { return "Edit Chapter" }

// ReadOnly explicitly declares write tool (cooperates with ConcurrencySafeTool to prevent concurrent scheduling).
func (t *EditChapterTool) ReadOnly(_ json.RawMessage) bool { return false }

// ConcurrencySafe explicitly forbids concurrency: parallel edit_chapter on the same chapter causes read-modify-write race condition,
// and parallel executions across different chapters would interleave checkpoint order. Unified serialization is most stable.
func (t *EditChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

// ActivityDescription supplies current tool activity description for UI/logging.
func (t *EditChapterTool) ActivityDescription(_ json.RawMessage) string { return "Editing chapter draft" }

func (t *EditChapterTool) Description() string {
	return "Performs targeted string replacement on chapter drafts (preferred for polishing, saves tokens compared to draft_chapter rewriting the entire chapter)." +
		"Finds old_string and replaces it with new_string, requiring an exact and unique match (replace_all=true required for multiple matches)." +
		"Writes to drafts/{ch}.draft.md; automatically seeds from chapters when drafts does not exist." +
		"Rejected when the chapter is completed and not in the PendingRewrites queue. Each call only changes one place; call multiple times for multiple modifications."
}

func (t *EditChapterTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("Chapter number")).Required(),
		schema.Property("old_string", schema.String("Exact fragment of original text to be replaced, multi-line must include newlines; must appear uniquely in draft when replace_all is not set")).Required(),
		schema.Property("new_string", schema.String("New text after replacement")).Required(),
		schema.Property("replace_all", schema.Bool("Replace all matches (default false)")),
	)
}

func (t *EditChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int    `json:"chapter"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.OldString == "" {
		return nil, fmt.Errorf("old_string cannot be empty: %w", errs.ErrToolArgs)
	}
	if a.OldString == a.NewString {
		return nil, fmt.Errorf("old_string is identical to new_string, no changes needed: %w", errs.ErrToolArgs)
	}

	// Ownership check: completed chapters must be in the rewrite queue to avoid contaminating final drafts
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		progress, _ := t.store.Progress.Load()
		if progress == nil || !slices.Contains(progress.PendingRewrites, a.Chapter) {
			return nil, fmt.Errorf("chapter %d is completed and not in PendingRewrites queue, cannot edit; to modify, please have the editor review and trigger rewrite/polish first: %w", a.Chapter, errs.ErrToolPrecondition)
		}
	}

	// Seed: copy from chapters as starting point when drafts does not exist
	if err := t.ensureDraft(a.Chapter); err != nil {
		return nil, err
	}

	// Delegate search-and-replace to agentcore.EditTool
	subArgs, _ := json.Marshal(map[string]any{
		"path":        fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"file_path":   fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"old_text":    a.OldString,
		"old_string":  a.OldString,
		"new_text":    a.NewString,
		"new_string":  a.NewString,
		"replace_all": a.ReplaceAll,
	})
	result, err := t.edit.Execute(ctx, subArgs)
	if err != nil {
		return nil, fmt.Errorf("apply edit: %w: %w", errs.ErrToolPrecondition, err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "edit",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint edit: %w: %w", errs.ErrStoreWrite, err)
	}

	// Additional guidance: let the writer know the next steps, avoiding missing check_consistency / commit_chapter
	var passthrough map[string]any
	if err := json.Unmarshal(result, &passthrough); err != nil {
		return result, nil
	}
	passthrough["chapter"] = a.Chapter
	passthrough["next_step"] = "edit has been saved. You can call edit_chapter again if there are still issues; otherwise call commit_chapter after check_consistency"
	return json.Marshal(passthrough)
}

// ensureDraft ensures drafts/{ch}.draft.md exists:
//   - Existing draft -> return directly
//   - No draft but final draft exists -> copy final draft to drafts as modification starting point (common in polishing scenarios)
//   - Neither exists -> return error, prompting to use draft_chapter first to create initial draft
func (t *EditChapterTool) ensureDraft(chapter int) error {
	draft, err := t.store.Drafts.LoadDraft(chapter)
	if err != nil {
		return fmt.Errorf("load draft: %w: %w", errs.ErrStoreRead, err)
	}
	if draft != "" {
		return nil
	}
	text, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return fmt.Errorf("load chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if text == "" {
		return fmt.Errorf("chapter %d has neither draft nor final draft, please call draft_chapter(mode=write, chapter=%d) first to create initial draft: %w", chapter, chapter, errs.ErrToolPrecondition)
	}
	if err := t.store.Drafts.SaveDraft(chapter, text); err != nil {
		return fmt.Errorf("seed draft from chapter: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
