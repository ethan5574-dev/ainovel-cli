package agents

import (
	"testing"

	"github.com/voocel/agentcore"
)

func TestParseThinkingLevel(t *testing.T) {
	ok := map[string]agentcore.ThinkingLevel{
		"":        "",
		"off":     agentcore.ThinkingOff,
		"minimal": agentcore.ThinkingMinimal,
		"low":     agentcore.ThinkingLow,
		"medium":  agentcore.ThinkingMedium,
		"high":    agentcore.ThinkingHigh,
		"xhigh":   agentcore.ThinkingXHigh,
		"  HIGH ": agentcore.ThinkingHigh, // Case/whitespace normalization
	}
	for in, want := range ok {
		got, err := ParseThinkingLevel(in)
		if err != nil {
			t.Errorf("ParseThinkingLevel(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseThinkingLevel(%q) = %q, want %q", in, got, want)
		}
	}

	for _, bad := range []string{"ultra", "max", "true", "0"} {
		if _, err := ParseThinkingLevel(bad); err == nil {
			t.Errorf("ParseThinkingLevel(%q) should return error, but passed", bad)
		}
	}
}
