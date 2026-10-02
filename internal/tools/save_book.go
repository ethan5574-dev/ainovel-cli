package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveBookTool saves reader-facing book metadata for Architect.
type SaveBookTool struct{ store *store.Store }

func NewSaveBookTool(store *store.Store) *SaveBookTool { return &SaveBookTool{store: store} }

func (t *SaveBookTool) Name() string { return "save_book" }
func (t *SaveBookTool) Description() string {
	return "Save the official book title and spoiler-free reader synopsis, highlighting protagonist, conflict, and reading hook rather than an internal outline or ending recap."
}
func (t *SaveBookTool) Label() string                          { return "Save book metadata" }
func (t *SaveBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *SaveBookTool) StrictSchema() bool                     { return true }

func (t *SaveBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("title", schema.String("Official title, without surrounding title brackets")).Required(),
		schema.Property("synopsis", schema.String("Spoiler-free reader-facing synopsis")).Required(),
	)
}

func (t *SaveBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var book domain.BookMetadata
	if err := json.Unmarshal(args, &book); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if err := book.Validate(); err != nil {
		return nil, fmt.Errorf("invalid book metadata: %w: %w", errs.ErrToolArgs, err)
	}
	if err := t.store.Book.Save(book); err != nil {
		return nil, fmt.Errorf("save book metadata: %w: %w", errs.ErrStoreWrite, err)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return nil, fmt.Errorf("checkpoint book metadata: %w: %w", errs.ErrStoreWrite, err)
	}
	remaining, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w: %w", errs.ErrStoreRead, err)
	}
	return json.Marshal(map[string]any{
		"saved":            true,
		"foundation_ready": len(remaining) == 0,
		"remaining":        remaining,
	})
}
