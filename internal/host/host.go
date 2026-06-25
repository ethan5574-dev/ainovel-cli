package host

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/exp"
	"github.com/voocel/ainovel-cli/internal/host/flow"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/host/sim"
	modelreg "github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/rules"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// Host is a runtime thin wrapper.
// Responsibilities: Start / Resume / Intervention Injection / Event Projection / Model Management.
// Does not make scheduling decisions, does not run when idle.
type Host struct {
	cfg               bootstrap.Config
	bundle            assets.Bundle
	store             *storepkg.Store
	models            *bootstrap.ModelSet
	coordinator       *agentcore.Agent
	coordinatorCtxMgr *corecontext.ContextEngine // Linked SetContextWindow + SetReserveTokens when switching default/coordinator models
	thinkingApplier   agents.ApplyThinking       // Linked live agent (coordinator + sub-agents) when adjusting thinking strength with /model
	askUser           *tools.AskUserTool
	writerRestore     *ctxpack.WriterRestorePack
	observer          *observer
	router            *flow.Dispatcher
	routerDetach      func()
	usage             *UsageTracker
	usageCancel       context.CancelFunc // Stop autoSaveLoop and trigger a final flush
	budget            *BudgetSentinel    // Budget policy; nil if not enabled (nil-safe)
	budgetDetach      func()
	notifier          *notify.Notifier // Unattended alert; nil if not enabled (nil-safe Send)

	events   chan Event
	streamCh chan string
	done     chan struct{}

	mu         sync.Mutex
	lifecycle  lifecycle
	cocreating bool // Co-create occupation: block concurrent interventions of import/simulate/continue within paused window
	closeOnce  sync.Once
}

type lifecycle string

const (
	lifecycleIdle      lifecycle = "idle"
	lifecycleRunning   lifecycle = "running"
	lifecyclePaused    lifecycle = "paused"
	lifecycleCompleted lifecycle = "completed"
)

// New creates Host.
func New(cfg bootstrap.Config, bundle assets.Bundle) (*Host, error) {
	cfg.FillDefaults()
	if err := cfg.ValidateBase(); err != nil {
		return nil, err
	}
	slog.Info("Start", "module", "boot", "provider", cfg.Provider, "model", cfg.ModelName, "output", cfg.OutputDir)

	// Start background goroutine to refresh model metadata (window/pricing) from OpenRouter, disk cache 24h.
	modelreg.StartPricingRefresh(modelreg.DefaultRegistry(), bootstrap.DefaultConfigDir())

	store := storepkg.NewStore(cfg.OutputDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return nil, fmt.Errorf("create models: %w", err)
	}
	slog.Info("Model ready", "module", "boot", "summary", models.Summary())

	usage := NewUsageTracker(models, store)
	// Prioritize loading meta/usage.json; use sessions/*.jsonl to backfill in following cases:
	//   - File does not exist (first upgrade to version with persistence)
	//   - Schema version mismatch (discard old formats after future upgrade)
	//   - File exists but is corrupted / IO error (prevent bad data from resetting accumulated totals to zero)
	// SaveNow immediately after backfill, solidifying the results, so next startup hits Load directly.
	loaded, loadErr := usage.LoadFromStore()
	if loadErr != nil {
		slog.Warn("usage load failed, will try to backfill from sessions", "module", "usage", "err", loadErr)
	}
	if !loaded {
		if n, err := usage.ReplaySessions(cfg.OutputDir); err != nil {
			slog.Warn("usage replay failed", "module", "usage", "err", err)
		} else if n > 0 {
			slog.Info("usage backfilled from sessions successfully", "module", "usage", "messages", n)
			if err := usage.SaveNow(); err != nil {
				slog.Warn("save after usage backfill failed", "module", "usage", "err", err)
			}
		}
	}
	usageCtx, usageCancel := context.WithCancel(context.Background())
	usage.StartAutoSave(usageCtx)

	coordinator, askUser, restore, coordinatorCtxMgr, applyThinking := agents.BuildCoordinator(cfg, store, models, bundle, usage.Record)
	store.Signals.ClearStaleSignals()

	h := &Host{
		cfg:               cfg,
		bundle:            bundle,
		store:             store,
		models:            models,
		coordinator:       coordinator,
		coordinatorCtxMgr: coordinatorCtxMgr,
		thinkingApplier:   applyThinking,
		askUser:           askUser,
		writerRestore:     restore,
		usage:             usage,
		usageCancel:       usageCancel,
		events:            make(chan Event, 100),
		streamCh:          make(chan string, 256),
		done:              make(chan struct{}, 4),
		lifecycle:         lifecycleIdle,
	}
	h.observer = newObserver(coordinator, store, h.emitEvent, h.emitDelta, h.emitClear)
	if cfg.Notify.IsEnabled() {
		h.notifier = notify.New(cfg.Notify.Command, cfg.Notify.Events)
	}
	// Budget sentinel subscription must precede Dispatcher: on the same sub-agent boundary event, Abort and FollowUp
	// compete. If Sentinel sets Abort first, the Dispatcher's dispatch naturally falls through; the routing layer does not perceive the budget.
	if sentinel := NewBudgetSentinel(cfg.Budget,
		func() float64 { c, _, _, _, _ := usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: "budget", Level: level, Title: "ainovel: Budget", Body: summary})
		},
	); sentinel != nil {
		h.budget = sentinel
		usage.SetOnCost(sentinel.OnCost)
		h.budgetDetach = coordinator.Subscribe(sentinel.HandleEvent)
		// Billing blind spot alert: when model doesn't return usage, the cost stays at 0, budget never triggers - fuse not connected must alert someone.
		usage.SetOnMissingUsage(func() {
			const blind = "Budget blind spot: model did not return usage data, cost statistic is 0, budget limit will not trigger (for custom models please verify registry pricing or upstream include_usage)"
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: blind, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: "budget", Level: "warn", Title: "ainovel: Budget", Body: blind})
		})
	}
	h.router = flow.NewDispatcher(coordinator, store)
	// Duplicate command alert: pure telemetry, when hanging "model might be spinning in place" is worth calling someone to look.
	// Event stream and notify are emitted in pairs - notify is just an off-screen copy of the in-screen event (Architecture §2.3).
	h.router.SetOnRepeat(func(agent, task string, n int) {
		body := fmt.Sprintf("The same command has been issued for the %d time (%s): %s", n, agent, task)
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Duplicate Command: " + body, Level: "warn"})
		h.notifier.Send(notify.Notification{Kind: "repeat", Level: "warn", Title: "ainovel: Duplicate Command", Body: body})
	})
	h.routerDetach = h.router.Attach()

	if err := store.RunMeta.Init(cfg.Style, cfg.Provider, cfg.ModelName); err != nil {
		slog.Error("Failed to initialize runtime meta info", "module", "boot", "err", err)
	}

	return h, nil
}

