package worker

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

func TestBuildJobConfig_PaidTierOverridesWriterRole(t *testing.T) {
	base := bootstrap.Config{
		Roles: map[string]bootstrap.RoleConfig{
			"writer":    {Provider: "cheap", Model: "cheap-model"},
			"architect": {Provider: "cheap", Model: "cheap-model"},
		},
	}
	override := &bootstrap.RoleConfig{Provider: "premium", Model: "premium-model"}

	cfg := BuildJobConfig(base, &Job{Tier: "paid", WorkspaceDir: "/tmp/x"}, override)

	if got := cfg.Roles["writer"]; got.Provider != "premium" || got.Model != "premium-model" {
		t.Fatalf("writer role = %+v, want premium override", got)
	}
	if got := cfg.Roles["architect"]; got.Provider != "cheap" {
		t.Fatalf("architect role changed unexpectedly: %+v", got)
	}
	// base.Roles must be untouched — every other job built from the same base config
	// would otherwise silently inherit this job's override.
	if got := base.Roles["writer"]; got.Provider != "cheap" {
		t.Fatalf("base config mutated: writer role = %+v, want unchanged cheap-model", got)
	}
}

func TestBuildJobConfig_NonPaidTierIgnoresOverride(t *testing.T) {
	base := bootstrap.Config{
		Roles: map[string]bootstrap.RoleConfig{"writer": {Provider: "cheap", Model: "cheap-model"}},
	}
	override := &bootstrap.RoleConfig{Provider: "premium", Model: "premium-model"}

	cfg := BuildJobConfig(base, &Job{Tier: "free", WorkspaceDir: "/tmp/x"}, override)

	if got := cfg.Roles["writer"]; got.Provider != "cheap" {
		t.Fatalf("free tier got the paid override: writer role = %+v", got)
	}
}

func TestBuildJobConfig_NilOverrideIsNoOp(t *testing.T) {
	base := bootstrap.Config{
		Roles: map[string]bootstrap.RoleConfig{"writer": {Provider: "cheap", Model: "cheap-model"}},
	}

	cfg := BuildJobConfig(base, &Job{Tier: "paid", WorkspaceDir: "/tmp/x"}, nil)

	if got := cfg.Roles["writer"]; got.Provider != "cheap" {
		t.Fatalf("paid tier with nil override changed writer role: %+v", got)
	}
}
