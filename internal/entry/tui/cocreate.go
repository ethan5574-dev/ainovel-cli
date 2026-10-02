package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
)

type startupMode int

const (
	startupModeQuick startupMode = iota
	startupModeCoCreate
)

func (m startupMode) label() string {
	switch m {
	case startupModeCoCreate:
		return "Co-Creation Plan"
	default:
		return "Quick Start"
	}
}

func (m startupMode) subtitle() string {
	switch m {
	case startupModeCoCreate:
		return "Clarify requirements with AI first, then start writing"
	default:
		return "Start writing directly from a brief prompt"
	}
}

func placeholderForNewMode(mode startupMode) string {
	switch mode {
	case startupModeCoCreate:
		return "Input your core idea, Enter to start co-creation with AI"
	default:
		return "Input your novel requirements, Enter to start writing directly"
	}
}

func placeholderForCoCreate(state *cocreateState) string {
	if state == nil {
		return placeholderForNewMode(startupModeCoCreate)
	}
	switch {
	case state.awaiting:
		return "AI is organizing your requirements..."
	case state.canStart():
		if state.stage {
			return "Provide details, or press Ctrl+S to apply direction and continue writing"
		}
		return "Provide details, or press Ctrl+S to start writing"
	default:
		return "Provide more details, Enter to send to AI"
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

type cocreateState struct {
	session             *startup.CoCreateSession
	stage               bool // true: stage co-creation plans subsequent events; false: pre-start co-creation clarifies requirements.
	awaiting            bool
	reqID               int
	cancel              context.CancelFunc // Cancel the current LLM request.
	deltaCh             chan cocreateStreamItem
	doneCh              chan cocreateDoneMsg
	convVP              viewport.Model
	promptVP            viewport.Model
	convFollow          bool // Follow streamed content until the user scrolls upward.
	selectedSuggestions []string
	// focusPrompt selects the pane for arrow/Page/Home/End scrolling: false = conversation (default),
	// true = creative instructions. Mouse reporting is disabled on the welcome screen for native copying; Tab selects the right pane for keyboard scrolling.
	focusPrompt bool
}

func newCoCreateState(initial string) *cocreateState {
	makeVP := func() viewport.Model {
		vp := viewport.New(0, 0)
		vp.MouseWheelEnabled = true
		vp.MouseWheelDelta = 3
		return vp
	}
	return &cocreateState{
		session:    startup.NewCoCreateSession(strings.TrimSpace(initial)),
		awaiting:   true,
		convVP:     makeVP(),
		promptVP:   makeVP(),
		convFollow: true,
	}
}

// stageCoCreateOpener is the synthesized opening user message for mid-stage co-creation, sent to the LLM as the kickoff user turn,
// letting the assistant actively open based on the "current story state" instead of waiting silently for the user to speak first.
const stageCoCreateOpener = "Let me pause for a moment, I want to plan the next steps with you."

// stageCoCreateSystemLine is the neutral UI presentation of the opening: the opening is essentially synthesized by the system,
// not typed by the user, so we do not disguise it as a "You" message, and present it as a system line (it is still sent to LLM
// as stageCoCreateOpener, see i==0 special casing in renderCoCreateConversationPanel).
const stageCoCreateSystemLine = "Writing paused. Entering mid-stage co-creation — AI will work with you to plan the next steps based on the current story progress."

// newStageCoCreateState creates the mid-stage co-creation state: seeding the opening and marking stage, making runCoCreate run
// StageCoCreateStream, and Ctrl+S run ResumeFromCoCreate.
func newStageCoCreateState() *cocreateState {
	s := newCoCreateState(stageCoCreateOpener)
	s.stage = true
	return s
}

func (s *cocreateState) appendUser(text string) {
	s.resetSuggestionInput()
	s.session.AppendUser(text)
}

func (s *cocreateState) apply(reply host.CoCreateReply) {
	s.awaiting = false
	s.resetSuggestionInput()
	s.session.ApplyReply(reply)
}

func (s *cocreateState) applyDelta(kind, text string) {
	s.session.ApplyDelta(kind, text)
}

func (s *cocreateState) canStart() bool {
	return s.session.CanStart()
}

func (s *cocreateState) initialInput() string {
	return s.session.InitialInput()
}

func (s *cocreateState) streamReply() string {
	return s.session.StreamReply()
}

func (s *cocreateState) draftPrompt() string {
	return s.session.DraftPrompt()
}

func (s *cocreateState) ready() bool {
	return s.session.Ready()
}

func (s *cocreateState) suggestions() []string {
	return s.session.Suggestions()
}

// appendSuggestion appends a numbered suggestion to shortcut-generated input.
// Manual edits break the shortcut composition match, restoring ordinary numeric input.
func (s *cocreateState) appendSuggestion(index int, current string) (string, bool) {
	suggestions := s.suggestions()
	if index < 0 || index >= len(suggestions) {
		return "", false
	}
	if len(s.selectedSuggestions) == 0 {
		if strings.TrimSpace(current) != "" {
			return "", false
		}
	} else if current != strings.Join(s.selectedSuggestions, "；") {
		s.resetSuggestionInput()
		return "", false
	}

	suggestion := strings.TrimSpace(suggestions[index])
	for _, selected := range s.selectedSuggestions {
		if selected == suggestion {
			return current, true
		}
	}

	s.selectedSuggestions = append(s.selectedSuggestions, suggestion)
	return strings.Join(s.selectedSuggestions, "；"), true
}

func (s *cocreateState) resetSuggestionInput() {
	s.selectedSuggestions = nil
}

func (s *cocreateState) buildPrompt() (string, error) {
	return s.session.BuildPrompt()
}

func renderStartupModeBar(width int, mode startupMode) string {
	quick := renderStartupModePill(mode == startupModeQuick, "Quick Start")
	cocreate := renderStartupModePill(mode == startupModeCoCreate, "Co-Creation Plan")
	title := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("Startup Mode")
	divider := lipgloss.NewStyle().
		Foreground(colorDim).
		Render("·")
	line := title + " " + divider + " " + quick + "  " + cocreate
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Render(line)
}

func renderStartupModePill(active bool, label string) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if active {
		style = style.Foreground(lipgloss.Color("#1c1a14")).Background(colorAccent).Bold(true)
	} else {
		style = style.Foreground(colorMuted)
	}
	return style.Render(label)
}

// coCreateColumns splits the modal content area into left and right pane widths.
// The left pane holds the conversation and input box (stacked vertically), the right pane holds the writing instruction draft; they sum to the modal width.
func coCreateColumns(bodyW int) (leftW, rightW int) {
	leftW = bodyW * 58 / 100
	if leftW < 42 {
		leftW = bodyW / 2
	}
	rightW = bodyW - leftW
	if rightW < 28 {
		rightW = 28
		leftW = bodyW - rightW
	}
	return leftW, rightW
}

func renderCoCreateBody(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int) string {
	if state == nil {
		return ""
	}
	leftW, rightW := coCreateColumns(width)

	// The right border is drawn by the outer leftCol container, running from top to bottom; conversation / suggestions /
	// input do not draw their own right borders. input is still a full rounded box, with 1 margin column on the left and right aligning with
	// conversation's padding, looking symmetrical to the side borders.
	// In co-creation mode, textarea is fixed to 1 line (see model.refitTextareaHeight branch),
	// input height = 1 (textarea) + 2 (top/bottom border) = 3 lines, never shifts.
	innerW := leftW - 1 // Leave 1 column for the outer right vertical line

	inputBox := lipgloss.NewStyle().
		Width(innerW-6). // -2 margin -2 padding -2 border
		Border(baseBorder).
		BorderForeground(colorDim).
		Padding(0, 1).
		Margin(0, 1).
		Render(inputView)

	suggestionsBox := renderCoCreateSuggestions(innerW, state)
	suggestionsH := 0
	if suggestionsBox != "" {
		suggestionsH = lipgloss.Height(suggestionsBox)
	}

	convH := height - lipgloss.Height(inputBox) - suggestionsH
	if convH < 4 {
		convH = 4
	}

	convPanel := renderCoCreateConversationPanel(innerW, convH, state, errMsg, spinnerFrame)

	var stack string
	if suggestionsBox == "" {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, inputBox)
	} else {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, suggestionsBox, inputBox)
	}

	leftCol := lipgloss.NewStyle().
		Border(baseBorder, false, true, false, false).
		BorderForeground(colorDim).
		Render(stack)

	rightPanel := renderCoCreatePromptPanel(rightW, height, state)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightPanel)
}

