package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/store"
)

func TestSaveArcSummaryPersistsStyleRulesDialogueObjects(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveArcSummaryTool(s)
	args, err := json.Marshal(map[string]any{
		"volume":     1,
		"arc":        2,
		"title":      "Entering the Mountain",
		"summary":    "The protagonist completes the mountain trial and confirms the direction of future investigation.",
		"key_events": []string{"Pass the trial", "Discover clues to the old case"},
		"character_snapshots": []map[string]any{
			{"name": "Shen Yuan", "status": "Alive", "motivation": "Investigate the old case"},
		},
		"style_rules": map[string]any{
			"prose": []string{"Priority on tactile and olfactory descriptions in environment", "Action scenes use short sentences to progress", "Psychological description does not explain conclusions"},
			"dialogue": []map[string]any{
				{"name": "Shen Yuan", "rules": []string{"Dialogue is minimalist", "Use fewer questions"}},
			},
			"taboos": []string{"Avoid long monologues at the end of chapters"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rules, err := s.World.LoadStyleRules()
	if err != nil {
		t.Fatalf("LoadStyleRules: %v", err)
	}
	if rules == nil || len(rules.Dialogue) != 1 {
		t.Fatalf("expected one dialogue rule, got %+v", rules)
	}
	if rules.Dialogue[0].Name != "Shen Yuan" || len(rules.Dialogue[0].Rules) != 2 {
		t.Fatalf("unexpected dialogue rule: %+v", rules.Dialogue[0])
	}
}

func TestSaveArcSummaryRejectsDialogueStringArray(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveArcSummaryTool(s)
	args, err := json.Marshal(map[string]any{
		"volume":              1,
		"arc":                 2,
		"title":               "Entering the Mountain",
		"summary":             "The protagonist completes the mountain trial and confirms the direction of future investigation.",
		"key_events":          []string{"Pass the trial"},
		"character_snapshots": []map[string]any{},
		"style_rules": map[string]any{
			"prose":    []string{"Priority on tactile and olfactory descriptions in environment"},
			"dialogue": []string{"Shen Yuan's dialogue is minimalist"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "style_rules.dialogue") {
		t.Fatalf("expected style_rules.dialogue validation error, got %v", err)
	}
}
