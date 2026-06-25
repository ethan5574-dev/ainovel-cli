package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/subagent"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/reminder"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// logRulesLoaded prints the rule loading status during assembly: project rules directory, actual loaded sources, and the effective word count check value.
// If rule files are placed in the wrong path, they will be silently skipped by the loader, and the sources will not enter the LLM (only visible in the /diag panel).
// Zero feedback on misplaced files is the biggest obstacle for user troubleshooting. This startup log line makes "wrong path / word count not written in front matter" visible at a glance.
func logRulesLoaded(opts rules.LoadOptions) {
	b := rules.Merge(rules.Load(opts))
	words := "Not set (no word count check)"
	if w := b.Structured.ChapterWords; w != nil {
		words = fmt.Sprintf("%d-%d", w.Min, w.Max)
	}
	slog.Info("rules loaded",
		"project_rules_dir", opts.ProjectRulesDir,
		"sources", b.Sources,
		"chapter_words", words)
}

// agentToRole normalizes the subagent name into a role name recognized by ModelSet.
// Both architect_short and architect_long share the same architect role configuration.
// Synonymous with host.agentRoleName, kept separately because build and host do not depend on each other.
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	return name
}

// subagentMaxRetries gives a uniform LLM retry upper limit for all SubAgentConfigs and the Coordinator.
// Backoff strategy: exponential 1s/2s/4s/8s/16s (constrained by maxDelay upper limit), prioritizing server Retry-After.
// Combined with ToolsAreIdempotent=true, this allows retryable errors like stream-idle / 503 / short network jitter
// to be retried locally at the subagent layer, instead of throwing the entire subagent back to the coordinator for rescheduling.
// Project rule 1 guarantees that writing tools are idempotent via checkpoint+digest, making retries safe.
const subagentMaxRetries = 5

// UsageRecorder is an optional usage callback for BuildCoordinator; its signature is consistent with OnMessage,
// called once for each agent message, and aggregated by the Host layer. nil means no tracking.
type UsageRecorder func(agentName string, msg agentcore.AgentMessage)

// ApplyThinking applies the thinking level of a specific role to the live agent (used for runtime /model adjustments).
// coordinator -> Agent.SetThinkingLevel; architect -> two architect_* subagents;
// writer/editor -> corresponding subagents. Empty level = follow model/provider default. Other role names are ignored.
type ApplyThinking func(role string, level agentcore.ThinkingLevel)

// ParseThinkingLevel converts a configuration string to agentcore.ThinkingLevel.
// "" is valid (= no override/inheritance); others must be one of off/minimal/low/medium/high/xhigh,
// otherwise returns an error (downgraded to empty and warned at startup, error echoed to user at runtime).
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.ThinkingLevel(strings.ToLower(strings.TrimSpace(s)))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingMinimal, agentcore.ThinkingLow,
		agentcore.ThinkingMedium, agentcore.ThinkingHigh, agentcore.ThinkingXHigh:
		return lv, nil
	default:
		return "", fmt.Errorf("invalid thinking level %q (options: off/minimal/low/medium/high/xhigh)", s)
	}
}

// roleThinking resolves the effective thinking level of a role; invalid values downgrade to empty (no override) and warn.
func roleThinking(cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	lv, err := ParseThinkingLevel(cfg.ResolveThinking(role))
	if err != nil {
		slog.Warn("ignore invalid thinking level configuration", "module", "agent", "role", role, "err", err)
		return ""
	}
	return lv
}