// extractReplyForDisplay extracts the <reply>...</reply> block from the assistant's historical content.
// Other tags (<draft>/<ready>/<suggestions>) are protocol fields for the next round's model and should not be exposed to the user.
// When the model partially complies (omits opening <reply> tag), the text from start to </reply> or the next opening tag counts as reply.
// If it contains no tags at all (fallback path), it is returned as-is.
func extractReplyForDisplay(content string) string {
	rest := content
	if rIdx := strings.Index(content, "<reply>"); rIdx >= 0 {
		rest = content[rIdx+len("<reply>"):]
	}
	if cIdx := strings.Index(rest, "</reply>"); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	cut := len(rest)
	for _, mark := range []string{"<draft>", "<ready>", "<suggestions>"} {
		if idx := strings.Index(rest, mark); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	if cut == len(rest) && !strings.Contains(content, "<") {
		return content
	}
	return strings.TrimSpace(rest[:cut])
}

// renderCoCreateSuggestions renders AI suggestion lines above the input. When awaiting or when there are no suggestions,
// it returns an empty string, allowing the layout to collapse naturally. Up to 3 suggestions are shown, selected via keys 1/2/3.
func renderCoCreateSuggestions(width int, state *cocreateState) string {
	if state == nil || state.awaiting {
		return ""
	}
	sugs := state.suggestions()
	if len(sugs) == 0 {
		return ""
	}
	if len(sugs) > 3 {
		sugs = sugs[:3]
	}

	digits := []string{"❶", "❷", "❸"}
	digitStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(colorMuted)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	lines := []string{hintStyle.Render("AI suggestions (combine with 1/2/3, edit before sending):")}
	for i, s := range sugs {
		lines = append(lines, digitStyle.Render(digits[i]+" ")+bodyStyle.Render(strings.TrimSpace(s)))
	}

	// Align with inputBox left/right margin/padding: 2 columns on the left (margin1+padding1), same for the right.
	return lipgloss.NewStyle().
		Width(width-2).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
}

func coCreateModalSize(width, height int) (boxW, boxH int) {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 24
	}
	boxW = min(max(width*76/100, 88), width-4)
	boxH = min(max(height*72/100, 22), height-4)
	if boxW < 64 {
		boxW = max(width-2, 42)
	}
	if boxH < 14 {
		boxH = max(height-2, 12)
	}
	return boxW, boxH
}