// ── Lifecycle ──

// Start start mode: initialize progress and start the coordinator long loop.
func (h *Host) Start(prompt string) error {
	return h.StartPrepared(BuildStartPrompt(prompt))
}

// StartPrepared starts creation using the prepared startup prompt.
func (h *Host) StartPrepared(promptText string) error {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("stage co-creation in progress, please end co-creation first")
	}
	h.mu.Unlock()

	promptText = strings.TrimSpace(promptText)
	if promptText == "" {
		return fmt.Errorf("prompt is required")
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	if err := h.store.Checkpoints.Reset(); err != nil {
		return fmt.Errorf("reset checkpoints: %w", err)
	}
	if err := h.store.Progress.Init("", 0); err != nil {
		return fmt.Errorf("init progress: %w", err)
	}

	slog.Info("Start creation", "module", "host", "prompt_len", len(promptText))
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Start creation", Level: "info"})
	h.observer.setAborting(false)
	// Reset duplicate tracking and enable routing before starting Prompt, avoiding first round of events arriving before Enable
	h.router.ResetRepeat()
	h.router.Enable()
	if err := h.coordinator.Prompt(context.Background(), promptText); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	// Actively dispatch the first command: if already in writing stage (Phase=Writing), Host delivers it immediately;
	// route returns nil during the planning stage, with no side effects.
	h.router.Dispatch()

	h.mu.Lock()
	h.lifecycle = lifecycleRunning
	h.mu.Unlock()
	go h.waitDone()
	return nil
}

// Resume resume mode: generates resume prompt from checkpoint + progress and starts.
func (h *Host) Resume() (string, error) {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return "", fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return "", fmt.Errorf("stage co-creation in progress, please end co-creation first")
	}
	h.mu.Unlock()

	prompt, label, err := buildResumePrompt(h.store)
	if err != nil {
		return "", err
	}
	if label == "" {
		return "", nil // New mode, no resume
	}
	if err := h.budget.Refuse(); err != nil {
		return "", err
	}

	slog.Info("Resume creation", "module", "host", "label", label)
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Resume creation: " + label, Level: "info"})
	for _, w := range h.store.CheckConsistency() {
		slog.Warn("consistency alert", "module", "host", "detail", w)
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Consistency alert: " + w, Level: "warn"})
	}
	h.refreshWriterRestore()
	h.observer.setAborting(false)
	h.router.ResetRepeat()
	h.router.Enable()
	if err := h.coordinator.Prompt(context.Background(), prompt); err != nil {
		return "", fmt.Errorf("resume prompt: %w", err)
	}
	// Actively dispatch the first command, preventing the Coordinator from replying text only to resume prompt while StopGuard repeatedly intercepts it.
	h.router.Dispatch()

	h.mu.Lock()
	h.lifecycle = lifecycleRunning
	h.mu.Unlock()
	go h.waitDone()
	return label, nil
}

