package host

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// buildStoryStateSummary compiles a concise summary of the current story state, allowing the stage co-creation assistant to understand "what has already been written".
// Reuses store access points, retrieving only high-level facts needed for planning direction (progress / compass / latest volume / main characters / active foreshadowing);
// It does not pull full text or feed full JSON from novel_context—co-creation is a conversation, requiring a readable overview, not writing context.
// Any missing item is skipped (best-effort), returning an empty string if no progress is available.
func buildStoryStateSummary(s *store.Store) string {
	if s == nil {
		return ""
	}
	var b strings.Builder

	if progress, _ := s.Progress.Load(); progress != nil {
		if name := strings.TrimSpace(progress.NovelName); name != "" {
			fmt.Fprintf(&b, "- Novel Name: %s\n", name)
		}
		fmt.Fprintf(&b, "- Progress: Completed %d chapters", len(progress.CompletedChapters))
		if progress.TotalChapters > 0 {
			fmt.Fprintf(&b, " / Planned %d chapters", progress.TotalChapters)
		}
		fmt.Fprintf(&b, ", approx %d words, next chapter is Chapter %d\n", progress.TotalWordCount, progress.NextChapter())
		if progress.Layered && progress.CurrentVolume > 0 {
			fmt.Fprintf(&b, "- Current Location: Volume %d Arc %d\n", progress.CurrentVolume, progress.CurrentArc)
		}
	}

	if compass, _ := s.Outline.LoadCompass(); compass != nil {
		if dir := strings.TrimSpace(compass.EndingDirection); dir != "" {
			fmt.Fprintf(&b, "- Ending Direction: %s\n", dir)
		}
		if compass.EstimatedScale != "" {
			fmt.Fprintf(&b, "- Estimated Scale: %s\n", compass.EstimatedScale)
		}
		if len(compass.OpenThreads) > 0 {
			fmt.Fprintf(&b, "- Active Long-term Threads: %s\n", strings.Join(compass.OpenThreads, "; "))
		}
	}

	// Summary of the latest volume, showing the assistant where the story is
	if vols, _ := s.Summaries.LoadAllVolumeSummaries(); len(vols) > 0 {
		last := vols[len(vols)-1]
		fmt.Fprintf(&b, "- Latest \"%s\": %s\n", last.Title, truncate(last.Summary, 200))
	}

	// Main characters (core/important), at most 8
	if chars, _ := s.Characters.Load(); len(chars) > 0 {
		var names []string
		for _, c := range chars {
			if c.Tier == "secondary" || c.Tier == "decorative" {
				continue
			}
			line := c.Name
			if role := strings.TrimSpace(c.Role); role != "" {
				line += " (" + role + ")"
			}
			names = append(names, line)
			if len(names) >= 8 {
				break
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "- Main Characters: %s\n", strings.Join(names, ", "))
		}
	}

	// Unresolved foreshadowing, at most 6 items
	if fs, _ := s.World.LoadActiveForeshadow(); len(fs) > 0 {
		var items []string
		for _, f := range fs {
			items = append(items, truncate(f.Description, 40))
			if len(items) >= 6 {
				break
			}
		}
		fmt.Fprintf(&b, "- Unresolved Foreshadowing: %s\n", strings.Join(items, "; "))
	}

	return strings.TrimSpace(b.String())
}

// stageSystemPrompt compiles the complete system prompt for stage co-creation: stage prompt + current story status summary.
// The summary is appended at the end as a data appendix (separated by a horizontal line and format specifications), echoing the "progress is shown below" instruction in the prompt.
func stageSystemPrompt(s *store.Store) string {
	prompt := stageCoCreateSystemPrompt
	if summary := buildStoryStateSummary(s); summary != "" {
		prompt += "\n\n---\n## Current Story Status\n(The following is an objective summary of already written content for your reference when planning, do not copy verbatim in <draft>)\n" + summary
	}
	return prompt
}