// coCreateInputWidth calculates the actual text input width of the textarea.
// Left pane decorations: outer right vertical line 1 + input left/right margins 2 + border 2 + padding 2 = 7 columns;
// textarea's own prompt+cursor takes 2 columns; so textareaW = leftW - 9.
func coCreateInputWidth(width, height int) int {
	boxW, _ := coCreateModalSize(width, height)
	bodyW := boxW - 4
	leftW, _ := coCreateColumns(bodyW)
	inputW := leftW - 9
	if inputW < 20 {
		inputW = 20
	}
	return inputW
}

func renderCoCreateModal(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int, quitPending bool) string {
	if state == nil {
		return ""
	}

	boxW, boxH := coCreateModalSize(width, height)

	// title / subtitle / hint are placed outside the modal (centered above and below), allowing the modal inner
	// body to take all space — the left pane's right vertical line and the right pane run from top to bottom.
	// The modal actually occupies: boxH (content) + 2 (padding 1*2) + 2 (border) = boxH+4 lines;
	// The overall stack = title(1) + subtitle(1) + empty(1) + modal(boxH+4) + empty(1) + hint(1) = boxH+9.
	// Therefore, we subtract 5 lines from boxH budget for decorations outside the modal to prevent terminal overflow.
	contentH := boxH - 5
	if contentH < 10 {
		contentH = 10
	}

	titleText, subtitleText := "Co-Creation Plan", "Clarify your requirements first, then start writing"
	if state.stage {
		titleText, subtitleText = "Mid-Stage Co-Creation", "Plan the next steps, then continue writing"
	}
	headerStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	title := headerStyle.Foreground(colorMuted).Bold(true).Render(titleText)
	subtitle := headerStyle.Foreground(colorDim).Italic(true).Render(subtitleText)

	var hintLine string
	hintStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	if quitPending {
		// quitPending is consistent with inputHints(); otherwise the co-creation modal covers the bottom bar, and the user won't feel "Press Ctrl+C again".
		hintLine = hintStyle.Foreground(lipgloss.Color("243")).Bold(true).Render("Press Ctrl+C again to exit")
	} else {
		hintLine = hintStyle.Foreground(colorDim).Italic(true).Render(coCreateHint(state))
	}

	body := renderCoCreateBody(boxW-4, contentH, state, errMsg, inputView, spinnerFrame)
	box := lipgloss.NewStyle().
		Width(boxW).
		Height(contentH).
		Border(baseBorder).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Render(body)

	stack := lipgloss.JoinVertical(lipgloss.Center, title, subtitle, "", box, "", hintLine)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, stack)
}