// interventionMsg wraps user text into an intervention message readable by Coordinator.
// Steer and Continue share the same framing: user commands from both entry points carry `[User Intervention]` prefix
// to stably trigger intervention classification in coordinator.md. Otherwise, raw text of Continue would bypass routing rules,
// causing Coordinator to lose classification anchors and mis-dispatch sub-agents (previously caused "modify completed chapters" to be dispatched to writer, hitting edit_chapter guard).
func interventionMsg(text string) agentcore.Message {
	return agentcore.UserMsg("[User Intervention] " + text)
}

// Continue continues with the specified prompt. Called when user inputs in input box after stopping.
func (h *Host) Continue(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text is required")
	}
	h.mu.Lock()
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("stage co-creation in progress, please end co-creation first")
	}
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[Continue] " + text, Level: "info"})

	if running {
		h.coordinator.FollowUp(interventionMsg(text))
		return nil
	}
	// After stop -> inject and resume automatically (resuming run is also subject to budget pre-constraints)
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	h.refreshWriterRestore()
	h.observer.setAborting(false)
	_, err := h.coordinator.Inject(interventionMsg(text))
	if err != nil {
		return fmt.Errorf("inject: %w", err)
	}
	h.mu.Lock()
	h.lifecycle = lifecycleRunning
	h.mu.Unlock()
	go h.waitDone()
	return nil
}

// Steer submits user intervention.
func (h *Host) Steer(text string) {
	h.mu.Lock()
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[User Intervention] " + text, Level: "info"})

	msg := interventionMsg(text)
	if running {
		if _, err := h.coordinator.Inject(msg); err != nil {
			slog.Error("steer inject failed", "module", "host", "err", err)
		}
		return
	}
	// Stopped: persist for next startup + feedback system status ("saved" is a system notification outside USER events)
	_ = h.store.RunMeta.SetPendingSteer(text)
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Intervention saved, will take effect on next startup", Level: "info"})
}

// Abort pauses the current coordinator.
func (h *Host) Abort() bool {
	return h.abortWithEvent("User manually paused the current creation", "warn")
}

// abortWithEvent executes pause with a specified reason event. Budget shutdown and manual pause share the same shutdown mechanism,
// only the event text is different (budget shutdown = Abort command pre-signed by the user, semantically equivalent to manual pause).
func (h *Host) abortWithEvent(summary, level string) bool {
	h.mu.Lock()
	running := h.lifecycle == lifecycleRunning
	if running {
		h.lifecycle = lifecyclePaused
	}
	h.mu.Unlock()
	if !running {
		return false
	}
	// Setting the flag must be done before coordinator.Abort: cancel propagation immediately triggers stream init / subagent
	// failure events, which observer identifies as abort-derived noise and suppresses based on this flag.
	h.observer.setAborting(true)
	h.coordinator.Abort()
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
	return true
}

// Close terminates coordinator and closes event channels.
//
// Usage persistence semantics: first cancel autoSaveLoop (which flushes the last dirty state on its own),
// then perform a synchronous SaveNow to wrap up. Known gap: if there are still in-flight LLM calls returning
// after AbortSilent, the triggered OnMessage -> Record will update memory but **will not be persisted**. This loss of
// "the last few hundred tokens" will be automatically backfilled by session jsonl replay at next startup.
func (h *Host) Close() {
	h.observer.setAborting(true)
	h.coordinator.AbortSilent()
	if h.routerDetach != nil {
		h.routerDetach()
		h.routerDetach = nil
	}
	if h.budgetDetach != nil {
		h.budgetDetach()
		h.budgetDetach = nil
	}
	if h.usageCancel != nil {
		h.usageCancel()
		h.usageCancel = nil
	}
	if err := h.usage.SaveNow(); err != nil {
		slog.Warn("save usage before exit failed", "module", "usage", "err", err)
	}
	h.closeOnce.Do(func() {
		close(h.done)
		close(h.events)
		close(h.streamCh)
	})
}

// waitDone waits for coordinator to stop and publishes end state events.
//
// Does not make any automatic resume. Run end = Host enters final state:
//   - Phase=Complete  -> mark completed, emit "creation completed" event
//   - Others          -> mark idle, emit "Coordinator stopped" event
//
// There are only two paths for the user to continue creation: manual Continue (inject during downtime) or restart process via Resume.
// See docs/architecture.md §13.3, §8.3.
func (h *Host) waitDone() {
	h.coordinator.WaitForIdle()
	h.observer.finalize()

	h.mu.Lock()
	progress, _ := h.store.Progress.Load()
	if progress != nil && progress.Phase == domain.PhaseComplete {
		h.lifecycle = lifecycleCompleted
		summary := fmt.Sprintf("Creation completed: %d Chs %d words", len(progress.CompletedChapters), progress.TotalWordCount)
		h.mu.Unlock()
		slog.Info(summary, "module", "host")
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "success"})
		h.notifier.Send(notify.Notification{
			Kind: "run_end", Level: "info", Title: "ainovel: Creation Completed",
			Body: h.runEndBody(progress.NovelName, summary),
		})
	} else {
		wasRunning := h.lifecycle == lifecycleRunning
		if wasRunning {
			h.lifecycle = lifecycleIdle
		}
		completed := 0
		name := ""
		if progress != nil {
			completed = len(progress.CompletedChapters)
			name = progress.NovelName
		}
		h.mu.Unlock()
		if wasRunning {
			summary := fmt.Sprintf("Coordinator stopped (%d chapters completed)", completed)
			slog.Warn(summary, "module", "host")
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "warn"})
			h.notifier.Send(notify.Notification{
				Kind: "run_end", Level: "warn", Title: "ainovel: Creation Stopped",
				Body: h.runEndBody(name, summary),
			})
		}
	}

	select {
	case h.done <- struct{}{}:
	default:
	}
}

