package host

import (
	"fmt"
	"os"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// buildResumePrompt generates a short prompt and UI label for Resume based on facts.
//
// Refactoring note (2026-04-20): All decisions about "what specifically to do next" have been moved down to the Host Flow Router.
// This function no longer plans actions for the Coordinator; it only does three things:
//  1. Determines if resume is needed (Phase=Complete or no Progress -> returns empty label indicating a new start)
//  2. Generates a label suitable for display in the UI (like "Resume: Arc-end review pending (V2 A3)")
//  3. Explicitly passes the PendingSteer left by the user during downtime to the Coordinator
//
// Returns (prompt, label, error). An empty label indicates no resumable state (should start new).
func buildResumePrompt(store *storepkg.Store) (string, string, error) {
	progress, err := store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	if progress == nil || progress.Phase == domain.PhaseComplete {
		return "", "", nil
	}

	label := describeResume(store, progress)

	var b strings.Builder
	title := progress.NovelName
	if title == "" {
		title = "Current Novel"
	}
	b.WriteString(fmt.Sprintf("[Resume] This book: \"%s\"", title))
	if n := len(progress.CompletedChapters); n > 0 {
		b.WriteString(fmt.Sprintf(" Completed %d chapters", n))
		if progress.TotalChapters > 0 {
			b.WriteString(fmt.Sprintf(" (out of %d)", progress.TotalChapters))
		}
		b.WriteString(fmt.Sprintf(", total %d words", progress.TotalWordCount))
	}
	b.WriteString(".\n")
	b.WriteString("The Host will issue the next command using a `[Host command]` message based on current facts. Execute it immediately upon receipt; do not run novel_context reasoning first.\n")

	if meta, _ := store.RunMeta.Load(); meta != nil && meta.PendingSteer != "" {
		b.WriteString("\nThe user left an intervention message during downtime:\n\"")
		b.WriteString(meta.PendingSteer)
		b.WriteString("\"\nPlease evaluate and handle this first according to the user intervention rules in coordinator.md.")
	}

	return b.String(), label, nil
}

// describeResume generates a human-readable resume label; it does not affect the Coordinator's behavior.
// All execution routes are derived from facts by the Flow Router; this is only for UI display as "Resume: xxx".
func describeResume(store *storepkg.Store, progress *domain.Progress) string {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return fmt.Sprintf("Resume: Planning stage (%s)", progress.Phase)
	case domain.PhaseWriting:
		// Align priority with Router's decision priority to keep the label consistent with the command to be dispatched.
		if pending, _ := store.Signals.LoadPendingCommit(); pending != nil {
			return fmt.Sprintf("Resume: Chapter %d commit interrupted", pending.Chapter)
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "Rewrite"
			if progress.Flow == domain.FlowPolishing {
				verb = "Polish"
			}
			return fmt.Sprintf("%s Resume: %d chapters pending", verb, len(progress.PendingRewrites))
		}
		if progress.Flow == domain.FlowReviewing {
			return "Resume: Review interrupted"
		}
		if progress.InProgressChapter > 0 {
			return fmt.Sprintf("Resume: Chapter %d in progress", progress.InProgressChapter)
		}
		if label := describeArcEndLabel(store, progress); label != "" {
			return label
		}
		return fmt.Sprintf("Resume: Continue from Chapter %d", progress.NextChapter())
	}
	return "Resume"
}

// describeArcEndLabel generates a UI-friendly label for various intermediate states at arc-end or volume-end.
// Keeps the same order as the arc-end branch in flow.Route to ensure the label aligns with the Router's first command.
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) string {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return ""
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil || boundary == nil || !boundary.IsArcEnd {
		return ""
	}
	vol, arc := boundary.Volume, boundary.Arc
	switch {
	case !store.World.HasArcReview(lastCh):
		return fmt.Sprintf("Resume: Arc-end review pending (V%d A%d)", vol, arc)
	case !store.Summaries.HasArcSummary(vol, arc):
		return fmt.Sprintf("Resume: Arc summary pending (V%d A%d)", vol, arc)
	case boundary.IsVolumeEnd && !store.Summaries.HasVolumeSummary(vol):
		return fmt.Sprintf("Resume: Volume summary pending (V%d)", vol)
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("Resume: Expand next arc pending (V%d A%d)", boundary.NextVolume, boundary.NextArc)
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("Resume: Decide next volume pending (End of V%d)", vol)
	}
	return ""
}