// coCreateHint generates a brief key binding hint based on the state to avoid repeating placeholder semantics.
func coCreateHint(state *cocreateState) string {
	switch {
	case state == nil:
		return "Enter Send · Esc Exit"
	case state.awaiting:
		return "AI responding · ↑↓ Scroll chat · Wheel scroll prompts · Esc Exit"
	case state.canStart():
		action := "Ctrl+S Start Writing"
		if state.stage {
			action = "Ctrl+S Apply and Continue"
		}
		return "Enter Add details · " + action + " · ↑↓ Scroll chat · Wheel scroll prompts · Esc Exit"
	default:
		return "Enter Send · ↑↓ Scroll chat · Wheel scroll prompts · Esc Exit"
	}
}

func renderCoCreateConversationPanel(width, height int, state *cocreateState, errMsg string, spinnerFrame int) string {
	// Do not draw own border — the right vertical line is uniformly drawn by the outer leftCol container.
	// Total column width = width; style.Width = contentW = width-2; content area = contentW-2 after Padding(0,1).
	// Subtract 2 columns for prefixes like "▌ " / "  " from the line width, otherwise wrapping will cause lines + prefix to overflow by 2 columns,
	// triggering terminal line wrapping — lipgloss thinks the modal height is fixed, but the actual terminal height increases,
	// causing "height jitter" of the outer box during streaming thinking. Thus wrapW = contentW - 4.
	contentW := width - 2
	if contentW < 12 {
		contentW = 12
	}
	wrapW := max(12, contentW-4)

	userRole := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Render("You")
	aiRole := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("AI")
	userBody := lipgloss.NewStyle().Foreground(colorAccent2)
	aiBody := lipgloss.NewStyle().Foreground(bodyTextColor)
	thinkingStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	thinkingTag := lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("AI Thinking")

	sysStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	var lines []string
	for i, item := range state.session.History() {
		isUser := item.Role != "assistant"
		// The synthesized kickoff message for mid-stage co-creation (always the user message at history[0]) is shown as a neutral system line,
		// not disguised as user input; it is still sent to the LLM as the kickoff user turn.
		if isUser && state.stage && i == 0 {
			for j, line := range wrapStreamText(stageCoCreateSystemLine, wrapW) {
				prefix := "· "
				if j > 0 {
					prefix = "  "
				}
				lines = append(lines, sysStyle.Render(prefix+line))
			}
			lines = append(lines, "")
			continue
		}
		if isUser {
			lines = append(lines, userRole)
			for _, line := range wrapStreamText(strings.TrimSpace(item.Content), wrapW) {
				// Render the entire line at once to avoid ANSI control char bleeding at the junction of prefix color reset and body color.
				lines = append(lines, userBody.Render("▌ "+line))
			}
		} else {
			lines = append(lines, aiRole)
			// history stores the full four Raw segments of assistant (used for model context), while UI only displays the [REPLY] segment.
			display := extractReplyForDisplay(item.Content)
			for _, line := range wrapStreamText(strings.TrimSpace(display), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
		}
		lines = append(lines, "")
	}

	if state.awaiting {
		if t := state.session.StreamThinking(); t != "" {
			lines = append(lines, thinkingTag)
			for _, line := range wrapStreamText(t, wrapW) {
				lines = append(lines, thinkingStyle.Render("  "+line))
			}
			lines = append(lines, "")
		}
		if state.streamReply() != "" {
			lines = append(lines, aiRole)
			for _, line := range wrapStreamText(state.streamReply(), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
			lines = append(lines, "")
		}
		// sparkle decoration: keep user informed that "AI is working"
		lines = append(lines, strings.TrimLeft(renderEventSparkle(spinnerFrame, contentW), " "))
	}
	if errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(colorError).Render("! "+errMsg))
	}

	// Use viewport instead of manual truncation, allowing users to scroll back.
	// vp height = panel height - 1 header line. After SetContent, if the user was at the bottom,
	// it auto-scrolls to the latest (stream follow); when the user scrolls up, convFollow is turned off to stop following.
	vpH := height - 1
	if vpH < 1 {
		vpH = 1
	}
	if state.convVP.Width != contentW || state.convVP.Height != vpH {
		state.convVP.Width = contentW
		state.convVP.Height = vpH
	}
	state.convVP.SetContent(strings.Join(lines, "\n"))
	if state.convFollow {
		state.convVP.GotoBottom()
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)
	return style.Render(panelTitleStyle.Render(":: Co-Creation Conversation") + "\n" + state.convVP.View())
}

