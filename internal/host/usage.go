package host

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/models"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// recentSampleCap is the sliding window size: keeps only the recent N calls' (cacheRead, input)
// samples for each role, used in the left panel to compare "cumulative vs recent N" hit rates, identifying "early drags" vs "steady-state low hits".
const recentSampleCap = 10

// UsageTracker accumulates LLM input/output tokens and USD cost of all agents throughout the session.
//
// Working mechanism:
//   - Triggered by calling Record(agentName, msg) on each agent's OnMessage callback.
//   - agentName is mapped to a role (architect_* is normalized to architect), and the model currently bound to that role is retrieved from ModelSet.
//   - Uses models.DefaultRegistry to look up model pricing, multiplying the 4 categories: non-cached input, output, cache read, and cache write.
//   - If the model is not in the registry, falls back to msg.Usage.Cost.Total (supplied by provider, which may be 0).
//   - Hot switching models (/model) dynamically prices subsequent messages using the new model, while retaining old costs for older messages.
//
// Also maintains per-role dimensions (writer/editor/architect/coordinator):
//   - Accumulated hit data -> overall optimization effects
//   - Sliding window for recent N times -> distinguishes early drags vs steady-state low hit rates
//   - CacheCapable flag -> distinguishes "not enabled" from "truly 0% hit rate"
//
// Thread-safe.
type UsageTracker struct {
	mu       sync.Mutex
	overall  agentTotals
	perAgent map[string]*agentTotals // key is the normalized role name returned by agentRoleName
	perModel map[string]*agentTotals // key is provider/model; falls back to model if provider is unknown
	modelSet *bootstrap.ModelSet
	store    *storepkg.Store // can be nil (test scenarios); if nil, all persistence methods are silent noops

	// missingAssistantUsage accumulates the number of times an assistant message is received but Usage is nil.
	// This mainly occurs when a self-built OpenAI-compatible backend does not send the final usage chunk at the end
	// of streaming according to the OpenAI stream_options.include_usage protocol—partial.Usage is always nil,
	// and all accumulated fields stop at 0. This counter allows the UI to directly tell the user "the upstream is not returning
	// usage, it's not broken here", rather than having them debug the cache panel code.
	missingAssistantUsage int
	loggedMissingUsage    bool // warns only once per session to avoid flooding tui.log

	// saveCh is triggered non-blockingly by Record after accumulation; autoSaveLoop listens and writes to disk via debounce.
	// buffered=1: multiple consecutive Records fold into a single write signal; if full, it is dropped directly and written in the next tick.
	saveCh chan struct{}

	// onCost is called outside the lock after each billing with the latest accumulated cost (BudgetSentinel threshold detection).
	// Must be set via SetOnCost before concurrent Records start; read-only afterwards.
	onCost func(total float64)

	// onMissingUsage is called once when "assistant message has no Usage" is first detected (at the same time as slog warn).
	// When budget is enabled, this indicates a billing blind spot—cost is always 0 and budget is never triggered, so human intervention is required.
	onMissingUsage func()
}

// usageSample is a single OnMessage hit sample, recording only the numerator and denominator of the hit rate.
type usageSample struct {
	CacheRead int
	Input     int
}

// agentTotals is the accumulated count for an agent.
//   - Saved is the difference calculated back from the current hit data: "if charged at the non-cached price"
//   - CacheCapable is set to true only after the role has undergone at least one call to a "known model supporting cache"
//   - samples is a fixed-length ring buffer; the first recentSampleCap times are appended directly, and rotated by sampleIdx afterwards
type agentTotals struct {
	Input        int
	Output       int
	CacheRead    int
	CacheWrite   int
	Cost         float64
	Saved        float64
	CacheCapable bool
	samples      []usageSample
	sampleIdx    int
}

func NewUsageTracker(set *bootstrap.ModelSet, store *storepkg.Store) *UsageTracker {
	return &UsageTracker{
		modelSet: set,
		store:    store,
		perAgent: make(map[string]*agentTotals, 4),
		perModel: make(map[string]*agentTotals, 4),
		saveCh:   make(chan struct{}, 1),
	}
}

