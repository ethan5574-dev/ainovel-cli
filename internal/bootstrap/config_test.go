package bootstrap

import "testing"

func TestConfigResolveThinking(t *testing.T) {
	cfg := Config{
		Thinking: "low", // Top-level default
		Roles: map[string]RoleConfig{
			"writer":    {Provider: "p", Model: "m", Thinking: "high"}, // Role override
			"architect": {Provider: "p", Model: "m"},                   // No thinking set, should fallback to default
		},
	}

	cases := []struct {
		role string
		want string
	}{
		{"writer", "high"},     // Role override prioritized
		{"architect", "low"},   // Role not configured -> fallback to top-level default
		{"editor", "low"},      // Role does not exist -> top-level default
		{"", "low"},            // Empty -> top-level default
		{"default", "low"},     // default -> top-level default
		{"coordinator", "low"}, // Role not configured -> top-level default
	}
	for _, c := range cases {
		if got := cfg.ResolveThinking(c.role); got != c.want {
			t.Errorf("ResolveThinking(%q) = %q, want %q", c.role, got, c.want)
		}
	}

	// When top-level default is also empty, returns "" for unconfigured roles (no override).
	empty := Config{Roles: map[string]RoleConfig{"writer": {Thinking: "xhigh"}}}
	if got := empty.ResolveThinking("editor"); got != "" {
		t.Errorf("under empty default, editor should return \"\", got %q", got)
	}
	if got := empty.ResolveThinking("writer"); got != "xhigh" {
		t.Errorf("under empty default, writer override should take effect, got %q", got)
	}
}
