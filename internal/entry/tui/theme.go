package tui

import "github.com/charmbracelet/lipgloss"

// Theme color palette — warm scroll aesthetic
// AdaptiveColor: Light = light background value, Dark = dark background value
//
// Design principles: Light is left stable (satisfactory results achieved on light backgrounds); Dark is adjusted
// to be ~25% lighter in lightness and slightly higher in saturation than Light, ensuring sufficient contrast on dark backgrounds
// (colorDim was previously #6b6355, which is almost invisible on a #1c1c1c black background, causing dividers/hints to disappear entirely).
//
// colorAccent2 is changed from #7a9e7e to cyan-green #5fb8a3 on dark backgrounds to differentiate from colorSuccess's "healthy green"
// — previously they were identical in color, causing confusion between the architect agent's marker and the "high hit rate" joy.
// bodyTextColor is the foreground strategy for "neutral body text":
//   - Dark terminal -> NoColor, inheriting the terminal's default foreground, avoiding hardcoding #e8e0d0 off-white which might clash
//     with the user's custom warm/cold theme (dark terminal users find default color more readable in practice).
//   - Light terminal -> use Light value of colorText (dark brown #3d3529), retaining the brand's warm tone;
//     default black on light background is too harsh, and the dark brown is visually softer.
//
// Both ends of AdaptiveColor must provide color values; there is no "colorless" option, so we check the background at startup,
// and all subsequent "neutral body text" (overview values, chapter text, command descriptions, etc.) uniformly references bodyTextColor.
var bodyTextColor lipgloss.TerminalColor = func() lipgloss.TerminalColor {
	if lipgloss.HasDarkBackground() {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color("#3d3529")
}()

var (
	colorText    = lipgloss.AdaptiveColor{Light: "#3d3529", Dark: "#e8e0d0"}
	colorDim     = lipgloss.AdaptiveColor{Light: "#8a7e6b", Dark: "#8a8175"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#7a7060", Dark: "#b8b09c"}
	colorAccent  = lipgloss.AdaptiveColor{Light: "#b8860b", Dark: "#e5b449"}
	colorAccent2 = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#5fb8a3"}
	colorRunning = lipgloss.AdaptiveColor{Light: "#6f8641", Dark: "#b5d075"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#7ec488"}
	colorError   = lipgloss.AdaptiveColor{Light: "#b5433a", Dark: "#e07060"}
	colorReview  = lipgloss.AdaptiveColor{Light: "#b07530", Dark: "#e09b5a"}
	colorContext = lipgloss.AdaptiveColor{Light: "#6b5a9e", Dark: "#a890d8"}
	colorTool    = lipgloss.AdaptiveColor{Light: "#3a7a8a", Dark: "#7ec5d8"}
)

// Status color mapping
var statusColors = map[string]lipgloss.AdaptiveColor{
	"READY":    colorDim,
	"PAUSING":  colorAccent,
	"PAUSED":   colorAccent,
	"RUNNING":  colorRunning,
	"REVIEW":   colorReview,
	"REWRITE":  colorReview,
	"COMPLETE": colorSuccess,
	"ERROR":    colorError,
}

// Status display: Icon + English labels. Consistent with the warm tone theme to avoid abrupt solid blocks.
// RUNNING's icon is left empty, dynamically filled by the spinner frame, embedding the sense of motion into the status indicator itself.
var statusDisplay = map[string]struct {
	icon  string
	label string
}{
	"READY":    {"○", "Ready"},
	"RUNNING":  {"", "Running"},
	"REVIEW":   {"◆", "Review"},
	"REWRITE":  {"◆", "Rewrite"},
	"COMPLETE": {"●", "Complete"},
	"PAUSED":   {"⏸", "Paused"},
	"PAUSING":  {"⏸", "Pausing"},
	"ERROR":    {"✕", "Error"},
}

// Event category color mapping
var categoryColors = map[string]lipgloss.AdaptiveColor{
	"DISPATCH": colorAccent,
	"DECISION": colorContext,
	"TOOL":     colorTool,
	"SYSTEM":   colorAccent,
	"USER":     colorAccent2,
	"REVIEW":   colorReview,
	"CHECK":    colorSuccess,
	"ERROR":    colorError,
	"AGENT":    colorMuted,
	"CONTEXT":  colorContext,
	"COMPACT":  colorContext,
}

// Base styles
var (
	baseBorder = lipgloss.RoundedBorder()

	topBarStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Padding(0, 1)

	statusIconStyle = lipgloss.NewStyle().
			Bold(true)

	statusLabelStyle = lipgloss.NewStyle().
				Foreground(colorText)

	panelTitleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	fieldLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(10)

	// fieldValueStyle / cardContentStyle use bodyTextColor — values in the overview area (runtime status,
	// number of completed chapters, word count, etc.), outline items, character list, chapter summary, and other "neutral body text"
	// inherit the terminal's default foreground color on dark backgrounds (to avoid clashing), and use dark brown on light backgrounds.
	// Highly semantic elements (headers, highlighted values, status, errors, hit rate coloring, etc.) still use theme colors like colorAccent / colorError.
	fieldValueStyle = lipgloss.NewStyle().Foreground(bodyTextColor)

	highlightValueStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	contextUsageMetaStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	cardTitleStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	cardContentStyle = lipgloss.NewStyle().Foreground(bodyTextColor)
)