// Record distributes an agent message to both accumulation and diagnostic paths.
//
// Accumulation only checks if Usage exists—"which message carries Usage" is an assembly detail of
// agentcore/litellm adapter (the upstream protocol puts usage at the top level of the response); if assembly rules change in the future, this doesn't need to change.
// Diagnostics require Role=Assistant and non-empty Content to avoid polluting missingAssistantUsage counts with AbortMsg / exception recovery / tool / user messages.
func (t *UsageTracker) Record(agentName string, msg agentcore.AgentMessage) {
	if t == nil {
		return
	}
	m, ok := msg.(agentcore.Message)
	if !ok {
		return
	}
	if m.Usage == nil {
		if m.Role == agentcore.RoleAssistant && len(m.Content) > 0 {
			t.flagMissingUsage(agentName)
		}
		return
	}
	role := agentRoleName(agentName)
	provider, modelName := usageActualModel(m.Usage)
	t.accumulate(role, provider, modelName, *m.Usage)
}

func usageActualModel(u *agentcore.Usage) (provider, modelName string) {
	if u == nil {
		return "", ""
	}
	return strings.TrimSpace(u.Provider), strings.TrimSpace(u.Model)
}

// flagMissingUsage accumulates a "looks like a real LLM response but failed to get usage" event.
// It warns only once per session to avoid flooding the tui.log.
func (t *UsageTracker) flagMissingUsage(agentName string) {
	t.mu.Lock()
	t.missingAssistantUsage++
	shouldLog := !t.loggedMissingUsage
	t.loggedMissingUsage = true
	t.mu.Unlock()
	if shouldLog {
		slog.Warn("LLM response did not carry usage data, cache/cost panel will have no accumulation - usually because upstream streaming did not send the final usage chunk according to the OpenAI include_usage protocol",
			"module", "usage", "agent", agentName)
		if t.onMissingUsage != nil {
			t.onMissingUsage()
		}
	}
	t.notifyDirty()
}

// SetOnMissingUsage registers a one-time callback for "first discovery of missing usage".
// Must be called once during Host construction, before concurrent Records start.
func (t *UsageTracker) SetOnMissingUsage(cb func()) {
	if t == nil {
		return
	}
	t.onMissingUsage = cb
}

// notifyDirty triggers a dirty write signal non-blockingly, which is actually written by autoSaveLoop via debounce.
// Signal channel buffered=1: multiple consecutive Records fold into a single save request.
func (t *UsageTracker) notifyDirty() {
	if t == nil || t.saveCh == nil {
		return
	}
	select {
	case t.saveCh <- struct{}{}:
	default:
	}
}

// accumulate accumulates a message carrying Usage into three counters: overall, per-role, and per-model.
// Empty provider/model means "get the corresponding model for the role using the current ModelSet" (real-time path);
// non-empty means "force cost calculation using the specified model" (replay path using _meta in session jsonl).
// resolveCost is executed outside the lock (it only reads modelSet/Registry), and inside the lock it only performs addition.
func (t *UsageTracker) accumulate(role, provider, modelName string, u agentcore.Usage) {
	provider, modelName = t.effectiveModel(role, provider, modelName)
	cost, saved, capable := t.resolveCost(modelName, u)

	t.mu.Lock()
	addUsage(&t.overall, u, cost, saved, capable)

	per := t.perAgent[role]
	if per == nil {
		per = &agentTotals{}
		t.perAgent[role] = per
	}
	addUsage(per, u, cost, saved, capable)

	if key := modelUsageKey(provider, modelName); key != "" {
		perModel := t.perModel[key]
		if perModel == nil {
			perModel = &agentTotals{}
			t.perModel[key] = perModel
		}
		addUsage(perModel, u, cost, saved, capable)
	}
	total := t.overall.Cost
	t.mu.Unlock()

	t.notifyDirty()
	if t.onCost != nil {
		t.onCost(total)
	}
}

// SetOnCost registers a billing callback (called outside the lock carrying the latest accumulated cost).
// Must be called once during Host construction, before concurrent Records start.
func (t *UsageTracker) SetOnCost(cb func(total float64)) {
	if t == nil {
		return
	}
	t.onCost = cb
}

func (t *UsageTracker) effectiveModel(role, provider, modelName string) (string, string) {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		if t != nil && t.modelSet != nil {
			p, m, _ := t.modelSet.CurrentSelection(role)
			return p, m
		}
		return "", ""
	}
	if provider == "" && t != nil && t.modelSet != nil {
		p, m, _ := t.modelSet.CurrentSelection(role)
		if m == modelName {
			provider = p
		}
	}
	return provider, modelName
}