// runEndBody assembles run_end notification body: book title + progress summary + total cost.
func (h *Host) runEndBody(novelName, summary string) string {
	if name := strings.TrimSpace(novelName); name != "" {
		summary = "《" + name + "》" + summary
	}
	cost, _, _, _, _ := h.usage.Totals()
	if cost > 0 {
		summary += fmt.Sprintf(" · cost $%.2f", cost)
	}
	return summary
}

// ── Channels ──

// StreamClearSentinel is sent individually via streamCh to indicate "clear current streaming round".
// Do not use a separate clearCh - dual channel disorder often causes the ✻ header to land at the end of the previous round.
const StreamClearSentinel = "\x00\x00CLEAR\x00\x00"

func (h *Host) Events() <-chan Event        { return h.events }
func (h *Host) Stream() <-chan string       { return h.streamCh }
func (h *Host) Done() <-chan struct{}       { return h.done }
func (h *Host) Dir() string                 { return h.store.Dir() }
func (h *Host) AskUser() *tools.AskUserTool { return h.askUser }

// ── Event Emission ──

func (h *Host) emitEvent(ev Event) {
	defer func() { recover() }()
	// Unique slog entry point for all events. agentcore events translated by observer and SYSTEM events self-emitted
	// by Host (Start/Abort/Resume...) are all logged here, avoiding the inability to distinguish ESC abort and external
	// termination in tui.log.
	if ev.Summary != "" || ev.Detail != "" {
		level := slog.LevelInfo
		switch ev.Level {
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
		// Log full Detail (for troubleshooting, untruncated); fallback to Summary only if Detail is empty.
		msg := ev.Detail
		if msg == "" {
			msg = ev.Summary
		}
		attrs := []any{"module", "event", "category", ev.Category, "agent", ev.Agent}
		if ev.Kind != "" {
			attrs = append(attrs, "kind", ev.Kind)
		}
		slog.Log(context.Background(), level, msg, attrs...)
	}
	select {
	case h.events <- ev:
	default:
		select {
		case <-h.events:
		default:
		}
		select {
		case h.events <- ev:
		default:
		}
	}
}

func (h *Host) emitDelta(delta string) {
	defer func() { recover() }()
	select {
	case h.streamCh <- delta:
	default:
		select {
		case <-h.streamCh:
		default:
		}
		select {
		case h.streamCh <- delta:
		default:
		}
	}
}

func (h *Host) emitClear() {
	// Sent via streamCh as a "sentinel" to ensure in-order delivery to TUI in the same channel as emitDelta.
	h.emitDelta(StreamClearSentinel)
}

// ── Snapshot (TUI State Aggregation) ──

func (h *Host) Snapshot() UISnapshot {
	h.mu.Lock()
	state := h.lifecycle
	provider, model, _ := h.models.CurrentSelection("default")
	h.mu.Unlock()

	// Dynamically parse the context window of current model, next Snapshot reflects the switch of /model automatically
	modelWindow, _ := h.cfg.ResolveContextWindow(model)
	cost, tokIn, tokOut, cacheRead, cacheWrite := h.usage.Totals()
	saved := h.usage.SavedUSD()
	overallCapable := h.usage.OverallCacheCapable()
	recentRead, recentInput, recentSamples := h.usage.OverallRecent()
	perAgent := h.usage.PerAgent()
	cacheStats := make([]AgentCacheStat, 0, len(perAgent))
	for _, a := range perAgent {
		cacheStats = append(cacheStats, AgentCacheStat{
			Role:            a.Role,
			Input:           a.Input,
			Output:          a.Output,
			CacheRead:       a.CacheRead,
			CacheWrite:      a.CacheWrite,
			Cost:            a.Cost,
			Saved:           a.Saved,
			CacheCapable:    a.CacheCapable,
			RecentCacheRead: a.RecentCacheRead,
			RecentInput:     a.RecentInput,
			RecentSamples:   a.RecentSamples,
		})
	}
	perModel := h.usage.PerModel()
	modelStats := make([]AgentCacheStat, 0, len(perModel))
	for _, a := range perModel {
		modelStats = append(modelStats, AgentCacheStat{
			Model:        a.Model,
			Input:        a.Input,
			Output:       a.Output,
			CacheRead:    a.CacheRead,
			CacheWrite:   a.CacheWrite,
			Cost:         a.Cost,
			Saved:        a.Saved,
			CacheCapable: a.CacheCapable,
		})
	}

	snap := UISnapshot{
		Provider:               provider,
		ModelName:              model,
		ModelContextWindow:     modelWindow,
		Style:                  h.cfg.Style,
		RuntimeState:           string(state),
		IsRunning:              state == lifecycleRunning,
		TotalInputTokens:       tokIn,
		TotalOutputTokens:      tokOut,
		TotalCacheReadTokens:   cacheRead,
		TotalCacheWriteTokens:  cacheWrite,
		TotalCostUSD:           cost,
		TotalSavedUSD:          saved,
		BudgetLimitUSD:         h.budget.Limit(),
		OverallCacheCapable:    overallCapable,
		OverallRecentCacheRead: recentRead,
		OverallRecentInput:     recentInput,
		OverallRecentSamples:   recentSamples,
		CachePerAgent:          cacheStats,
		CachePerModel:          modelStats,
		MissingAssistantUsage:  h.usage.MissingAssistantUsage(),
	}

	progress, _ := h.store.Progress.Load()
	if progress != nil {
		snap.NovelName = strings.TrimSpace(progress.NovelName)
		snap.Phase = string(progress.Phase)
		snap.Flow = string(progress.Flow)
		snap.CurrentChapter = progress.CurrentChapter
		snap.TotalChapters = progress.TotalChapters
		snap.CompletedCount = len(progress.CompletedChapters)
		snap.TotalWordCount = progress.TotalWordCount
		snap.InProgressChapter = progress.InProgressChapter
		snap.PendingRewrites = progress.PendingRewrites
		snap.RewriteReason = progress.RewriteReason
		snap.Layered = progress.Layered
		if progress.CurrentVolume > 0 {
			snap.CurrentVolumeArc = fmt.Sprintf("Vol %d · Arc %d", progress.CurrentVolume, progress.CurrentArc)
		}
	}
	if snap.NovelName == "" {
		if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
			snap.NovelName = domain.ExtractNovelNameFromPremise(premise)
		}
	}
	if meta, _ := h.store.RunMeta.Load(); meta != nil {
		snap.PendingSteer = meta.PendingSteer
	}

	snap.Agents = h.observer.agentSnapshots()
	h.fillContextStatus(&snap)
	snap.StatusLabel = deriveStatusLabel(snap)

	// Recovery label
	if _, label, err := buildResumePrompt(h.store); err == nil && label != "" {
		snap.RecoveryLabel = label
	}

	h.fillDetails(&snap, progress)

	return snap
}