func renderCoCreatePromptPanel(width, height int, state *cocreateState) string {
	readyLabel := "Ready to start writing"
	if state.stage {
		readyLabel = "Ready to apply and continue"
	}
	status := lipgloss.NewStyle().Foreground(colorDim).Render("Chatting...")
	if state.ready() {
		status = lipgloss.NewStyle().Foreground(colorAccent).Render(readyLabel)
	}
	if state.awaiting {
		status = lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render("AI organizing...")
	}

	// Content width = total column width - 2 (padding 0,1 occupies 2 columns, no border).
	contentW := width - 2
	if contentW < 8 {
		contentW = 8
	}

	emptyHint := "AI will continuously organize a final prompt here that can directly initiate writing."
	panelTitle := ":: Current Writing Instructions"
	if state.stage {
		emptyHint = "AI will continuously organize a brief for the subsequent phase directions here."
		panelTitle = ":: Subsequent Direction"
	}
	text := strings.TrimSpace(state.draftPrompt())
	if text == "" {
		text = lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render(emptyHint)
	} else {
		text = renderMarkdownPreview(text, max(12, contentW-2))
	}
	vpHeight := height - 5
	if vpHeight < 3 {
		vpHeight = 3
	}
	if state.promptVP.Width != contentW || state.promptVP.Height != vpHeight {
		state.promptVP.Width = contentW
		state.promptVP.Height = vpHeight
	}
	state.promptVP.MouseWheelEnabled = true
	state.promptVP.SetContent(text)

	hint := ""
	if state.promptVP.TotalLineCount() > state.promptVP.VisibleLineCount() {
		switch {
		case state.promptVP.AtTop():
			hint = "↓ More content below, scroll wheel or PgDn to view"
		case state.promptVP.AtBottom():
			hint = "↑ More content above, scroll wheel or PgUp to view"
		default:
			hint = "↑↓ Scroll wheel to view more"
		}
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)

	body := panelTitleStyle.Render(panelTitle) + "\n" + status + "\n\n" + state.promptVP.View()
	if hint != "" {
		body += "\n\n" + lipgloss.NewStyle().
			Width(contentW).
			AlignHorizontal(lipgloss.Center).
			Foreground(colorDim).
			Italic(true).
			Render(hint)
	}
	return style.Render(body)
}

func renderMarkdownPreview(text string, width int) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	h1Style := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	h3Style := lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	bulletStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	codeStyle := lipgloss.NewStyle().Foreground(colorMuted).Italic(true)

	var out []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			out = append(out, "")
			continue
		}

		switch {
		case strings.HasPrefix(line, "# "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			out = append(out, h1Style.Render(title))
		case strings.HasPrefix(line, "## "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			out = append(out, h2Style.Render(title))
		case strings.HasPrefix(line, "### "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			out = append(out, h3Style.Render(title))
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			body := strings.TrimSpace(line[2:])
			wrapped := wrapStreamText(body, max(8, width-4))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render("• ")+cardContentStyle.Render(item))
				} else {
					out = append(out, "  "+cardContentStyle.Render(item))
				}
			}
		case isOrderedMarkdownItem(line):
			prefix, body := splitOrderedMarkdownItem(line)
			wrapped := wrapStreamText(body, max(8, width-len(prefix)-2))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render(prefix+" ")+cardContentStyle.Render(item))
				} else {
					out = append(out, strings.Repeat(" ", len(prefix)+1)+cardContentStyle.Render(item))
				}
			}
		case strings.HasPrefix(line, "> "):
			body := strings.TrimSpace(strings.TrimPrefix(line, "> "))
			for _, item := range wrapStreamText(body, max(8, width-4)) {
				out = append(out, codeStyle.Render("│ "+item))
			}
		default:
			for _, item := range wrapStreamText(line, width) {
				out = append(out, cardContentStyle.Render(item))
			}
		}
	}
	return strings.Join(out, "\n")
}

func isOrderedMarkdownItem(line string) bool {
	if len(line) < 3 {
		return false
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func splitOrderedMarkdownItem(line string) (prefix, body string) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return "", strings.TrimSpace(line)
	}
	return line[:i+1], strings.TrimSpace(line[i+2:])
}
