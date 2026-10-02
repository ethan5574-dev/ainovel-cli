package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/agentcore/subagent"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/agents/guard"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// agentToRole normalizes a subagent name to a ModelSet role.
// architect_short and architect_long share the architect role configuration.
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	return name
}

// promptCacheBase derives a stable short hash from the book directory for prompt-cache identity:
// the same book shares a routing bucket across restarts without exposing the local path to providers.
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}

// subagentMaxRetries is the LLM retry limit for all workers.
// Backoff is exponential, bounded by maxDelay, and honors server Retry-After first.
// Tools start only after a complete assistant message, so stream-idle / 503 / transient
// network failures can retry safely without replaying tool side effects.
const subagentMaxRetries = 7

// UsageRecorder is an optional BuildWorkers usage callback with the same signature as OnMessage.
// Host aggregates one call per agent message. task identifies the spawn session for cache-chain checks.
// nil disables tracking.
type UsageRecorder func(agentName, task string, msg agentcore.AgentMessage)

// ApplyThinking applies a role's reasoning level to workers for runtime /model changes.
// architect updates both architect_* subagents; writer/editor update their matching worker.
// An empty level keeps the model/provider default; other roles are ignored.
type ApplyThinking func(role string, level agentcore.ThinkingLevel)

// ParseThinkingLevel converts a config string to agentcore.ThinkingLevel.
// Empty is valid (no override/inherit); other values must be off/low/medium/high/xhigh/max.
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.NormalizeThinkingLevel(agentcore.ThinkingLevel(s))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingLow, agentcore.ThinkingMedium,
		agentcore.ThinkingHigh, agentcore.ThinkingXHigh, agentcore.ThinkingMax:
		return lv, nil
	default:
		return "", fmt.Errorf("invalid reasoning level %q (options: off/low/medium/high/xhigh/max)", s)
	}
}

func ResolveThinkingForModel(model agentcore.ChatModel, level agentcore.ThinkingLevel) (agentcore.ThinkingLevel, bool) {
	level = agentcore.NormalizeThinkingLevel(level)
	// For ordinary chat models without thinking support, explicit off is an invalid parameter rather than a no-op.
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return agentcore.ThinkingAuto, level == agentcore.ThinkingAuto
	}
	return llm.ThinkingPolicyFor(model).Resolve(level)
}

func AvailableThinkingForModel(model agentcore.ChatModel) []agentcore.ThinkingLevel {
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return []agentcore.ThinkingLevel{agentcore.ThinkingAuto}
	}
	return llm.ThinkingPolicyFor(model).Available
}

// roleThinking resolves a role's effective reasoning level; invalid values fall back to empty with a warning.
func roleThinking(cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	lv, err := ParseThinkingLevel(cfg.ResolveReasoningEffort(role))
	if err != nil {
		slog.Warn("Ignoring invalid reasoning-level configuration", "module", "agent", "role", role, "err", err)
		return ""
	}
	return lv
}

func resolvedRoleThinking(model agentcore.ChatModel, cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	resolved, _ := ResolveThinkingForModel(model, roleThinking(cfg, role))
	return resolved
}

