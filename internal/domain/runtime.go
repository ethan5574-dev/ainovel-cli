package domain

import "strings"

// Phase represents the novel creation phase.
type Phase string

const (
	PhaseInit     Phase = "init"
	PhasePremise  Phase = "premise"
	PhaseOutline  Phase = "outline"
	PhaseWriting  Phase = "writing"
	PhaseComplete Phase = "complete"
)

// FlowState represents the currently active flow type, used for checkpoint recovery.
type FlowState string

const (
	FlowWriting   FlowState = "writing"
	FlowReviewing FlowState = "reviewing"
	FlowRewriting FlowState = "rewriting"
	FlowPolishing FlowState = "polishing"
	FlowSteering  FlowState = "steering"
)

// PlanningTier represents the planned length category of the novel.
type PlanningTier string

const (
	PlanningTierShort PlanningTier = "short"
	PlanningTierMid   PlanningTier = "mid"
	PlanningTierLong  PlanningTier = "long"
)

// Progress represents progress tracking, persisted in meta/progress.json.
type Progress struct {
	NovelName         string      `json:"novel_name"`
	Phase             Phase       `json:"phase"`
	CurrentChapter    int         `json:"current_chapter"`
	TotalChapters     int         `json:"total_chapters"`
	CompletedChapters []int       `json:"completed_chapters"`
	TotalWordCount    int         `json:"total_word_count"`
	ChapterWordCounts map[int]int `json:"chapter_word_counts,omitempty"` // Word count per chapter, used to correct total word count during rewrite
	InProgressChapter int         `json:"in_progress_chapter,omitempty"` // The chapter currently being written (scene-level recovery)
	CompletedScenes   []int       `json:"completed_scenes,omitempty"`    // Completed scene indices of the current chapter
	Flow              FlowState   `json:"flow,omitempty"`                // Current active flow
	PendingRewrites   []int       `json:"pending_rewrites,omitempty"`    // Queue of chapters pending rewrite
	RewriteReason     string      `json:"rewrite_reason,omitempty"`      // Reason for rewrite
	StrandHistory     []string    `json:"strand_history,omitempty"`      // dominant_strand recorded in chapter order
	HookHistory       []string    `json:"hook_history,omitempty"`        // hook_type recorded in chapter order
	// Long-form layered tracking (used in long-form mode only, zero-valued for short/medium)
	CurrentVolume int  `json:"current_volume,omitempty"`
	CurrentArc    int  `json:"current_arc,omitempty"`
	Layered       bool `json:"layered,omitempty"`
	// ReopenedFromComplete marks that this book was reopened from complete status for rework.
	// Rework only modifies existing chapters and does not add/remove structure, so once the rewrite queue
	// is empty, it should be marked as complete again (avoiding getting stuck in a writing -> out-of-bounds rewrite loop
	// due to final volume foreshadowing updates). Standard writing doesn't set this flag, keeping completion conservative.
	ReopenedFromComplete bool `json:"reopened_from_complete,omitempty"`
}

// IsResumable checks if execution can be resumed from checkpoint.
func (p *Progress) IsResumable() bool {
	return p.Phase == PhaseWriting && p.CurrentChapter > 0
}

// NextChapter returns the next chapter number to write.
func (p *Progress) NextChapter() int {
	return p.LatestCompleted() + 1
}

// LatestCompleted returns the maximum completed chapter number, or 0 if no chapters are completed.
func (p *Progress) LatestCompleted() int {
	max := 0
	for _, ch := range p.CompletedChapters {
		if ch > max {
			max = ch
		}
	}
	return max
}

// ExtractNovelNameFromPremise extracts the novel title from the first `# Title` line of the premise (may be wrapped in Chinese book quotes 《》).
// The model occasionally copies the prompt placeholder literally instead of generating a real name;
// in such cases, it returns an empty string, allowing the host to show a placeholder like "Untitled Book"
// to avoid showing placeholder text literal strings in the TUI.
func ExtractNovelNameFromPremise(premise string) string {
	for raw := range strings.SplitSeq(strings.ReplaceAll(premise, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "# ") {
			return ""
		}
		name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "# ")), "《》\"")
		switch name {
		case "书名", "实际书名", "示例书名", "Book Title", "Actual Title", "Example Title":
			return "" // Prompt placeholder, not a real book title
		}
		return name
	}
	return ""
}

// ContextProfile represents context loading strategy, adaptive based on total chapters.
type ContextProfile struct {
	SummaryWindow  int  // Load recent N chapters' summaries
	TimelineWindow int  // Load recent N chapters' timeline
	Layered        bool // true = enable layered summary loading (Volume + Arc + Chapter summaries)
}