// fillContextStatus populates Coordinator context health info.
func (h *Host) fillContextStatus(snap *UISnapshot) {
	if h.coordinator == nil {
		return
	}
	if usage := h.coordinator.BaselineContextUsage(); usage != nil {
		snap.ContextTokens = usage.Tokens
		snap.ContextWindow = usage.ContextWindow
		snap.ContextPercent = usage.Percent
	}
	if ctx := h.coordinator.ContextSnapshot(); ctx != nil {
		snap.ContextScope = ctx.Scope
		snap.ContextStrategy = ctx.LastStrategy
		snap.ContextActiveMessages = ctx.ActiveMessages
		snap.ContextSummaryCount = ctx.SummaryMessages
		snap.ContextCompactedCount = ctx.LastCompactedCount
		snap.ContextKeptCount = ctx.LastKeptCount
		if snap.ContextTokens == 0 {
			if ctx.BaselineUsage != nil {
				snap.ContextTokens = ctx.BaselineUsage.Tokens
				snap.ContextWindow = ctx.BaselineUsage.ContextWindow
				snap.ContextPercent = ctx.BaselineUsage.Percent
			} else if ctx.Usage != nil {
				snap.ContextTokens = ctx.Usage.Tokens
				snap.ContextWindow = ctx.Usage.ContextWindow
				snap.ContextPercent = ctx.Usage.Percent
			}
		}
	}
}

