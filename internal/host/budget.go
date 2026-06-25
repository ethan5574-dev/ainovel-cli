package host

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// Budget state machine: monotonically advancing, each transition triggers side effects exactly once, no rollbacks.
// Raising budget = user re-authorization = restart after config change or a new Host instance; does not rollback within this instance.
const (
	budgetNormal      int32 = iota // Alarm water level not reached
	budgetWarned                   // Alarm emitted, limit not crossed
	budgetStopPending              // Limit crossed, waiting for sub-agent boundary stop
	budgetStopped                  // Stop executed
)

// BudgetSentinel monitors cumulative cost and enforces the user's budget policy (config budget block).
//
// Architectural positioning (architecture.md §8.3/§10): Does not evaluate model behavior—stopping when crossing the limit
// is equivalent to the user manually aborting at that moment; Host is just executing a pre-signed instruction. It impacts
// the control flow, so it is not an observer; it is positioned as a Host policy component on par with flow.Dispatcher.
// The Route/tool layer is unaware.
//
// Stopping timing: By default, at the sub-agent boundary (HandleEvent listens for EventToolExecEnd(tool=subagent),
// the same trigger point as Dispatcher), not wasting in-flight chapters; if hardStop=true, it stops immediately when crossing the limit.
// Subscription order constraint: Sentinel must register before Dispatcher—after Abort is set, Dispatcher's FollowUp naturally
// fails, removing the need for budget awareness at the routing layer.
type BudgetSentinel struct {
	limit     float64
	warnRatio float64
	hardStop  bool

	costNow func() float64             // Current cumulative cost (wrapped by usage.Totals; test stub injectable)
	abort   func(reason string)        // Host stop wrapper (with cause event)
	report  func(level, summary string) // Alarm output (emitEvent + notify, injected by Host)

	state atomic.Int32

	// Billing blind zone detection: For models where the registry has no price and the provider doesn't report cost, each transaction's
	// cost increment is $0, silently invalidating the budget. Judging by "consecutive zero increments" instead of total==0—the latter
	// cannot catch cases where the model switches to a priceless model mid-run (total stops at a non-zero historical value but stops growing).
	// Free models will also trigger this, and the hint "budget will not trigger" is equally valid for them.
	lastTotal   atomic.Uint64 // math.Float64bits(last callback's cumulative cost)
	zeroStreak  atomic.Int32
	blindWarned atomic.Bool
}

// blindZeroStreak alarm triggers after how many consecutive zero-increment transactions.
// Normal pricing models must have increments > 0 (cost is a float accumulation, not rounded).
// 5 is chosen only to prevent extreme glitches, not an adjustable policy threshold.
const blindZeroStreak = 5

// NewBudgetSentinel creates a budget sentinel; returns nil if policy is not enabled (all methods are nil-safe).
func NewBudgetSentinel(cfg bootstrap.BudgetConfig, costNow func() float64, abort func(reason string), report func(level, summary string)) *BudgetSentinel {
	if !cfg.Enabled() {
		return nil
	}
	return &BudgetSentinel{
		limit:     cfg.BookUSD,
		warnRatio: cfg.WarnRatio,
		hardStop:  cfg.HardStop,
		costNow:   costNow,
		abort:     abort,
		report:    report,
	}
}

// OnCost is called by UsageTracker after each billing with the latest cumulative cost (outside lock).
// One callback may transition across two levels (normal->warned->stopPending), triggering side effects once for each level.
func (s *BudgetSentinel) OnCost(total float64) {
	if s == nil {
		return
	}
	if prev := s.lastTotal.Swap(math.Float64bits(total)); total == math.Float64frombits(prev) {
		if s.zeroStreak.Add(1) >= blindZeroStreak && s.blindWarned.CompareAndSwap(false, true) {
			s.report("warn", fmt.Sprintf("Budget blind spot: Billing occurred consecutively but cumulative cost is stuck at $%.2f and no longer growing (either because the current model has no price in the registry and the provider did not report cost, or it is a free model)—budget limit will not trigger", total))
		}
	} else {
		s.zeroStreak.Store(0)
	}
	if total >= s.limit*s.warnRatio && s.state.CompareAndSwap(budgetNormal, budgetWarned) {
		s.report("warn", fmt.Sprintf("Budget warning: Spent $%.2f, reaching %.0f%% of the $%.2f budget", total, s.limit, s.warnRatio*100))
	}
	if total >= s.limit && s.state.CompareAndSwap(budgetWarned, budgetStopPending) {
		if s.hardStop {
			s.report("error", fmt.Sprintf("Budget exhausted: Spent $%.2f, exceeding the $%.2f budget, stopping immediately", total, s.limit))
			s.stop(total)
			return
		}
		s.report("error", fmt.Sprintf("Budget exhausted: Spent $%.2f, exceeding the $%.2f budget, will stop after the current sub-agent task ends", total, s.limit))
	}
}

// HandleEvent executes pending stops at sub-agent boundaries. Subscriptions must precede the Dispatcher.
// Do not skip IsError—an error response is also a boundary; stopping should not be postponed due to sub-agent failure.
func (s *BudgetSentinel) HandleEvent(ev agentcore.Event) {
	if s == nil {
		return
	}
	if ev.Type != agentcore.EventToolExecEnd || ev.Tool != "subagent" {
		return
	}
	if s.state.Load() != budgetStopPending {
		return
	}
	s.stop(s.costNow())
}

func (s *BudgetSentinel) stop(total float64) {
	if s.state.CompareAndSwap(budgetStopPending, budgetStopped) {
		s.abort(fmt.Sprintf("Budget stop: Spent $%.2f, exceeding budget $%.2f; resume by raising budget.book_usd", total, s.limit))
	}
}

// Refuse is the pre-start check: returns refusal error if budget is exceeded (called by Start/Resume/Continue recovery paths).
// Raising budget = re-authorization; Refuse naturally permits starting under the new configuration.
func (s *BudgetSentinel) Refuse() error {
	if s == nil {
		return nil
	}
	if cost := s.costNow(); cost >= s.limit {
		return fmt.Errorf("This book has spent $%.2f, reaching the budget limit of $%.2f; please raise budget.book_usd and retry", cost, s.limit)
	}
	return nil
}

// Limit returns the budget limit (for UI display); returns 0 if disabled.
func (s *BudgetSentinel) Limit() float64 {
	if s == nil {
		return 0
	}
	return s.limit
}