// MemoryPolicy represents memory sharing strategy at runtime.
// Used for context output and host handoff/reminder decisions.
type MemoryPolicy struct {
	Mode                string `json:"mode,omitempty"`
	SummaryWindow       int    `json:"summary_window,omitempty"`
	TimelineWindow      int    `json:"timeline_window,omitempty"`
	LayeredSummaries    bool   `json:"layered_summaries,omitempty"`
	SummaryStrategy     string `json:"summary_strategy,omitempty"`
	WorkingRefresh      string `json:"working_refresh,omitempty"`
	EpisodicRefresh     string `json:"episodic_refresh,omitempty"`
	PlanningRefresh     string `json:"planning_refresh,omitempty"`
	FoundationRefresh   string `json:"foundation_refresh,omitempty"`
	PlanningFocus       string `json:"planning_focus,omitempty"`
	FoundationFocus     string `json:"foundation_focus,omitempty"`
	PreviousTailChars   int    `json:"previous_tail_chars,omitempty"`
	ChapterPlanEnabled  bool   `json:"chapter_plan_enabled,omitempty"`
	RelatedLookup       bool   `json:"related_chapter_lookup,omitempty"`
	CurrentOutlineBound bool   `json:"current_outline_bound,omitempty"`
	TotalChapters       int    `json:"total_chapters,omitempty"`
	HandoffPreferred    bool   `json:"handoff_preferred,omitempty"`
	ReadOnlyThreshold   int    `json:"read_only_threshold,omitempty"`
}

// NewContextProfile calculates context strategy based on total chapters.
func NewContextProfile(totalChapters int) ContextProfile {
	switch {
	case totalChapters <= 15:
		return ContextProfile{SummaryWindow: 10, TimelineWindow: 10}
	case totalChapters <= 50:
		return ContextProfile{SummaryWindow: 5, TimelineWindow: 8}
	default:
		return ContextProfile{SummaryWindow: 3, TimelineWindow: 5, Layered: true}
	}
}

// NewChapterMemoryPolicy generates runtime memory policy for chapters based on progress and context profile.
func NewChapterMemoryPolicy(progress *Progress, profile ContextProfile, currentOutlineBound bool) MemoryPolicy {
	policy := MemoryPolicy{
		Mode:                "chapter",
		SummaryWindow:       profile.SummaryWindow,
		TimelineWindow:      profile.TimelineWindow,
		LayeredSummaries:    profile.Layered,
		WorkingRefresh:      "Refresh on each chapter load",
		EpisodicRefresh:     "Refresh on chapter commit, review, or long-form state changes",
		PreviousTailChars:   800,
		ChapterPlanEnabled:  true,
		CurrentOutlineBound: currentOutlineBound,
		ReadOnlyThreshold:   5,
	}
	if profile.Layered {
		policy.SummaryStrategy = "Volume summary + Arc summary + Recent chapter summaries"
	} else {
		policy.SummaryStrategy = "Recent chapter summaries"
	}
	if progress != nil {
		policy.TotalChapters = progress.TotalChapters
		if progress.TotalChapters > 30 {
			policy.RelatedLookup = true
		}
		if progress.Flow == FlowReviewing || progress.Flow == FlowRewriting || progress.Flow == FlowPolishing {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.HandoffPreferred = true
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.ReadOnlyThreshold = 4
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.ReadOnlyThreshold = 4
		}
	}
	return policy
}

// NewArchitectMemoryPolicy returns the memory policy used in the planning phase.
func NewArchitectMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{
		Mode:               "architect",
		PlanningRefresh:    "Refresh when volume/arc structure, compass or summary changes",
		FoundationRefresh:  "Refresh when characters, foreshadows, or world settings change",
		PlanningFocus:      "Layered outline, compass, volume summary",
		FoundationFocus:    "Character profile, character snapshots, foreshadow ledger",
		HandoffPreferred:   true,
		ChapterPlanEnabled: false,
		ReadOnlyThreshold:  4,
	}
}

// RunMeta represents run metadata, persisted in meta/run.json.
type RunMeta struct {
	StartedAt    string       `json:"started_at"`
	Provider     string       `json:"provider,omitempty"`
	Style        string       `json:"style"`
	Model        string       `json:"model"`
	PlanningTier PlanningTier `json:"planning_tier,omitempty"`
	SteerHistory []SteerEntry `json:"steer_history,omitempty"`
	PendingSteer string       `json:"pending_steer,omitempty"` // Pending steer instruction, re-injected on resume
}

// SteerEntry represents a user steer record.
type SteerEntry struct {
	Input     string `json:"input"`
	Timestamp string `json:"timestamp"`
}

// UserDirective represents user persistent requirements across chapters.
// Persisted in meta/user_directives.json, injected by novel_context
// working_memory.user_directives for all subagents to follow.
//
// Chapter/TotalChapters is the progress snapshot when it was requested: allowing directives
// to have an explicit start point (not applied retroactively), and letting relative directives
// (e.g. "add 10 chapters") be checked as completed by the consumer, instead of executing on every load.
type UserDirective struct {
	Text          string `json:"text"`
	Chapter       int    `json:"chapter"`        // Progress when issued
	TotalChapters int    `json:"total_chapters"` // Planned total chapters when issued
	CreatedAt     string `json:"created_at"`     // RFC3339
}
