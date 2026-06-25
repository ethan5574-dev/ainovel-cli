// Package imp implements import and reverse-engineering of external novel chapters.
//
// Core idea: use LLM to reverse-engineer the foundation + per-chapter facts, reusing the existing save_foundation /
// commit_chapter tools' atomic persistence. After import, the store state is equivalent to "crashed after writing N chapters",
// and callers can call host.Resume() to resume writing seamlessly.
//
// Bypass Coordinator: importing is deterministic playback, not part of Coordinator's LLM decisions.
// Introducing the Coordinator would only bring uncertainty. This package directly calls the LLM client and tools.
package imp

import "time"

// Chapter represents a single split chapter.
type Chapter struct {
	Title   string
	Content string
}

// Options controls the import behavior.
type Options struct {
	// SourcePath is required. Path to a single txt/md file.
	SourcePath string

	// ResumeFrom is optional. Start importing from chapter N; 0 or 1 means from the beginning.
	// If > 1, the foundation reverse-engineering is skipped (assuming it is already persisted).
	ResumeFrom int
}

// Stage represents the current stage of the import process.
type Stage string

const (
	StageSplitting  Stage = "splitting"
	StageFoundation Stage = "foundation"
	StageChapter    Stage = "chapter"
	StageDone       Stage = "done"
	StageError      Stage = "error"
)

// Event is progress event emitted by the import process.
type Event struct {
	Time    time.Time
	Stage   Stage
	Current int    // current chapter number during the chapter stage; 0 for other stages
	Total   int    // total number of chapters
	Message string // human-readable description
	Err     error  // present on StageError
}