func modelUsageKey(provider, modelName string) string {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	switch {
	case modelName == "":
		return ""
	case provider == "":
		return modelName
	default:
		return provider + "/" + modelName
	}
}

// addUsage adds the tokens and cost of a single call to a totals structure.
// Must be called while holding UsageTracker.mu.
//
// CacheCapable prioritizes "facts" for determination: as long as CacheRead or CacheWrite > 0 has been seen,
// it proves that the upstream indeed performed prompt caching. The registry's CacheReadCostPer1M is only a fallback,
// because self-built backend models (like mimo-v2.5-pro or local proxies) are usually not in the BerriAI/litellm
// pricing index, but their actual Usage fully contains cache data, and the UI should not misjudge it as "disabled".
func addUsage(t *agentTotals, u agentcore.Usage, cost, saved float64, capable bool) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Cost += cost
	t.Saved += saved
	if capable || u.CacheRead > 0 || u.CacheWrite > 0 {
		t.CacheCapable = true
	}
	pushSample(t, u.CacheRead, u.Input)
}

// pushSample pushes a sample to the ring buffer. The first recentSampleCap times are pure appends, and rotated and overwritten afterwards.
func pushSample(t *agentTotals, cacheRead, input int) {
	s := usageSample{CacheRead: cacheRead, Input: input}
	if len(t.samples) < recentSampleCap {
		t.samples = append(t.samples, s)
		return
	}
	t.samples[t.sampleIdx] = s
	t.sampleIdx = (t.sampleIdx + 1) % recentSampleCap
}

// recentSums returns the sum of cacheRead and input in the sliding window, as the numerator and denominator of the "recent N hit rate".
// Uses sum/sum instead of "average of individual rates" to avoid magnifying noise from small samples (input of a few hundred tokens).
func recentSums(t *agentTotals) (cacheRead, input int) {
	for _, s := range t.samples {
		cacheRead += s.CacheRead
		input += s.Input
	}
	return cacheRead, input
}

// Totals returns a snapshot of the accumulated totals.
func (t *UsageTracker) Totals() (cost float64, input, output, cacheRead, cacheWrite int) {
	if t == nil {
		return 0, 0, 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Cost, t.overall.Input, t.overall.Output, t.overall.CacheRead, t.overall.CacheWrite
}

// SavedUSD returns the accumulated USD saved due to cache hits.
func (t *UsageTracker) SavedUSD() float64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Saved
}

// OverallRecent returns the cacheRead sum, input sum, and sample count in the sliding window (<= recentSampleCap).
func (t *UsageTracker) OverallRecent() (cacheRead, input, samples int) {
	if t == nil {
		return 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, in := recentSums(&t.overall)
	return r, in, len(t.overall.samples)
}

// OverallCacheCapable returns whether the system as a whole has undergone at least one call to a known model supporting cache.
func (t *UsageTracker) OverallCacheCapable() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheCapable
}

// MissingAssistantUsage returns the accumulated count of times "assistant message was received but Usage was nil".
// Greater than 0 usually means that the upstream streaming did not send OpenAI's final usage chunk,
// based on which the UI shows a hint rather than mistakenly believing that the cache module itself is broken.
func (t *UsageTracker) MissingAssistantUsage() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.missingAssistantUsage
}

// ── Persistence ──

// Snapshot copies the current accumulated state into a serializable domain.UsageState.
// The sliding window samples do not enter the snapshot—it is a short-term diagnostic window, which has little cross-process significance.
func (t *UsageTracker) Snapshot() domain.UsageState {
	if t == nil {
		return domain.UsageState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := domain.UsageState{
		Schema:       domain.UsageSchemaVersion,
		UpdatedAt:    time.Now(),
		Overall:      totalsSnapshot(&t.overall),
		PerAgent:     make(map[string]domain.AgentUsageTotals, len(t.perAgent)),
		PerModel:     make(map[string]domain.AgentUsageTotals, len(t.perModel)),
		MissingUsage: t.missingAssistantUsage,
	}
	for role, v := range t.perAgent {
		state.PerAgent[role] = totalsSnapshot(v)
	}
	for model, v := range t.perModel {
		state.PerModel[model] = totalsSnapshot(v)
	}
	return state
}

// LoadFromStore reads the persisted snapshot from store.Usage and backfills it into memory. Returns true
// if a non-empty state (with matching schema) was successfully loaded; false if there is no file or if it's unusable,
// in which case the caller should continue with a one-time session replay backfill.
func (t *UsageTracker) LoadFromStore() (bool, error) {
	if t == nil || t.store == nil {
		return false, nil
	}
	state, err := t.store.Usage.Load()
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}
	t.applyState(*state)
	return true, nil
}

