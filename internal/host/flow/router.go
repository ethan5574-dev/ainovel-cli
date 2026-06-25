// Package flow implements domain-specific routing: Host uses facts to decide which subagent to dispatch next.
//
// Design principles:
//   - Route is a pure function: input State, output *Instruction. No IO and no Store calls, so it is easy to test.
//   - State is built once from Store by LoadState (which is not pure) and contains all facts needed for routing.
//   - Returning nil is valid: it means "judgment required, let the Coordinator LLM decide."
//
// Router covers table-driven decisions (next step per chapter, arc-end postprocessing, queue-driven actions),
// and does not cover semantic decisions (planner selection, user steer handling, final summaries).
package flow

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// Instruction tells Host which subagent/task the Coordinator should call next.
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // Task description for the subagent
	Reason  string // Reason shown to the Coordinator (optional, useful for debugging and logs)
	Chapter int    // Chapter number for writer tasks (continue/rewrite/polish); 0 means not applicable (editor/architect)
}

// State is Route's input: all facts must be declared here, and Route must not read Store directly.
type State struct {
	Progress *domain.Progress

	// Last completed chapter (tail of Progress.CompletedChapters); 0 means writing has not started yet.
	LastCompleted int

	// Arc boundary info for the previous chapter; other fields are meaningless when IsArcEnd=false.
	// Should be nil when LastCompleted=0 or when not in layered mode.
	ArcBoundary *storepkg.ArcBoundary

	// Three facts for arc-end postprocessing: whether review / arc summary / volume summary have completed.
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// Missing foundation items (a signal to fill gaps during planning).
	FoundationMissing []string
}

// Route returns the next instruction based on facts; nil means let the Coordinator LLM decide.
//
// Decision priority (mutually exclusive, first match wins):
//  1. Phase=Complete        -> nil (LLM outputs the summary)
//  2. Phase!=Writing        -> nil (LLM handles planner selection / filling planning gaps)
//  3. PendingRewrites not empty -> writer rewrites/polishes by queue
//  4. Flow=Reviewing        -> nil (editor just saved review; verdict branching is handled in tools)
//  5. Flow=Steering         -> nil (user intervention in progress)
//  6. Missing arc review     -> editor(arc review)
//  7. Arc review done but arc summary missing -> editor(arc summary)
//  8. Volume summary missing  -> editor(volume summary)
//  9. Next arc is a skeleton  -> architect_long(expand_arc)
//
// 10. Need to decide next volume -> architect_long(append_volume / complete_book)
// 11. Otherwise -> writer(next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. Terminal state: let the LLM output the summary
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. Planning is decided by the Coordinator (architect_long/short selection + fill-in loop)
	if p.Phase != domain.PhaseWriting {
		return nil
	}

	// 3. Rewrite/polish queue has priority (facts are already persisted in tools; Router just dispatches)
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "Rewrite"
		if p.Flow == domain.FlowPolishing {
			verb = "Polish"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s chapter %d", verb, ch),
			Reason:  fmt.Sprintf("%d chapters remain in PendingRewrites", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. Reviewing: save_review just persisted, verdict upgrades/downgrades are handled in tools
	if p.Flow == domain.FlowReviewing {
		return nil
	}

	// 5. User intervention in progress: Coordinator is deciding, Host does not preempt
	if p.Flow == domain.FlowSteering {
		return nil
	}

	// 6-10. Layered-mode arc-end postprocessing
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Run arc review for volume %d, arc %d (scope=arc)", b.Volume, b.Arc),
				Reason: "Arc review is not complete",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Generate arc summary for volume %d, arc %d (save_arc_summary)", b.Volume, b.Arc),
				Reason: "Arc summary is not complete",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Generate volume summary for volume %d (save_volume_summary)", b.Volume),
				Reason: "Volume summary is not complete",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("Expand volume %d, arc %d (save_foundation type=expand_arc)", b.NextVolume, b.NextArc),
				Reason: "The next arc skeleton still needs expansion",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "Decide whether to call save_foundation type=append_volume (continue writing) or type=complete_book (end the book)",
				Reason: "At the volume boundary we need to decide whether to append a new volume or finish the book",
			}
		}
	}

	// 12. Normal continuation
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	return &Instruction{
		Agent:   "writer",
		Task:    fmt.Sprintf("Write chapter %d", next),
		Reason:  "Continue with the next chapter",
		Chapter: next,
	}
}

// FormatMessage formats Instruction into a user message for the Coordinator.
// The format is fixed so the Coordinator prompt can recognize it and respond directly.
func FormatMessage(i *Instruction) string {
	return fmt.Sprintf(
		"[Host directive] Next step: call subagent(%s, %q)\nReason: %s\nThis is an explicit workflow instruction. Execute it immediately; do not call novel_context first and do not output reasoning first.",
		i.Agent, i.Task, i.Reason,
	)
}