// BuildWorkers assembles architect_short/long, writer, and editor into a programmable
// subagent.Runner. Engine calls its typed entry points directly, without an LLM tool layer.
// (docs/engine-rfc.md §1)。
// Returns Runner, WriterRestorePack, and ApplyThinking (/model updates role reasoning levels;
// each Worker's ContextManager is rebuilt through its factory).
// onGuardBlock is optional (nil-safe), auditing Worker StopGuard blocks/escalations.
func BuildWorkers(
	cfg bootstrap.Config,
	store *store.Store,
	styleStats *tools.StyleStatsIndex,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
	onGuardBlock guard.BlockHook,
) (*subagent.Runner, *ctxpack.WriterRestorePack, ApplyThinking) {
	// Shared tools
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style, styleStats)
	readChapter := tools.NewReadChapterTool(store)

	architectTools := []agentcore.Tool{
		contextTool,
		tools.NewSaveBookTool(store),
		tools.NewSaveFoundationTool(store),
		tools.NewReviseOutlineTool(store),
		tools.NewResolveOutlineFeedbackTool(store),
		tools.NewAuditFoundationTool(store),
	}
	architectLongTools := append([]agentcore.Tool(nil), architectTools...)
	architectLongTools = append(architectLongTools, tools.NewExpandNextArcTool(store))
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewPlanChapterTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		tools.NewCommitChapterTool(store, styleStats),
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewSaveReviewTool(store),
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover is logged but not sent to the host.
	reportFailover := func(ev bootstrap.FailoverEvent) {
		slog.Warn("provider failover",
			"module", "agent",
			"role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err,
		)
	}

	architectModel := models.ForRoleWithFailover("architect", reportFailover)
	writerModel := models.ForRoleWithFailover("writer", reportFailover)
	editorModel := models.ForRoleWithFailover("editor", reportFailover)

	// The factory rebuilds ContextManager for every call, adapting its window to model swaps.
	architectProvider, architectModelName, _ := models.CurrentSelection("architect")
	architectContextWindow, architectSource := cfg.ResolveContextWindow(architectProvider, architectModelName)
	bootstrap.LogContextWindowChoice("architect", architectModelName, architectContextWindow, architectSource)
	writerProvider, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerProvider, writerModelName)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	editorProvider, editorModelName, _ := models.CurrentSelection("editor")
	editorContextWindow, editorSource := cfg.ResolveContextWindow(editorProvider, editorModelName)
	bootstrap.LogContextWindowChoice("editor", editorModelName, editorContextWindow, editorSource)

	// modelLookup attaches _meta:{provider,model} to assistant messages when saving sessions,
	// so replay computes historical cost accurately without relying on the current ModelSet after a model switch.
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, task, msg)
		}
	}

	// Prompt caching: one base per book, one name per role, and one key per session (#seq is added on spawn).
	// OpenAI uses prompt_cache_key for routing affinity; Claude uses cache_control rolling breakpoints.
	// Unsupported providers silently drop the setting through agentcore; there is no toggle.
	cacheBase := promptCacheBase(store.Dir())

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return guard.NewArchitectStopGuard(store, onGuardBlock)
	}
	// Architect/Editor context managers are rebuilt for each run using the current model window.
	roleContextFactory := func(profile roleContextProfile) func(agentcore.ChatModel) agentcore.ContextManager {
		return func(model agentcore.ChatModel) agentcore.ContextManager {
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newRoleContextManager(profile, model, window, contextTool.Name())
		}
	}
	architectThinking, _ := ResolveThinkingForModel(architectModel, roleThinking(cfg, "architect"))
	architectShort := subagent.Config{
		Name:                  "architect_short",
		Description:           "Short planner: compact settings and a flat outline for a dense, single-conflict, single-volume story",
		Model:                 architectModel,
		SystemPrompt:          bundle.Prompts.ArchitectShort,
		Tools:                 architectTools,
		MaxTurns:              15,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         architectThinking,
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-architect_short",
		ContextManagerFactory: roleContextFactory(architectContextProfile),
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			return foundationReadyResult(toolName, result)
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:                  "architect_long",
		Description:           "Long planner: layered settings and volume/arc outlines for serialized stories with sustained escalation",
		Model:                 architectModel,
		SystemPrompt:          bundle.Prompts.ArchitectLong,
		Tools:                 architectLongTools,
		MaxTurns:              20,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         architectThinking,
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-architect_long",
		ContextManagerFactory: roleContextFactory(architectContextProfile),
		StopAfterToolResult:   architectLongShouldStopAfterToolResult,
		StopGuardFactory:      architectStopGuardFactory,
	}

	// One assembly path: fill the {{VOICE}} placeholder in the protocol template, then append the style preset.
	// Voice A/B evaluation uses the same function so both arms remain equivalent.
	writerPrompt := assets.BuildWriterPrompt(bundle.Prompts.Writer, bundle.Voice, bundle.Styles[cfg.Style])

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:             "writer",
		Description:      "Writer: independently plans, writes, self-reviews, and submits a chapter",
		Model:            writerModel,
		SystemPrompt:     writerPrompt,
		Tools:            writerTools,
		MaxTurns:         30,
		MaxRetries:       subagentMaxRetries,
		ThinkingLevel:    resolvedRoleThinking(writerModel, cfg, "writer"),
		StopAfterTools:   []string{"commit_chapter"},
		OnMessage:        onMsg,
		CacheLastMessage: "ephemeral",
		PromptCacheKey:   cacheBase + "-writer",
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return guard.NewWriterStopGuard(store, onGuardBlock)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// Rebuild context management per chapter for the current writer model.
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:         model,
				ContextWindow: window,
				ReserveTokens: bootstrap.CompactReserveTokens(window),
				Agent:         "writer",
				// Commit the projection so later turns do not repeatedly rewrite the request prefix.
				CommitProjected: true,
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					MinResultTokens: 200,
				},
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:            store,
						KeepRecentTokens: 20000,
					}),
				},
				Summary: &corecontext.FullSummaryConfig{
					PostSummaryHooks:    []corecontext.PostSummaryHook{restore.Hook()},
					SystemPrompt:        ctxpack.WriterSummarySystemPrompt,
					SummaryPrompt:       ctxpack.WriterSummaryPrompt,
					UpdateSummaryPrompt: ctxpack.WriterUpdateSummaryPrompt,
					TurnPrefixPrompt:    ctxpack.WriterTurnPrefixPrompt,
				},
			})
		},
	}

	editor := subagent.Config{
		Name:                  "editor",
		Description:           "Reviewer: read original prose and identify structural and aesthetic issues",
		Model:                 editorModel,
		SystemPrompt:          bundle.Prompts.Editor,
		Tools:                 editorTools,
		MaxTurns:              20,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         resolvedRoleThinking(editorModel, cfg, "editor"),
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-editor",
		ContextManagerFactory: roleContextFactory(editorContextProfile),
		// Stop on a terminal artifact, while still consulting StopGuard (contract test TestContract_
		// TerminalToolExitConsultsStopGuard). Task-aware NewEditorStopGuard
		// rejects premature exit after a review when a summary was assigned, so save_review can safely hard-stop.
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_review" || toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return guard.NewEditorStopGuard(store, task, onGuardBlock)
		},
	}

	runner := subagent.NewRunner(architectShort, architectLong, writer, editor)

	// Apply runtime reasoning-level changes from /model.
	applyThinking := func(role string, level agentcore.ThinkingLevel) {
		switch role {
		case "architect":
			level, _ = ResolveThinkingForModel(models.ForRole("architect"), level)
			runner.SetThinkingLevel("architect_short", level)
			runner.SetThinkingLevel("architect_long", level)
		case "writer", "editor":
			level, _ = ResolveThinkingForModel(models.ForRole(role), level)
			runner.SetThinkingLevel(role, level)
		}
	}

	return runner, restore, applyThinking
}

type saveFoundationResult struct {
	Type            string `json:"type"`
	FoundationReady bool   `json:"foundation_ready"`
}

func decodeSaveFoundationResult(toolName string, result json.RawMessage) saveFoundationResult {
	if toolName != "save_foundation" {
		return saveFoundationResult{}
	}
	var r saveFoundationResult
	_ = json.Unmarshal(result, &r)
	return r
}

func architectLongShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	if foundationReadyResult(toolName, result) {
		return true
	}
	if toolName == "expand_next_arc" {
		return true
	}
	r := decodeSaveFoundationResult(toolName, result)
	switch r.Type {
	case "complete_book":
		return true
	default:
		return false
	}
}

func foundationReadyResult(toolName string, result json.RawMessage) bool {
	if toolName != "audit_foundation" {
		return false
	}
	var r struct {
		FoundationReady bool `json:"foundation_ready"`
	}
	return json.Unmarshal(result, &r) == nil && r.FoundationReady
}