// fillDetails populates details pane: premise, characters, recent commit/review/summaries.
func (h *Host) fillDetails(snap *UISnapshot, progress *domain.Progress) {
	if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
		snap.Premise = truncate(premise, 80)
	}
	if outline, _ := h.store.Outline.LoadOutline(); len(outline) > 0 {
		for _, e := range outline {
			snap.Outline = append(snap.Outline, OutlineSnapshot{
				Chapter: e.Chapter, Title: e.Title, CoreEvent: e.CoreEvent,
			})
		}
	}
	if progress != nil && progress.Layered {
		if compass, _ := h.store.Outline.LoadCompass(); compass != nil {
			snap.CompassDirection = compass.EndingDirection
			snap.CompassScale = compass.EstimatedScale
		}
		if volumes, _ := h.store.Outline.LoadLayeredOutline(); len(volumes) > 0 {
			for _, v := range volumes {
				if v.Index > progress.CurrentVolume {
					snap.NextVolumeTitle = v.Title
					break
				}
			}
		}
	}
	if chars, _ := h.store.Characters.Load(); len(chars) > 0 {
		for _, c := range chars {
			label := c.Name
			if c.Role != "" {
				label += " (" + c.Role + ")"
			}
			snap.Characters = append(snap.Characters, label)
		}
	}
	if ledger, _ := h.store.Cast.Load(); len(ledger) > 0 {
		snap.SupportingCount = len(ledger)
		recent, _ := h.store.Cast.RecentActive(5)
		for _, e := range recent {
			label := e.Name
			if e.BriefRole != "" {
				label += " (" + e.BriefRole + ")"
			}
			snap.RecentSupporting = append(snap.RecentSupporting, label)
		}
	}
	if progress != nil && len(progress.CompletedChapters) > 0 {
		lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
		wc := progress.ChapterWordCounts[lastCh]
		snap.LastCommitSummary = fmt.Sprintf("Ch%d (%d words)", lastCh, wc)
	}
	currentCh := 1
	if progress != nil && len(progress.CompletedChapters) > 0 {
		currentCh = progress.CompletedChapters[len(progress.CompletedChapters)-1]
	}
	if review, err := h.store.World.LoadLastReview(currentCh); err == nil && review != nil {
		snap.LastReviewSummary = fmt.Sprintf("verdict=%s (%d issues)", review.Verdict, len(review.Issues))
		if len(review.AffectedChapters) > 0 {
			snap.LastReviewSummary += fmt.Sprintf(" affects %v", review.AffectedChapters)
		}
	}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		snap.LastCheckpointName = fmt.Sprintf("%s.%s", cp.Scope, cp.Step)
	}
	if progress != nil {
		for i := len(progress.CompletedChapters) - 1; i >= 0 && len(snap.RecentSummaries) < 2; i-- {
			ch := progress.CompletedChapters[i]
			if summary, err := h.store.Summaries.LoadSummary(ch); err == nil && summary != nil {
				snap.RecentSummaries = append(snap.RecentSummaries,
					fmt.Sprintf("Ch%d: %s", ch, truncate(summary.Summary, 50)))
			}
		}
	}
}

func deriveStatusLabel(s UISnapshot) string {
	switch {
	case s.Phase == string(domain.PhaseComplete):
		return "COMPLETE"
	case s.Flow == string(domain.FlowReviewing):
		return "REVIEW"
	case s.Flow == string(domain.FlowRewriting) || s.Flow == string(domain.FlowPolishing):
		return "REWRITE"
	case s.RuntimeState == "running":
		return "RUNNING"
	default:
		return "READY"
	}
}

// ── Model Management ──

func (h *Host) ConfiguredProviders() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	providers := make([]string, 0, len(h.cfg.Providers))
	for name := range h.cfg.Providers {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	return providers
}

func (h *Host) ConfiguredModels(provider string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.CandidateModels(provider)
}

func (h *Host) CurrentModelSelection(role string) (string, string, bool) {
	return h.models.CurrentSelection(role)
}

func (h *Host) SwitchModel(role, provider, model string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required")
	}
	if err := h.models.Swap(role, provider, model); err != nil {
		return err
	}
	if role == "" || role == "default" {
		h.cfg.Provider = provider
		h.cfg.ModelName = model
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.Provider = provider
		rc.Model = model
		h.cfg.Roles[role] = rc
	}
	if path := bootstrap.DefaultConfigPath(); path != "" {
		if err := bootstrap.SaveConfig(path, h.cfg); err != nil {
			slog.Warn("failed to save config", "module", "host", "err", err)
		}
	}
	// Log a warn when switching to an unregistered model, reminding the user that it falls back to 128k - long essays are easy to be compressed early.
	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	window, source := h.cfg.ResolveContextWindow(model)
	bootstrap.LogContextWindowChoice(logRole, model, window, source)

	// When switching to default/coordinator, link context window and reserve of coordinator engine.
	// writer/architect/editor go through ContextManagerFactory to rebuild automatically per new model, no link needed.
	// Lacking link causes: during 1M->128k switch, coordinator engine still computes threshold by 1M,
	// accumulated messages over 128k throw API errors; during 128k->1M, threshold is pinned at 96k, wasting long context.
	//
	// Key: must use models.CurrentSelection("coordinator") to get the model "actually used by coordinator"
	// to calculate window - instead of using the switch target model directly. When user configures roles.coordinator individual model,
	// switching default doesn't affect coordinator actual model; using switch target window to SetContextWindow would mistakenly
	// tune coordinator threshold to irrelevant value (e.g. default switches to 1M model while pulling 200k coordinator
	// engine threshold to 891k, writing over 200k directly blows API).
	if h.coordinatorCtxMgr != nil && (role == "" || role == "default" || role == "coordinator") {
		_, coordinatorModel, _ := h.models.CurrentSelection("coordinator")
		coordinatorWindow, coordSource := h.cfg.ResolveContextWindow(coordinatorModel)
		h.coordinator.SetContextWindow(coordinatorWindow)
		h.coordinatorCtxMgr.SetContextWindow(coordinatorWindow)
		h.coordinatorCtxMgr.SetReserveTokens(bootstrap.CompactReserveTokens(coordinatorWindow))
		// If the coordinator's actual model differs from the switch target (user switches default but coordinator has dedicated role),
		// the above LogContextWindowChoice prints default's window, inconsistent with the actual effective value; append a line.
		if coordinatorModel != model {
			bootstrap.LogContextWindowChoice("coordinator", coordinatorModel, coordinatorWindow, coordSource)
		}
	}

	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("Model switched: %s → %s/%s", role, provider, model),
		Level:    "info",
	})
	return nil
}

