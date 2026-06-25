package reminder

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// StopGuard is the last line of defense against an impossible early stop.
// When the LLM tries to end_turn:
//   - Progress.Phase = Complete -> allow
//   - otherwise inject a user message so the agent continues to the next turn
//   - if the block happens more than maxConsecutive times in a row -> escalate and terminate the run
//
// The guard maintains a consecutive block counter and resets it after a successful allow or injection.
// Reminder + Prompt actually drive Coordinator behavior; StopGuard is only a fallback.
const maxConsecutiveBlocks = 5

// NewStopGuard constructs the Coordinator-specific StopGuard.
// onBlock is optional; when non-nil it is called on every block for auditing.
func NewStopGuard(st *store.Store, onBlock func(reason string, consecutive int32)) agentcore.StopGuard {
	var consecutive atomic.Int32
	var lastBlockTurn atomic.Int64 // TurnIndex of the last block; -1 means no block has happened yet
	lastBlockTurn.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		progress, _ := st.Progress.Load()
		if progress != nil && progress.Phase == domain.PhaseComplete {
			consecutive.Store(0)
			lastBlockTurn.Store(-1)
			return agentcore.StopDecision{Allow: true}
		}
		// Only consecutive blocked turns are counted; otherwise treat it as a new round
		// (the LLM already made tool progress, or user injection / resume rewound TurnIndex).
		last := lastBlockTurn.Load()
		if last < 0 || int64(info.TurnIndex) != last+1 {
			consecutive.Store(0)
		}
		lastBlockTurn.Store(int64(info.TurnIndex))
		n := consecutive.Add(1)
		if n > maxConsecutiveBlocks {
			slog.Error("stop_guard consecutive block limit exceeded, escalating to termination",
				"module", "host.reminder", "turn", info.TurnIndex, "consecutive", n)
			if onBlock != nil {
				onBlock("escalated", n)
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		inject := "Do not end the conversation. Phase is not Complete yet; continue with the next step (check novel_context or call a subagent)."
		if progress != nil && len(progress.PendingRewrites) > 0 {
			inject = fmt.Sprintf("Do not end the conversation. Pending rewrites remain: %v. Call writer immediately.", progress.PendingRewrites)
		}
		slog.Warn("stop_guard intercepted end_turn",
			"module", "host.reminder", "turn", info.TurnIndex, "consecutive", n)
		if onBlock != nil {
			onBlock("blocked", n)
		}
		return agentcore.StopDecision{Allow: false, InjectMessage: inject}
	}
}