// BuildCoordinator assembles the Coordinator Agent and its SubAgents.
// Returns references to the Agent, AskUserTool, WriterRestorePack, and the Coordinator's ContextEngine,
// as well as the ApplyThinking closure—Host layer needs to directly call SetContextWindow +
// SetReserveTokens when switching /model to link the window of the new model (writer/architect/editor are
// dynamically reconstructed by ContextManagerFactory and do not need a reference; only the resident coordinator needs one),
// and uses ApplyThinking to link the thinking strength of each role. The Host layer subscribes to the event stream
// through Agent.Subscribe, so emit callbacks are no longer needed.
func BuildCoordinator(
	cfg bootstrap.Config,
	store *store.Store,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
) (*agentcore.Agent, *tools.AskUserTool, *ctxpack.WriterRestorePack, *corecontext.ContextEngine, ApplyThinking) {
	// Shared tools
	rulesOpts := rules.DefaultOptions(bundle.RulesFS)
	logRulesLoaded(rulesOpts)
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style, rulesOpts)
	readChapter := tools.NewReadChapterTool(store)
	askUser := tools.NewAskUserTool()

	architectTools := []agentcore.Tool{
		contextTool,
		tools.NewSaveFoundationTool(store),
	}
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewPlanChapterTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		tools.NewCommitChapterTool(store).WithRules(rulesOpts),
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewSaveReviewTool(store),
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover only logs, does not notify host
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
	coordinatorModel := models.ForRoleWithFailover("coordinator", reportFailover)

	// The Coordinator's ContextManager is generated once during Agent construction, resolved by the startup model.
	// When switching to a model with a smaller window via /model during runtime, users are advised to explicitly configure context_window as a fallback.
	_, coordinatorModelName, _ := models.CurrentSelection("coordinator")
	coordinatorContextWindow, coordinatorSource := cfg.ResolveContextWindow(coordinatorModelName)
	// The Writer's ContextManager is rebuilt by the factory on every call, with the window dynamically following the model swap (see factory below).
	_, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerModelName)
	bootstrap.LogContextWindowChoice("coordinator", coordinatorModelName, coordinatorContextWindow, coordinatorSource)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	// modelLookup appends _meta:{provider,model} to each assistant message when writing to the session,
	// so that replay no longer depends on the "current ModelSet" to infer historical cost, enabling precise calculation even when switching models at runtime.
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, msg)
		}
	}
	baseCoordinatorLog := store.Sessions.CoordinatorLogger(modelLookup)
	coordinatorOnMessage := func(msg agentcore.AgentMessage) {
		baseCoordinatorLog(msg)
		if recordUsage != nil {
			recordUsage("coordinator", msg)
		}
	}

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return reminder.NewArchitectStopGuard(store)
	}
	architectThinking := roleThinking(cfg, "architect")
	architectShort := subagent.Config{
		Name:               "architect_short",
		Description:        "Short planner: generates compact settings and flat outlines for single-volume, single-conflict, high-density stories",
		Model:              architectModel,
		SystemPrompt:       bundle.Prompts.ArchitectShort,
		Tools:              architectTools,
		MaxTurns:           15,
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      architectThinking,
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			r := decodeSaveFoundationResult(toolName, result)
			return r.Type == "outline" && r.FoundationReady
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:               "architect_long",
		Description:        "Long planner: generates hierarchical settings and volume-arc outlines for serialized, continuously upgrading stories",
		Model:              architectModel,
		SystemPrompt:       bundle.Prompts.ArchitectLong,
		Tools:              architectTools,
		MaxTurns:           20,
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      architectThinking,
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			r := decodeSaveFoundationResult(toolName, result)
			switch r.Type {
			case "update_compass", "expand_arc", "complete_book":
				return true
			default:
				return false
			}
		},
		StopGuardFactory: architectStopGuardFactory,
	}

	writerPrompt := bundle.Prompts.Writer
	if style, ok := bundle.Styles[cfg.Style]; ok {
		writerPrompt += "\n\n" + style
	}

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:               "writer",
		Description:        "Writer: independently completes the conception, writing, self-review, and submission of a chapter",
		Model:              writerModel,
		SystemPrompt:       writerPrompt,
		Tools:              writerTools,
		MaxTurns:           30,
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      roleThinking(cfg, "writer"),
		ToolsAreIdempotent: true,
		StopAfterTools:     []string{"commit_chapter"},
		OnMessage:          onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewWriterStopGuard(store)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// Rebuilt on each subagent(writer) call, reading the latest model name from the current runModel.
			// After switching writer via /model, the next chapter automatically uses the new window.
			window, _ := cfg.ResolveContextWindow(bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:            model,
				ContextWindow:    window,
				ReserveTokens:    bootstrap.CompactReserveTokens(window),
				KeepRecentTokens: 20000,
				Agent:            "writer",
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					IdleThreshold: 5 * time.Minute,
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
		Name:               "editor",
		Description:        "Editor: reads the draft to identify problems from both structural and aesthetic levels",
		Model:              editorModel,
		SystemPrompt:       bundle.Prompts.Editor,
		Tools:              editorTools,
		MaxTurns:           20,
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      roleThinking(cfg, "editor"),
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		// Only summary-like terminal products stop immediately; save_review no longer hard-stops—StopAfterTool exit would bypass
		// StopGuard (agentcore loop.go). If save_review hard-stopped, an editor "dispatched to generate arc summary but reviewing first"
		// would be cut off at save_review and unable to reach save_arc_summary. The wrap-up of review/summary tasks
		// is instead managed by the task-aware NewEditorStopGuard.
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return reminder.NewEditorStopGuard(store, task)
		},
	}

	subagentTool := subagent.New(architectShort, architectLong, writer, editor)

	coordinatorEngine := newContextManager(contextManagerConfig{
		Model:            coordinatorModel,
		ContextWindow:    coordinatorContextWindow,
		ReserveTokens:    bootstrap.CompactReserveTokens(coordinatorContextWindow),
		KeepRecentTokens: 30000,
		Agent:            "coordinator",
		CommitOnProject:  true,
	})

	agent := agentcore.NewAgent(
		agentcore.WithModel(coordinatorModel),
		agentcore.WithSystemPrompt(bundle.Prompts.Coordinator),
		agentcore.WithTools(subagentTool, contextTool, tools.NewSaveDirectiveTool(store), tools.NewReopenBookTool(store)),
		agentcore.WithMaxTurns(100_000),
		agentcore.WithOnMessage(coordinatorOnMessage),
		agentcore.WithToolsAreIdempotent(true),
		// subagent is the main channel of the process; real errors should be explicitly returned to the Host, instead of permanently disabling tools in a single run.
		agentcore.WithMaxToolErrors(0),
		agentcore.WithMaxRetries(subagentMaxRetries),
		agentcore.WithContextManager(coordinatorEngine),
		agentcore.WithStopGuard(reminder.NewStopGuard(store, nil)),
		// hard intercept of subagent dispatch when phase=complete, preventing Writer infinite loops.
		agentcore.WithToolGate(completePhaseGate(store)),
	)
	// Coordinator thinking level: unconditionally apply resolved results. Empty when unconfigured (does not send thinking, using provider
	// default), consistent with each subagent (Config.ThinkingLevel defaults to empty)—avoiding overriding agentcore default
	// ThinkingLow which would force low on all providers (including GLM/Ollama which would be forced to turn on thinking).
	agent.SetThinkingLevel(roleThinking(cfg, "coordinator"))

	// runtime linkage of each role's thinking strength: coordinator goes through Agent, subagents go through subagentTool override.
	applyThinking := func(role string, level agentcore.ThinkingLevel) {
		switch role {
		case "coordinator":
			agent.SetThinkingLevel(level)
		case "architect":
			subagentTool.SetThinkingLevel("architect_short", level)
			subagentTool.SetThinkingLevel("architect_long", level)
		case "writer", "editor":
			subagentTool.SetThinkingLevel(role, level)
		}
	}

	return agent, askUser, restore, coordinatorEngine, applyThinking
}

// completePhaseGate returns a ToolGate: rejects all subagent dispatches when phase=complete.
// Prevents Coordinator LLM from calling Writer/Architect after the book is completed, causing infinite loops.
func completePhaseGate(st *store.Store) agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		// fail-open: always allow when Load fails or progress is nil, to prevent locking normal dispatch due to transient read errors.
		// The only cost is that the deadlock might recur if a read failure happens precisely during the complete phase (extremely low probability, acceptable).
		progress, _ := st.Progress.Load()
		if progress != nil && progress.Phase == domain.PhaseComplete {
			return &agentcore.GateDecision{
				Allowed: false,
				Reason:  "The book is already completed (phase=complete), cannot dispatch subagents directly. If the user wants to rework already written chapters, please call reopen_book(chapters=[...]) first to reopen the book into rework status (writer will be automatically dispatched to rewrite thereafter); if the user wants to add new plotlines, inform them that a new project needs to be created.",
			}, nil
		}
		return nil, nil
	}
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
