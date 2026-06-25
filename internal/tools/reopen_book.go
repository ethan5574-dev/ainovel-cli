package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ReopenBookTool reopens a completed book to rework state (held by Coordinator only).
// After completion, completePhaseGate hard blocks all subagent dispatches, so users cannot rework written chapters.
// This tool is not a subagent, and can be called during complete phase: it atomically switches phase back to writing,
// puts target chapters into PendingRewrites and sets flow=rewriting. The Flow Router then dispatches writers to rewrite chapter by chapter.
// Once the queue is finished, commit_chapter will automatically wrap up and complete the book again. Gate / Router / edit / commit heavy logic does not need to change.
type ReopenBookTool struct {
	store *store.Store
}

func NewReopenBookTool(s *store.Store) *ReopenBookTool {
	return &ReopenBookTool{store: s}
}

func (t *ReopenBookTool) Name() string  { return "reopen_book" }
func (t *ReopenBookTool) Label() string { return "Reopen Rework" }

func (t *ReopenBookTool) Description() string {
	return "Reopens a completed book (phase=complete) into rework state, used when the user wants to rewrite/polish certain chapters after completion. " +
		"chapters are the completed chapter numbers to rework; after calling, these chapters will enter the rewrite queue, and the Host will dispatch writers to rewrite them chapter by chapter, automatically completing the book again once all are done. " +
		"Use only when the book is completed and the user explicitly requests modification of written chapters; adding new plot/expanding scope is not rework, do not use this tool."
}

// Write tool, concurrency disabled.
func (t *ReopenBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *ReopenBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *ReopenBookTool) ActivityDescription(_ json.RawMessage) string { return "Reopening book for rework" }

func (t *ReopenBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapters", schema.Array("List of completed chapter numbers to rework (at least one)", schema.Int(""))).Required(),
		schema.Property("reason", schema.String("Reason for rework (optional, e.g. \"cleanup of special characters\")")),
	)
}

func (t *ReopenBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapters []int  `json:"chapters"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if len(a.Chapters) == 0 {
		return nil, fmt.Errorf("chapters cannot be empty, must specify chapters to rework: %w", errs.ErrToolArgs)
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress not initialized: %w", errs.ErrToolPrecondition)
	}
	// Can only rework written chapters; chapter numbers not in the completed set are additions/out-of-bounds, refuse and guide user to adjust volume size.
	var invalid []int
	for _, ch := range a.Chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("chapter %v has not been written yet, reopen can only rework completed chapters (please use size adjustment to add/extend plots): %w", invalid, errs.ErrToolPrecondition)
	}

	// Phase pre-check is handled in store.Reopen (only callable when phase is complete).
	if err := t.store.Progress.Reopen(a.Chapters, a.Reason); err != nil {
		return nil, fmt.Errorf("reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint: symmetric with complete_book (GlobalScope + meta/progress.json).
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return nil, fmt.Errorf("checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"reopened":         true,
		"phase":            string(domain.PhaseWriting),
		"pending_rewrites": a.Chapters,
		"next_step":        "Reopened successfully and target chapters queued. Please wait for Host to dispatch writer for rewriting chapter by chapter; it will automatically complete again once all are done.",
	})
}