// concreteThinkingRoles are the specific roles that can apply thinking strength (consistent with agents.ApplyThinking routing).
// When tuning default, re-apply one by one according to each role's ResolveThinking.
var concreteThinkingRoles = []string{"coordinator", "architect", "writer", "editor"}

// CurrentThinking returns the raw string of the currently active thinking strength of a certain role (for /model panel synchronization).
func (h *Host) CurrentThinking(role string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.ResolveThinking(strings.ToLower(strings.TrimSpace(role)))
}

// SetRoleThinking sets the thinking strength of a certain role (or default): validation -> persistence -> link to live agent -> event.
// Mirrors the structure of SwitchModel; orthogonal to model selection, can be adjusted individually. level empty = no override (inherit).
func (h *Host) SetRoleThinking(role, level string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	parsed, err := agents.ParseThinkingLevel(level)
	if err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))

	// Persistence: write Roles[role].Thinking for specific roles, and top-level Thinking for default/"".
	if role == "" || role == "default" {
		h.cfg.Thinking = string(parsed)
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.Thinking = string(parsed)
		h.cfg.Roles[role] = rc
	}
	if path := bootstrap.DefaultConfigPath(); path != "" {
		if err := bootstrap.SaveConfig(path, h.cfg); err != nil {
			slog.Warn("failed to save config", "module", "host", "err", err)
		}
	}

	// Link to live: apply to specific roles directly; for default, iterate through specific roles to reapply per ResolveThinking
	// (roles overridden at the role level keep their own, uncovered ones take the new default).
	if h.thinkingApplier != nil {
		if role == "" || role == "default" {
			for _, r := range concreteThinkingRoles {
				lv, _ := agents.ParseThinkingLevel(h.cfg.ResolveThinking(r))
				h.thinkingApplier(r, lv)
			}
		} else {
			h.thinkingApplier(role, parsed)
		}
	}

	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	shown := string(parsed)
	if shown == "" {
		shown = "Default (Inherited)"
	}
	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("Thinking strength switched: %s → %s", logRole, shown),
		Level:    "info",
	})
	return nil
}

// ── Event Replay ──

func (h *Host) ReplayQueue(afterSeq int64) ([]domain.RuntimeQueueItem, error) {
	if h.store == nil || h.store.Runtime == nil {
		return nil, nil
	}
	return h.store.Runtime.LoadQueueAfter(afterSeq)
}

// ── Co-Creation ──

// CoCreateStream cold start co-creation: clarify requirements from scratch, outputting creation instructions for the whole book.
func (h *Host) CoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, coCreateSystemPrompt, history, onProgress)
}

// StageCoCreateStream stage co-creation: plan the subsequent direction based on the written content.
// System prompt = stage prompt + current story state summary, letting the assistant know "what has been written".
func (h *Host) StageCoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, stageSystemPrompt(h.store), history, onProgress)
}

// stagePlanPrefix wraps the "subsequent direction brief" output by co-creation into a stage plan intervention, handed to Coordinator for ruling.
// Only prefix [Stage Plan] factual tag + neutral statement, do not write down "how to land it" - specific routing (compass / architect /
// save_directive) is left to the "stage planning" criteria in coordinator.md, preventing it from forming a second source of truth with prompt,
// and not blocking style requirements going via directive (holding "classification ruling belongs to LLM"). Continue then overlays [User Intervention] prefix.
const stagePlanPrefix = "[Stage Plan] I paused creation and summarized the following subsequent directions with the co-creation assistant. Please decide how to implement them based on your intervention classification, and then continue creation. Subsequent directions are as follows:\n\n"

// PauseForCoCreate enters stage co-creation: sets the co-creating occupancy flag, and pauses coordinator as well if running.
// Returns false if unable to enter (whole book completed or already in co-creation), which caller can ignore.
// The occupancy flag blocks concurrent interventions of import/simulate/start/resume/continue within the co-creation window -
// after pause during execution, lifecycle=paused, and existing ==running mutex fails, this flag fills the gap;
// already stopped (idle/paused) is also allowed to enter, resuming via Continue after planning.
func (h *Host) PauseForCoCreate() bool {
	h.mu.Lock()
	if h.cocreating || h.lifecycle == lifecycleCompleted {
		h.mu.Unlock()
		return false
	}
	h.cocreating = true
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	// If running, reuse abortWithEvent to stop (running->paused + setAborting + Abort + event), same order as manual
	// pause, no need to copy code; if already stopped (idle/paused), only set the flag, resuming via Continue after planning.
	if running {
		h.abortWithEvent("Entering stage co-creation, creation paused", "info")
	} else {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Entering stage co-creation", Level: "info"})
	}
	return true
}