// SaveNow immediately persists the current snapshot to disk. Both the autoSaveLoop and Close paths write through it.
func (t *UsageTracker) SaveNow() error {
	if t == nil || t.store == nil {
		return nil
	}
	return t.store.Usage.Save(t.Snapshot())
}

// StartAutoSave starts a goroutine to listen to saveCh + debounce for persisting. Before ctx is done, it
// flushes the last unsaved state. Close triggers flush + exit by cancelling ctx.
func (t *UsageTracker) StartAutoSave(ctx context.Context) {
	if t == nil || t.store == nil {
		return
	}
	go t.autoSaveLoop(ctx)
}

// autoSaveLoop throttles high-frequency dirty signals into a single persistence write every 500ms.
//
// Design note: 500ms is an empirical value—1-2 LLM turns per chapter means writing to disk 1-2 times is completely acceptable;
// even if the user manually presses ctrl+C and there is no time to trigger the timer, the ctx cancellation path will flush the last write.
// A real crash (OS kill -9) will lose at most the last 0.5s of accumulation—the upstream session jsonl is still
// the full ground truth, and the next startup will repair the difference by replaying from sessions/.
func (t *UsageTracker) autoSaveLoop(ctx context.Context) {
	const debounce = 500 * time.Millisecond
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	var pending bool
	flush := func() {
		if err := t.SaveNow(); err != nil {
			slog.Warn("usage save failed", "module", "usage", "err", err)
		}
		pending = false
	}
	for {
		select {
		case <-ctx.Done():
			if pending {
				flush()
			}
			return
		case <-t.saveCh:
			if pending {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			timer.Reset(debounce)
			pending = true
		case <-timer.C:
			flush()
		}
	}
}

// applyState writes the persisted snapshot back to memory. Called only during startup (after LoadFromStore / replay),
// during which autoSaveLoop has not started yet and Record will not trigger concurrently, so locks are not strictly necessary;
// however, mu is kept to prevent concurrency issues from testing or future invocation sequence changes.
func (t *UsageTracker) applyState(state domain.UsageState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.overall = totalsFromState(state.Overall)
	if state.PerAgent == nil {
		t.perAgent = make(map[string]*agentTotals, 4)
	} else {
		t.perAgent = make(map[string]*agentTotals, len(state.PerAgent))
		for role, v := range state.PerAgent {
			tot := totalsFromState(v)
			t.perAgent[role] = &tot
		}
	}
	if state.PerModel == nil {
		t.perModel = make(map[string]*agentTotals, 4)
	} else {
		t.perModel = make(map[string]*agentTotals, len(state.PerModel))
		for model, v := range state.PerModel {
			tot := totalsFromState(v)
			t.perModel[model] = &tot
		}
	}
	t.missingAssistantUsage = state.MissingUsage
}

// totalsSnapshot copies memory agentTotals into a serializable domain.AgentUsageTotals.
// The samples ring buffer is deliberately not carried out—see the comment on UsageState.
func totalsSnapshot(t *agentTotals) domain.AgentUsageTotals {
	if t == nil {
		return domain.AgentUsageTotals{}
	}
	return domain.AgentUsageTotals{
		Input:        t.Input,
		Output:       t.Output,
		CacheRead:    t.CacheRead,
		CacheWrite:   t.CacheWrite,
		Cost:         t.Cost,
		Saved:        t.Saved,
		CacheCapable: t.CacheCapable,
	}
}

// totalsFromState restores the persisted form back to memory agentTotals. samples is left empty, and will start
// accumulating from 0 again upon restart; the "recent N hit rate" semantics will be restored after a few Record rounds.
func totalsFromState(s domain.AgentUsageTotals) agentTotals {
	return agentTotals{
		Input:        s.Input,
		Output:       s.Output,
		CacheRead:    s.CacheRead,
		CacheWrite:   s.CacheWrite,
		Cost:         s.Cost,
		Saved:        s.Saved,
		CacheCapable: s.CacheCapable,
	}
}

// AgentUsage is an accumulated usage snapshot of an agent (exposed to UI).
type AgentUsage struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

// PerAgent returns the accumulated usage for each role. The results are sorted in descending order by CacheRead quantity, skipping roles that have not consumed any tokens.
func (t *UsageTracker) PerAgent() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perAgent))
	for role, v := range t.perAgent {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		recentRead, recentInput := recentSums(v)
		out = append(out, AgentUsage{
			Role:            role,
			Input:           v.Input,
			Output:          v.Output,
			CacheRead:       v.CacheRead,
			CacheWrite:      v.CacheWrite,
			Cost:            v.Cost,
			Saved:           v.Saved,
			CacheCapable:    v.CacheCapable,
			RecentCacheRead: recentRead,
			RecentInput:     recentInput,
			RecentSamples:   len(v.samples),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CacheRead != out[j].CacheRead {
			return out[i].CacheRead > out[j].CacheRead
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// PerModel returns the accumulated usage of each model. The results are sorted in descending order of cost, followed by descending order of input tokens.
func (t *UsageTracker) PerModel() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perModel))
	for model, v := range t.perModel {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		out = append(out, AgentUsage{
			Model:        model,
			Input:        v.Input,
			Output:       v.Output,
			CacheRead:    v.CacheRead,
			CacheWrite:   v.CacheWrite,
			Cost:         v.Cost,
			Saved:        v.Saved,
			CacheCapable: v.CacheCapable,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// resolveCost returns the cost, saved cost, and cache capability of the current message.
//   - cost: Multiplied by the 4 items if the registry matches; falls back to the provider's own cost if not matched
//   - saved: > 0 only if the registry matches, CacheRead > 0, and InputCost > CacheReadCost
//   - capable: true if the registry matches and the model's CacheReadCostPer1M > 0 -> known to support prompt caching
//
// modelName prioritizes the caller's input (replay uses _meta.model from the session jsonl).
func (t *UsageTracker) resolveCost(modelName string, u agentcore.Usage) (cost, saved float64, capable bool) {
	if entry, ok := models.DefaultRegistry().Resolve(modelName); ok {
		c := computeCost(u, *entry)
		s := computeSaved(u, *entry)
		canCache := entry.CacheReadCostPer1M > 0
		if c > 0 {
			return c, s, canCache
		}
	}
	if u.Cost != nil {
		return u.Cost.Total, 0, false
	}
	return 0, 0, false
}

// agentRoleName normalizes the sub-agent name to a role name.
// architect_short/mid/long are all normalized to architect; others are returned as is.
func agentRoleName(agentName string) string {
	if strings.HasPrefix(agentName, "architect_") {
		return "architect"
	}
	return agentName
}

// computeCost calculates the USD cost of this call according to the price per 1M tokens.
//
// Semantic premise (unified by all litellm providers, see Anthropic/Bedrock/OpenAI/Gemini/Compat adapter assembly points in agentcore):
//
//	u.Input  = Total input tokens, **including** CacheRead; excluding CacheWrite
//	u.Output = Output tokens
//
// Therefore nonCachedInput = u.Input - u.CacheRead holds true for all providers.
// The fallback branch is kept in case a provider returns dirty data in the future.
func computeCost(u agentcore.Usage, e models.ModelEntry) float64 {
	nonCachedInput := u.Input - u.CacheRead
	if nonCachedInput < 0 {
		nonCachedInput = u.Input
	}
	c := 0.0
	c += float64(nonCachedInput) * e.InputCostPer1M / 1_000_000
	c += float64(u.Output) * e.OutputCostPer1M / 1_000_000
	c += float64(u.CacheRead) * e.CacheReadCostPer1M / 1_000_000
	c += float64(u.CacheWrite) * e.CacheWriteCostPer1M / 1_000_000
	return c
}

// computeSaved estimates the USD saved by the CacheRead hits compared to billing at the normal input price.
// Note that the premium for CacheWrite is not deducted—it is a necessary investment for "paving the way for subsequent hits",
// and the actual return is recovered through subsequent CacheRead hits.
func computeSaved(u agentcore.Usage, e models.ModelEntry) float64 {
	if u.CacheRead <= 0 || e.InputCostPer1M <= 0 {
		return 0
	}
	delta := e.InputCostPer1M - e.CacheReadCostPer1M
	if delta <= 0 {
		return 0
	}
	return float64(u.CacheRead) * delta / 1_000_000
}
