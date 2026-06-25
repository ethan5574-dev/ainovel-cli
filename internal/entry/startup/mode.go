package startup

import "fmt"

// The startup layer handles orchestration before entering the Engine.
// Layering convention:
// 1. entry/tui and entry/headless are host entry points;
// 2. startup owns quick, cocreate, and continue-style startup strategies;
// 3. orchestrator.Engine only executes the formal session and does not prepare modes.

// Mode identifies the startup strategy used before entering the Engine.
type Mode string

const (
	// ModeQuick uses the user input directly as the creative starting point.
	ModeQuick Mode = "quick"
	// ModeCoCreate performs multiple clarification rounds first, then produces a draft to
	// enter the Engine.
	ModeCoCreate Mode = "cocreate"
	// ModeContinueFromNovel assembles context from an existing novel and continues writing.
	ModeContinueFromNovel Mode = "continue_from_novel"
)

// Request describes the raw input submitted from the entry layer to the startup strategy.
// The host entry collects user input first, then startup turns it into an Engine-ready plan.
type Request struct {
	Mode        Mode
	UserPrompt  string
	NovelPath   string
	OutputDir   string
	Interactive bool
}

// Plan describes the result produced by the startup strategy layer.
// The host entry should not assemble the formal startup prompt itself; it should consume
// the Plan and then drive the Engine.
type Plan struct {
	Mode        Mode
	DisplayName string
	StartPrompt string
	ResumeOnly  bool
}

// ErrNotImplemented marks placeholder strategies that have not been implemented yet.
var ErrNotImplemented = fmt.Errorf("startup mode not implemented")

// PrepareContinueFromNovel is the shared placeholder for "continue from an existing novel".
// In the future, both TUI and headless should first normalize input into Request, then
// produce an Engine-ready Plan here.
func PrepareContinueFromNovel(req Request) (Plan, error) {
	return Plan{}, fmt.Errorf("%w: %s", ErrNotImplemented, ModeContinueFromNovel)
}