// ResumeFromCoCreate ends stage co-creation: injects the subsequent direction output by co-creation as intervention and resumes creation.
// After clearing occupancy flag, reuses Continue's stop-and-inject path (subject to budget pre-constraints).
// Note: returning early when draft is empty without clearing flag is intentional (co-creation has not ended); TUI side canStart() guard
// uses the same "non-empty" criteria to guarantee this path is unreachable, and cocreating won't leak.
func (h *Host) ResumeFromCoCreate(draft string) error {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return fmt.Errorf("draft is required")
	}
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("not in co-create")
	}
	h.cocreating = false
	h.mu.Unlock()

	// Abort of PauseForCoCreate is async: wait for old run to converge before resuming, returning to the same "true stop"
	// premise as Continue after manual pause, preventing steer of resume command into the old run that is exiting. Entering co-creation
	// under non-running state (without Abort) has coordinator idle, and WaitForIdle returns immediately.
	h.coordinator.WaitForIdle()

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Stage co-creation completed, subsequent direction injected and resumed creation", Level: "info"})
	return h.Continue(stagePlanPrefix + draft)
}

// CancelCoCreate discards stage co-creation: clears occupancy flag, keeping paused state (user can continue in input box or restart Resume).
func (h *Host) CancelCoCreate() {
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return
	}
	h.cocreating = false
	h.mu.Unlock()
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Exited stage co-creation, creation remains paused (can continue in input box)", Level: "info"})
}

// ── Tools ──

func (h *Host) refreshWriterRestore() {
	if h.writerRestore != nil {
		h.writerRestore.Refresh(h.store)
	}
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// ImportFrom starts an external novel reverse import: splitting -> reverse engineering foundation -> analyzing chapter by chapter and saving to disk.
// Mutually exclusive with Coordinator; after import completes, the caller can immediately call Resume() to continue writing.
// The returned event channel is closed by imp.Run; the caller is responsible for consuming it (discard if full to prevent blocking the analysis goroutine).
func (h *Host) ImportFrom(ctx context.Context, opts imp.Options) (<-chan imp.Event, error) {
	if err := h.guardExclusive("Import"); err != nil {
		return nil, err
	}

	rulesOpts := rules.DefaultOptions(h.bundle.RulesFS)
	deps := imp.Deps{
		Store:      h.store,
		CommitTool: tools.NewCommitChapterTool(h.store).WithRules(rulesOpts),
		LLM:        h.models.ForRole("architect"),
		Prompts: imp.Prompts{
			Foundation: h.bundle.Prompts.ImportFoundation,
			Analyzer:   h.bundle.Prompts.ImportAnalyzer,
		},
	}
	return imp.Run(ctx, deps, opts)
}

// Simulate reads the simulate directory and generates or incrementally updates mimic portraits.
func (h *Host) Simulate(ctx context.Context) (<-chan sim.Event, error) {
	if err := h.guardExclusive("Generate mimic portrait"); err != nil {
		return nil, err
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	deps := sim.Deps{
		Store: h.store,
		LLM:   h.models.ForRole("architect"),
		Prompts: sim.Prompts{
			Source: h.bundle.Prompts.SimulationSource,
			Merge:  h.bundle.Prompts.SimulationMerge,
		},
	}
	return sim.Run(ctx, deps, sim.Options{SourceDir: filepath.Join(wd, "simulate")})
}

// ImportSimulationProfile imports the previously generated mimic portrait.
func (h *Host) ImportSimulationProfile(ctx context.Context, path string) (<-chan sim.Event, error) {
	if err := h.guardExclusive("Import mimic portrait"); err != nil {
		return nil, err
	}
	return sim.RunImport(ctx, h.store, path)
}

// guardExclusive checks for exclusive occupancy: rejects entry points that rewrite state when coordinator is running or in stage co-creation.
// Fills the concurrency gap of checking only ==running during paused state.
func (h *Host) guardExclusive(action string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.lifecycle == lifecycleRunning:
		return fmt.Errorf("coordinator is running, please pause before performing %s", action)
	case h.cocreating:
		return fmt.Errorf("stage co-creation is in progress, please exit co-creation before performing %s", action)
	}
	return nil
}

// Export exports completed chapters as external files (currently only TXT is supported).
//
// Different from ImportFrom: export is a read-only operation (does not touch Progress / Checkpoint),
// therefore **does not require Coordinator to be idle** - "current stage product" can be exported at any time during writing.
// Only reads a consistent snapshot of Progress.CompletedChapters + final chapters + outline + premise.
func (h *Host) Export(ctx context.Context, opts exp.Options) (*exp.Result, error) {
	return exp.Run(ctx, exp.Deps{Store: h.store}, opts)
}
