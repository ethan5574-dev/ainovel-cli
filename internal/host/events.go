package host

import (
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// Event is a structured event consumed by the TUI.
//
// For TOOL and DISPATCH invocation events, the start and end of the same call share an ID:
// When starting, an event with a zero FinishedAt value is emitted (TUI renders it in an "in-progress" style);
// When ending, another event with the same ID is emitted, populating FinishedAt + Duration (+ Failed).
// The TUI locates the original line by ID and updates it in place to avoid "start a line, complete another line" redundancy.
//
// Non-invocation events like SYSTEM / ERROR / CONTEXT have empty IDs, and each is appended independently.
type Event struct {
	ID         string    // Shared by the start/end of the same call; empty for non-call events
	Time       time.Time // First emitted time (start time)
	FinishedAt time.Time // Zero value = in progress; non-zero = completed
	Failed     bool      // Completed but failed (only meaningful in completion state)
	Category   string    // DISPATCH / TOOL / SYSTEM / REVIEW / CHECK / ERROR / CONTEXT
	Agent      string    // The agent that generated the event
	Summary    string
	Detail     string        // Full text, logged without truncation for troubleshooting; falls back to Summary if empty. UI reads only Summary
	Kind       string        // Error category (e.g. stream_idle), output with logs for filtering/alarms; omitted if empty
	Level      string        // info / warn / error / success
	Depth      int           // 0 = coordinator layer, 1 = sub-agent layer
	Duration   time.Duration // Execution duration when completed
}

// Running returns whether the event is in progress.
// Only invocation events (TOOL / DISPATCH with an ID) can be in progress; other types always return false.
func (e Event) Running() bool {
	return e.ID != "" && e.FinishedAt.IsZero()
}

// UISnapshot is a snapshot of aggregated state required for TUI rendering.
type UISnapshot struct {
	Provider           string
	NovelName          string
	ModelName          string
	ModelContextWindow int // Context window of the current default model (dynamically resolved as /model switches)
	Style              string
	RuntimeState       string // idle / running / pausing / paused / completed
	StatusLabel        string
	Phase              string
	Flow               string
	CurrentChapter     int
	TotalChapters      int
	CompletedCount     int
	TotalWordCount     int
	InProgressChapter  int
	PendingRewrites    []int
	RewriteReason      string
	PendingSteer       string
	RecoveryLabel      string
	IsRunning          bool
	Agents             []AgentSnapshot

	// Context
	ContextTokens         int
	ContextWindow         int
	ContextPercent        float64
	ContextScope          string
	ContextStrategy       string
	ContextActiveMessages int
	ContextSummaryCount   int
	ContextCompactedCount int
	ContextKeptCount      int

	// Cumulative usage (entire session, across all agents and model switches)
	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheReadTokens  int
	TotalCacheWriteTokens int
	TotalCostUSD          float64
	TotalSavedUSD         float64 // USD saved due to CacheRead hits (relative to pricing all input as non-cached)
	BudgetLimitUSD        float64 // Budget limit (config budget.book_usd); 0 = disabled

	// Cache Diagnostics
	OverallCacheCapable    bool // At least one role has run a model supporting prompt cache (distinguishing "disabled" from "0% hit")
	OverallRecentCacheRead int  // Sum of cacheRead in the sliding window's last N calls
	OverallRecentInput     int  // Sum of input in the sliding window's last N calls
	OverallRecentSamples   int  // Number of samples in the sliding window (<= recentSampleCap)

	// MissingAssistantUsage > 0 usually means the upstream streaming failed to send the final usage chunk according to the OpenAI
	// stream_options.include_usage protocol (common with custom proxies), causing UsageTracker to receive no cumulative data.
	// The UI uses this to prompt the user to check the backend, preventing them from assuming the cache module itself is broken.
	MissingAssistantUsage int

	// Cache per-role dimension, sorted by CacheRead descending, filtering roles that have not consumed tokens
	CachePerAgent []AgentCacheStat
	CachePerModel []AgentCacheStat

	// Basic settings
	Premise          string
	Outline          []OutlineSnapshot
	Characters       []string
	SupportingCount  int      // Total number of supporting characters in the cast ledger
	RecentSupporting []string // Recently active supporting characters (up to 5, descending by LastSeenChapter)
	Layered          bool
	CurrentVolumeArc string
	NextVolumeTitle  string
	CompassDirection string
	CompassScale     string

	// Details
	LastCommitSummary  string
	LastReviewSummary  string
	LastCheckpointName string
	RecentSummaries    []string
}

// OutlineSnapshot is a display summary of an outline entry.
type OutlineSnapshot struct {
	Chapter   int
	Title     string
	CoreEvent string
}

// AgentSnapshot is a display projection of an Agent's state.
type AgentSnapshot struct {
	Name      string
	State     string
	TaskID    string
	TaskKind  string
	Summary   string
	Tool      string
	Turn      int
	Context   AgentContextSnapshot
	UpdatedAt time.Time
}

// AgentCacheStat is the accumulated cache hit statistics for a single agent (projected to the left bar).
// HitRate = CacheRead / Input; Input is unified to the "includes CacheRead" semantics in the litellm layer.
//
// CacheCapable distinguishes between two types of 0% hits:
//   - true  -> Model supports prompt cache; 0% implies poor prompt design or unstable prefix, requiring optimization.
//   - false -> Model/provider does not support prompt cache; 0% is expected, no need to troubleshoot.
//
// Recent* represents the hit data in the sliding window (the last N calls). Comparing to cumulative data helps identify "initial drag" vs "steady-state low hit rate".
type AgentCacheStat struct {
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

// AgentContextSnapshot is the agent context usage status.
type AgentContextSnapshot struct {
	Tokens          int
	ContextWindow   int
	Percent         float64
	Scope           string
	Strategy        string
	ActiveMessages  int
	SummaryMessages int
	CompactedCount  int
	KeptCount       int
}

// CoCreateMessage represents a co-creation conversation message.
type CoCreateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CoCreateReply is the LLM reply in the co-creation conversation. Raw retains the complete four sections of the model's raw text,
// used to write back to history so the model in the next round sees its own [DRAFT] from the previous round, thereby truly achieving
// cumulative updates on the existing draft (only Message without [DRAFT] causes the model to re-summarize based on conversation in each round).
// Suggestions are AI-generated suggestions of "what you might want to say next", which the user can populate with a single key when stuck.
type CoCreateReply struct {
	Message     string
	Prompt      string
	Ready       bool
	Suggestions []string
	Raw         string
}

// ReplayDeltaText extracts replayable streaming text from a runtime queue item.
func ReplayDeltaText(item domain.RuntimeQueueItem) string {
	if payload, ok := item.Payload.(map[string]any); ok {
		if text, ok := payload["delta"].(string); ok {
			return text
		}
	}
	return ""
}

// BuildStartPrompt wraps user requirements as the Coordinator's start prompt.
func BuildStartPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	return "Please start writing a novel based on the following requirements. Once in planning, the first line of Premise must output '# Novel Title'. The number of chapters is up to you based on story needs; if the genre and conflict naturally suit a long series, please prioritize planning a layered long structure instead of compressing into a short story outline.\n\n[Writing Requirements]\n" +
		prompt +
		"\n\nIf certain details are not specified, please complete them yourself without violating the user's direction."
}
