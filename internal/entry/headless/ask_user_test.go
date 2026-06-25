package headless

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/tools"
)

func TestTerminalAskUserSingleSelect(t *testing.T) {
	handler := newTerminalAskUser(strings.NewReader("2\n"), &strings.Builder{})
	resp, err := handler.handle(context.Background(), []tools.Question{
		{
			Question: "What style do you want?",
			Header:   "Style",
			Options: []tools.Option{
				{Label: "Hot-blooded", Description: "Focus on leveling up"},
				{Label: "Suspense", Description: "Focus on mystery"},
			},
		},
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := resp.Answers["What style do you want?"]; got != "Suspense" {
		t.Fatalf("unexpected answer: %q", got)
	}
}

func TestTerminalAskUserCustomInput(t *testing.T) {
	handler := newTerminalAskUser(strings.NewReader("0\nNo romance\n"), &strings.Builder{})
	resp, err := handler.handle(context.Background(), []tools.Question{
		{
			Question: "Any other restrictions?",
			Header:   "Restrictions",
			Options: []tools.Option{
				{Label: "Dark", Description: "Depressing tone"},
				{Label: "Lighthearted", Description: "Cheerful tone"},
			},
		},
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := resp.Answers["Any other restrictions?"]; got != "Custom" {
		t.Fatalf("unexpected answer: %q", got)
	}
	if got := resp.Notes["Any other restrictions?"]; got != "No romance" {
		t.Fatalf("unexpected note: %q", got)
	}
}
