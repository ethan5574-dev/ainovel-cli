package agents

import (
	"log/slog"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// contextManagerConfig aggregates all configuration parameters of ContextManager.
type contextManagerConfig struct {
	Model            agentcore.ChatModel
	ContextWindow    int
	ReserveTokens    int
	Agent            string
	CommitProjected  bool
	Summary          *corecontext.FullSummaryConfig
	ToolMicrocompact *corecontext.ToolResultMicrocompactConfig
	ExtraStrategies  []corecontext.Strategy
}

func newContextManager(cfg contextManagerConfig) *corecontext.ContextEngine {
	var sc corecontext.FullSummaryConfig
	if cfg.Summary != nil {
		sc = *cfg.Summary
	}
	sc.Model = cfg.Model

	var tc corecontext.ToolResultMicrocompactConfig
	if cfg.ToolMicrocompact != nil {
		tc = *cfg.ToolMicrocompact
	}

	strategies := []corecontext.Strategy{
		corecontext.NewToolResultMicrocompact(tc),
	}
	strategies = append(strategies, cfg.ExtraStrategies...)
	strategies = append(strategies, corecontext.NewFullSummary(sc))

	var commitStrategies []string
	if cfg.CommitProjected {
		commitStrategies = make([]string, len(strategies))
		for i, strategy := range strategies {
			commitStrategies[i] = strategy.Name()
		}
	}

	engine := corecontext.NewEngine(corecontext.EngineConfig{
		ContextWindow:    cfg.ContextWindow,
		ReserveTokens:    cfg.ReserveTokens,
		CommitStrategies: commitStrategies,
		Strategies:       strategies,
	})

	callback := contextRewriteCallback(cfg.Agent)
	engine.SetProjectHook(callback)
	engine.SetRecoverHook(callback)
	return engine
}

// roleContextProfile describes compaction for single-task, repeated-read Workers such as Architect/Editor:
// clear only old novel_context results (persisted data can be reread), retaining write results and prose;
// if still over budget, use role-specific full summarization.
type roleContextProfile struct {
	Agent           string
	KeepRecentReads int // Number of recent novel_context reads to retain
	Summary         corecontext.FullSummaryConfig
}

// newRoleContextManager builds this profile for the current model window.
func newRoleContextManager(p roleContextProfile, model agentcore.ChatModel, window int, contextToolName string) *corecontext.ContextEngine {
	summary := p.Summary
	return newContextManager(contextManagerConfig{
		Model:           model,
		ContextWindow:   window,
		ReserveTokens:   bootstrap.CompactReserveTokens(window),
		Agent:           p.Agent,
		CommitProjected: true,
		ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
			KeepRecent:      p.KeepRecentReads,
			MinResultTokens: 200,
			Classifier:      func(toolName string) bool { return toolName == contextToolName },
		},
		Summary: &summary,
	})
}

// contextRewriteCallback creates a logging callback for context rewrites.
// The new architecture writes only slog, not runtime queue or UIEvent entries.
func contextRewriteCallback(agent string) func(corecontext.RewriteEvent) {
	return func(ev corecontext.RewriteEvent) {
		attrs := []any{
			"module", "context",
			"agent", agent,
			"reason", ev.Reason,
			"strategy", ev.Strategy,
			"committed", ev.Committed,
			"tokens_before", ev.TokensBefore,
			"tokens_after", ev.TokensAfter,
		}
		if info := ev.Info; info != nil {
			attrs = append(attrs,
				"msgs_before", info.MessagesBefore,
				"msgs_after", info.MessagesAfter,
				"compacted", info.CompactedCount,
				"kept", info.KeptCount,
				"duration_ms", info.Duration.Milliseconds(),
			)
		}
		slog.Warn("context rewrite", attrs...)
	}
}
