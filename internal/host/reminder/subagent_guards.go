package reminder

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// subagentMaxConsecutiveBlocks escalates to termination after N consecutive blocks, preventing weak model dead loops.
const subagentMaxConsecutiveBlocks = 3

// hardStopReasons are provider-side rejection reasons that cannot be recovered with prompt messages. Injected
// messages like "must commit" are ineffective for them. Instead, they consume tokens for a complete LLM call each time,
// and eventually escalate to let the coordinator redeploy the entire SubAgent, accumulating multiple times of waste
// (in testing, when ch02 hit safety, writing a chapter generated 3 redeploys and 17 LLM calls, and the hit rate
// dropped from 50% to 2.8%).
//
// Note that StopReasonError / StopReasonAborted do not need to be listed: agentcore terminates the run directly
// when receiving these two stop reasons in loop.go, and won't invoke StopGuard.
// Only provider rejection semantics that actually reach StopGuard are listed here.
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard constructs a StopGuard:
// If a checkpoint of the specified step does not appear after baseline, end_turn is rejected.
// baseline is captured by the caller at factory time, ensuring correct per-run semantics.
func newCheckpointDeltaGuard(st *store.Store, agentName string, requiredSteps []string, blockMsg string) agentcore.StopGuard {
	var baseline int64
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		baseline = cp.Seq
	}
	need := make(map[string]struct{}, len(requiredSteps))
	for _, s := range requiredSteps {
		need[s] = struct{}{}
	}
	var consecutive atomic.Int32
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// Unrecoverable error: escalate immediately to avoid wasting a prompt.
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard detected unrecoverable shutdown, escalating immediately",
				"module", "host.reminder", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// Scan backwards: new checkpoints are at the tail, break when <= baseline is met.
		all := st.Checkpoints.All()
		for i := len(all) - 1; i >= 0; i-- {
			cp := all[i]
			if cp.Seq <= baseline {
				break
			}
			if _, ok := need[cp.Step]; ok {
				consecutive.Store(0)
				return agentcore.StopDecision{Allow: true}
			}
		}
		n := consecutive.Add(1)
		if n > subagentMaxConsecutiveBlocks {
			slog.Error("subagent stop_guard consecutive blocks limit exceeded, escalating to termination",
				"module", "host.reminder", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		slog.Warn("subagent stop_guard intercepted end_turn",
			"module", "host.reminder", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
		return agentcore.StopDecision{Allow: false, InjectMessage: blockMsg}
	}
}

// NewWriterStopGuard requires the writer to produce at least one successful commit_chapter this turn.
func NewWriterStopGuard(st *store.Store) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "writer",
		[]string{"commit"},
		"You must call commit_chapter to commit this chapter before ending. draft_chapter only saves a draft, which is not considered complete.",
	)
}

// NewArchitectStopGuard requires the architect to persist at least one save_foundation output this turn.
func NewArchitectStopGuard(st *store.Store) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"premise", "outline", "layered_outline", "characters", "world_rules",
			"expand_arc", "append_volume", "update_compass", "complete_book",
		},
		"You must call save_foundation to persist the output before ending. Only outputting Markdown/JSON text means it is lost.",
	)
}

// NewEditorStopGuard requires the editor to persist outputs matching the "task" before ending.
//
// Task perception: when dispatched to generate summaries, only calling save_review (reviewing) is not considered complete -- the corresponding summary must be generated.
// Otherwise, an editor "dispatched to generate an arc summary but reviewing first" would satisfy the old loose criteria and end prematurely, and the arc summary would never be persisted
// (combined with dispatcher deduplication misfire, this once caused an infinite loop in the volume skeleton arc, see outline-exhaustion-livelock for details).
// StopAfterTool exit bypasses StopGuard (loop.go), so build.go also removes save_review from hard stops,
// allowing it to proceed to the summary tool after review, and then this guard will perform final validation.
func NewEditorStopGuard(st *store.Store, task string) agentcore.StopGuard {
	switch {
	case strings.Contains(task, "save_volume_summary") || strings.Contains(task, "Volume Summary") || strings.Contains(task, "卷摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"},
			"The current task is to generate the volume summary: you must call save_volume_summary to persist it before ending. Calling save_review is not considered complete.")
	case strings.Contains(task, "save_arc_summary") || strings.Contains(task, "Arc Summary") || strings.Contains(task, "弧摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"},
			"The current task is to generate the arc summary: you must call save_arc_summary to persist it before ending. Calling save_review is not considered complete.")
	default:
		// Review or temporary tasks: any review/summary persisted is fine (maintaining existing loose behavior).
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			"You must call one of save_review / save_arc_summary / save_volume_summary to persist the results before ending.")
	}
}
