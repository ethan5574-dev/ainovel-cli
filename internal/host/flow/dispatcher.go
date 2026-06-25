package flow

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/voocel/agentcore"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// Dispatcher subscribes to Coordinator events, computes routing when subagents return,
// and issues Host directives.
//
// Lifecycle: Attach returns a detach function; call it when shutting down the Host to
// release the subscription.
type Dispatcher struct {
	coordinator *agentcore.Agent
	store       *storepkg.Store

	enabled atomic.Bool // Controlled by Host; dispatching should stay off until startup completes.

	// Repeat tracking: remember the most recent Agent+Task pair and how many times it was
	// issued consecutively.
	// When the same instruction is recomputed repeatedly (the subagent returned, state did
	// not advance, and Route yields the same result), we do not swallow it silently.
	// Instead, we re-send it with count facts attached. "The routing result stayed the same
	// for N consecutive turns" is a fact only the Host can observe.
	// If we stay silent, the Coordinator gets trapped between "do not decide the next step
	// on its own" (coordinator.md) and "do not stop" (StopGuard), which turns into a
	// freelance-style dead loop.
	// The final judgment still belongs to the LLM: the resent message only adds facts and
	// permission to verify, with no threshold and no circuit breaker (architecture §10.13).
	// Because the message includes the count, each resend is distinct and we do not push
	// duplicate literal instructions into followUpQ.
	lastMu   sync.Mutex
	lastSent *Instruction
	repeats  int

	// onRepeat is a pure telemetry callback (for unattended alerts). It fires once when the
	// same instruction is issued for the repeatNotifyAt-th time. It does not affect dispatch,
	// and the dispatch logic is unaware of its existence.
	onRepeat func(agent, task string, n int)
}

// repeatNotifyAt is hard-coded rather than configurable: it is not a control-flow threshold
// (it triggers no action, it only calls for attention), so exposing it in config would imply
// behavior that can be tuned for semantic changes.
const repeatNotifyAt = 3

// NewDispatcher creates a Dispatcher. Call Attach before use to subscribe to events.
func NewDispatcher(coordinator *agentcore.Agent, store *storepkg.Store) *Dispatcher {
	d := &Dispatcher{coordinator: coordinator, store: store}
	return d
}

// Enable turns on route dispatching; when disabled, EventToolExecEnd does not emit FollowUp.
// Host enables this only after the first prompt is complete in Start/Resume to avoid
// interfering with startup flow.
func (d *Dispatcher) Enable() { d.enabled.Store(true) }

// Attach subscribes to Coordinator events; call the returned function on shutdown to unbind.
func (d *Dispatcher) Attach() func() {
	return d.coordinator.Subscribe(d.handle)
}

func (d *Dispatcher) handle(ev agentcore.Event) {
	if !d.enabled.Load() {
		return
	}
	// Exact trigger point: a subagent returns successfully, or reopen_book reopens a finished
	// book into rework mode.
	// Both advance the fact layer and require an immediate Route call to compute the next
	// step. reopen_book is not a subagent (during complete-phase handling it bypasses
	// completePhaseGate), and if we do not trigger here, the reopened rework queue has no
	// dispatcher.
	// We do not use EventModelResponse because agentcore emits it after every LLM call, which
	// would push the same instruction into followUpQ repeatedly. Query-style Steer is
	// constrained by coordinator.md to keep calling subagent within the same turn, which hits
	// this trigger point.
	if ev.Type != agentcore.EventToolExecEnd || ev.IsError {
		return
	}
	if ev.Tool != "subagent" && ev.Tool != "reopen_book" {
		return
	}
	d.Dispatch()
}

// Dispatch computes routing immediately and issues the instruction; Host may call it
// directly at special moments such as after Resume.
func (d *Dispatcher) Dispatch() {
	state := LoadState(d.store)
	inst := Route(state)
	if inst == nil {
		return
	}
	n := d.trackRepeat(inst)
	// Writer task: mark the chapter in progress at dispatch time so the right-hand outline
	// updates to "▸ In progress" immediately.
	// We do not wait for plan_chapter to actually run (it will call StartChapter again, which
	// is idempotent).
	if inst.Agent == "writer" && inst.Chapter > 0 && d.store != nil {
		if err := d.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			slog.Error("flow router refuses invalid writer dispatch", "module", "host.flow", "chapter", inst.Chapter, "err", err)
			return
		}
		if err := d.store.Progress.StartChapter(inst.Chapter); err != nil {
			slog.Warn("flow router pre-mark in-progress failed", "module", "host.flow", "chapter", inst.Chapter, "err", err)
		}
	}
	msg := formatDispatchMessage(inst, n)
	slog.Debug("flow router dispatch", "module", "host.flow", "agent", inst.Agent, "reason", inst.Reason, "repeat", n)
	d.coordinator.FollowUp(agentcore.UserMsg(msg))
}

// formatDispatchMessage assembles the directive message sent to the Coordinator.
// When n>1, it appends repeat facts, telling the model that the routing facts have not
// changed since the last dispatch and allowing it to verify before choosing between the
// original path or a different subagent. The Host does not force any branch here.
func formatDispatchMessage(inst *Instruction, n int) string {
	msg := FormatMessage(inst)
	if n > 1 {
		msg += fmt.Sprintf("\n(Note: this instruction has been issued %d times. The routing facts have not changed since the last dispatch. This time you may call novel_context first to verify facts, then decide whether to continue as planned or reassign to another subagent.)", n)
	}
	return msg
}

// SetOnRepeat registers a telemetry callback for repeated instructions. Call it once
// before Attach/dispatch starts.
func (d *Dispatcher) SetOnRepeat(cb func(agent, task string, n int)) {
	d.onRepeat = cb
}

// trackRepeat records how many times the same instruction has been issued consecutively and
// returns the current count (1 = new instruction).
// It compares Agent+Task only, not Reason, because Reason is auxiliary text for humans.
// When the count reaches repeatNotifyAt, it triggers onRepeat once outside the lock
// (and re-arms after the key changes and counting restarts).
func (d *Dispatcher) trackRepeat(next *Instruction) int {
	d.lastMu.Lock()
	if d.lastSent != nil && d.lastSent.Agent == next.Agent && d.lastSent.Task == next.Task {
		d.repeats++
	} else {
		cp := *next
		d.lastSent = &cp
		d.repeats = 1
	}
	n := d.repeats
	d.lastMu.Unlock()

	if n == repeatNotifyAt && d.onRepeat != nil {
		d.onRepeat(next.Agent, next.Task, n)
	}
	return n
}

// ResetRepeat clears repeat tracking. Host calls this on Resume / new Start so the first
// instruction after recovery or a fresh start is issued with "first time" semantics.
func (d *Dispatcher) ResetRepeat() {
	d.lastMu.Lock()
	defer d.lastMu.Unlock()
	d.lastSent = nil
	d.repeats = 0
}
