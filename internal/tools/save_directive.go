package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveDirectiveTool persists user's long-term creative instructions (held by Coordinator only).
// Saved to meta/user_directives.json, injected into working_memory.user_directives by novel_context,
// so all subagents automatically see it in each chapter—no need for Coordinator to manually convey it during dispatch, persisting across compression and restarts.
type SaveDirectiveTool struct {
	store *store.Store
}

func NewSaveDirectiveTool(s *store.Store) *SaveDirectiveTool {
	return &SaveDirectiveTool{store: s}
}

func (t *SaveDirectiveTool) Name() string  { return "save_directive" }
func (t *SaveDirectiveTool) Label() string { return "Save Long-term Directive" }

func (t *SaveDirectiveTool) Description() string {
	return "Persists user's long-term creative instructions (e.g., \"increase dialogue ratio from now on\", \"use only English for chapter titles\"). " +
		"Once saved, all subagents will see it in working_memory.user_directives in every chapter, no need to manually pass it down. " +
		"action=add appends a new directive (text is required, preserving user intent, optionally condensed); " +
		"action=remove deletes a directive by its index (index is required, see index in previous list response). " +
		"Returns the updated full list. Save only state-based instructions (descriptions that hold true whenever read); " +
		"relative/actionable instructions (e.g. \"add 10 chapters\") are forbidden—this tool does not dispatch subagents, saving them does not mean execution, please route directly to subagents for immediate handling."
}

// Write tool, concurrency disabled.
func (t *SaveDirectiveTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveDirectiveTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveDirectiveTool) ActivityDescription(_ json.RawMessage) string { return "Saving long-term directive" }

func (t *SaveDirectiveTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("action", schema.Enum("Action type", "add", "remove")).Required(),
		schema.Property("text", schema.String("Directive content (required for add): one sentence stating the requirement, preserving user intent")),
		schema.Property("index", schema.Int("Index of entry to remove (required for remove, 1-based, see the returned index in the list)")),
	)
}

func (t *SaveDirectiveTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Action string `json:"action"`
		Text   string `json:"text"`
		Index  int    `json:"index"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}

	var (
		list []domain.UserDirective
		err  error
	)
	switch a.Action {
	case "add":
		text := strings.TrimSpace(a.Text)
		if text == "" {
			return nil, fmt.Errorf("add requires non-empty text: %w", errs.ErrToolArgs)
		}
		chapter, total := 0, 0
		if progress, perr := t.store.Progress.Load(); perr == nil && progress != nil {
			chapter = progress.NextChapter()
			total = progress.TotalChapters
		}
		list, err = t.store.Directives.Add(domain.UserDirective{
			Text:          text,
			Chapter:       chapter,
			TotalChapters: total,
			CreatedAt:     time.Now().Format(time.RFC3339),
		})
	case "remove":
		if a.Index < 1 {
			return nil, fmt.Errorf("remove requires index >= 1: %w", errs.ErrToolArgs)
		}
		list, err = t.store.Directives.Remove(a.Index)
	default:
		return nil, fmt.Errorf("unknown action %q: %w", a.Action, errs.ErrToolArgs)
	}
	if err != nil {
		return nil, err
	}

	items := directiveFacts(list)
	return json.Marshal(map[string]any{
		"saved":      true,
		"directives": items,
		"count":      len(items),
	})
}

// directiveFacts converts long-term directives to fact views for LLM (tool result has the same shape as envelope injection):
// at_* is the progress snapshot when issued—directives take effect from at_chapter onwards, relative expressions can be judged
// based on at_total_chapters to see if satisfied. created_at is audit information and not sent to LLM.
func directiveFacts(list []domain.UserDirective) []map[string]any {
	items := make([]map[string]any, len(list))
	for i, d := range list {
		items[i] = map[string]any{
			"index":             i + 1,
			"text":              d.Text,
			"at_chapter":        d.Chapter,
			"at_total_chapters": d.TotalChapters,
		}
	}
	return items
}
