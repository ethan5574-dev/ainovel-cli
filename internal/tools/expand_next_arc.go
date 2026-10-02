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

// ExpandNextArcTool expands the skeleton following the current completed arc into detailed chapters.
type ExpandNextArcTool struct {
	store *store.Store
}

func NewExpandNextArcTool(store *store.Store) *ExpandNextArcTool {
	return &ExpandNextArcTool{store: store}
}

func (t *ExpandNextArcTool) Name() string  { return "expand_next_arc" }
func (t *ExpandNextArcTool) Label() string { return "Expand next arc" }
func (t *ExpandNextArcTool) Description() string {
	return "Expand the next skeleton arc after the completed arc. The system selects its volume/arc from progress and outline; submit only title, goal, and chapters calibrated to completed facts."
}

func (t *ExpandNextArcTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ExpandNextArcTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ExpandNextArcTool) StrictSchema() bool                   { return true }

func (t *ExpandNextArcTool) Schema() map[string]any {
	chapter := schema.Object(
		schema.Property("title", schema.String("Chapter title")).Required(),
		schema.Property("core_event", schema.String("Chapter core event")).Required(),
		schema.Property("hook", schema.String("Chapter-ending hook")).Required(),
		schema.Property("scenes", schema.Array("Planned scenes; empty array if none", schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("title", schema.String("Arc title calibrated to completed facts")).Required(),
		schema.Property("goal", schema.String("Arc goal calibrated to completed facts")).Required(),
		schema.Property("chapters", schema.Array("Detailed chapter plans for this arc", chapter)).Required(),
	)
}

func (t *ExpandNextArcTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var expansion domain.ArcExpansion
	if err := json.Unmarshal(args, &expansion); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	position, err := t.store.ExpandNextArc(expansion)
	if err != nil {
		return nil, fmt.Errorf("expand next arc: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := consumeWriterFeedback(t.store); err != nil {
		return nil, err
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.ArcScope(position.Volume, position.Arc), t.Name(), "layered_outline.json"); err != nil {
		return nil, fmt.Errorf("checkpoint %s: %w: %w", t.Name(), errs.ErrStoreWrite, err)
	}
	return json.Marshal(map[string]any{
		"saved":    true,
		"type":     t.Name(),
		"volume":   position.Volume,
		"arc":      position.Arc,
		"title":    expansion.Title,
		"goal":     expansion.Goal,
		"chapters": len(expansion.Chapters),
	})
}
