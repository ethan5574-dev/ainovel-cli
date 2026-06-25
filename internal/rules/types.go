// Package rules implements the persistent input layer for user preferences (Policy).
//
// Rule is the fourth category of facts, alongside Progress / Checkpoint / Artifact, but with opposite characteristics:
// the first three are system outputs, whereas Rule is the persistent input of user intent.
//
// Design Constraints (non-negotiable):
//   - Tools only return facts, not commands (Violation is a fact, editor determines whether to trigger rewrite)
//   - Do not introduce new verdict paths (reuse PendingRewrites)
//   - Do not introduce strictness fields (severity is fixedly mapped by rule type, editor decides semantically)
//   - Do not silently swallow conflicts (all exceptions go to Bundle.Conflicts, visible to LLM and /diag)
//   - Do not touch Flow Router (rules do not participate in routing)
package rules

// SourceKind marks the rule source, used for sorting by nearest priority during merging.
// Sourced in proximity order (larger is closer): Project > Global > Default.
//
// Only three layers are supported starting from Phase 1.1. Genre / Learned layers are not opened before actual genre libraries / save_rule are implemented—
// simply add constants and implement in loader when needed, avoiding empty placeholders.
type SourceKind int

const (
	// SourceDefault — Project built-in default rules (assets/rules/default.md), lowest priority.
	SourceDefault SourceKind = iota
	// SourceGlobal — User global preferences (all .md under ~/.ainovel/rules/, merged in lexicographical order), reused across books.
	SourceGlobal
	// SourceProject — This book's rules (all .md under ./.ainovel/rules/, merged in lexicographical order), highest priority.
	SourceProject
)

// String returns the human-readable name of the source, used for headers in markdown concatenation and conflicts.detail.
func (k SourceKind) String() string {
	switch k {
	case SourceDefault:
		return "default"
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// WordRange indicates the allowed range for chapter word counts; nil means undeclared.
type WordRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Structured holds the structured fields of front matter.
//
// In single file parsing, Parsed.Structured only fills fields declared in that file, keeping others as zero value.
// After merging, Bundle.Structured is the aggregated result after nearest priority sorting.
type Structured struct {
	Genre            string         `json:"genre,omitempty"`
	ChapterWords     *WordRange     `json:"chapter_words,omitempty"`
	ForbiddenChars   []string       `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string       `json:"forbidden_phrases,omitempty"`
	FatigueWords     map[string]int `json:"fatigue_words,omitempty"`
}

// IsEmpty is used to determine if there are no structured rules at all; checker can skip based on this.
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		s.ChapterWords == nil &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.FatigueWords) == 0
}

// ConflictKind marks the type of conflict or exception, facilitating classification by LLM and diagnostics panel.
type ConflictKind string

const (
	// ConflictParseError — front matter parsing failed as a whole; body is still injected as preferences.
	ConflictParseError ConflictKind = "parse_error"
	// ConflictUnknownField — User wrote fields not supported in Phase 1 (forward-compatible).
	ConflictUnknownField ConflictKind = "unknown_field"
	// ConflictTypeError — Field type error (e.g. forbidden_chars written as string); the field is discarded.
	ConflictTypeError ConflictKind = "type_error"
	// ConflictFieldConflict — Values of the same structured field mismatch across multiple sources; nearest priority applies.
	ConflictFieldConflict ConflictKind = "field_conflict"
	// ConflictInvalidValue — Field value format is invalid (e.g. chapter_words: "abc"); the field is discarded.
	ConflictInvalidValue ConflictKind = "invalid_value"
)

// Conflict represents a conflict or exception record.
//
// Never blocks loading — all exceptions are exposed to LLM and /diag here, no silent handling.
type Conflict struct {
	Source string       `json:"source"`          // File path (absolute or relative, recorded by source)
	Kind   ConflictKind `json:"kind"`            // Conflict type
	Field  string       `json:"field,omitempty"` // Affected field name (e.g. forbidden_chars); empty for parse_error
	Detail string       `json:"detail"`          // Human-readable details (including source list / error message)
}

// Parsed is the parsed result of a single rules.md file.
type Parsed struct {
	Source     string     // File path
	Kind       SourceKind // Source type, used for merge priority
	Structured Structured // Front matter fields declared in this file
	Preference string     // Markdown body of this file (excluding front matter)
	Conflicts  []Conflict // Conflicts generated during parsing of this file (unknown fields / type errors)
}

// Bundle is the final format injected into working_memory.user_rules after merging.
//
// Fields mapped to JSON output:
//
//	{
//	  "structured": {...},
//	  "preferences": "...merged markdown...",
//	  "sources": ["..."],
//	  "conflicts": [...]
//	}
type Bundle struct {
	Structured  Structured `json:"structured"`
	Preferences string     `json:"preferences"`
	Sources     []string   `json:"sources"`
	Conflicts   []Conflict `json:"conflicts"`
}

// IsEmpty indicates that the Bundle is completely empty (both structured fields and preference body are empty).
// An empty Bundle should still be kept when injecting into user_rules to prevent the LLM from handling nil.
func (b Bundle) IsEmpty() bool {
	return b.Structured.IsEmpty() && b.Preferences == ""
}

// Severity marks the severity level of a Violation.
// Fixed mapping (not user-configurable):
//
//	forbidden_chars appears            -> Error
//	forbidden_phrases appears          -> Error
//	fatigue_words exceeds threshold    -> Warning
//	chapter_words deviation < 20%      -> Warning
//	chapter_words deviation >= 20%     -> Error
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// ChapterWordsDeviationThreshold defines the threshold for chapter_words deviation to be upgraded to an error (20%).
const ChapterWordsDeviationThreshold = 0.20

// Violation is the output of the checker: a factual statement that this chapter violated a mechanical rule.
//
// Note: commit_chapter passes violations through to the returned JSON, without blocking commit;
// during review, the editor maps these facts to the existing seven dimensions (aesthetic/pacing/character/consistency),
// allowing the LLM to decide whether to upgrade the verdict to trigger polish/rewrite.
type Violation struct {
	Rule      string   `json:"rule"`                // forbidden_chars / forbidden_phrases / fatigue_words / chapter_words
	Target    string   `json:"target,omitempty"`    // Specific violation target (which word/character); empty for chapter_words
	Limit     any      `json:"limit,omitempty"`     // Threshold; fatigue_words=int / chapter_words="3000-6000" / forbidden_*=empty
	Actual    any      `json:"actual"`              // Actual value; occurrence count for fatigue_words/forbidden_*, word count of this chapter for chapter_words
	Deviation float64  `json:"deviation,omitempty"` // chapter_words deviation rate (0~1), empty for other rules
	Severity  Severity `json:"severity"`            // error / warning
}
