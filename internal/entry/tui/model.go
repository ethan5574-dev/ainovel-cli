package tui

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/tools"
	"github.com/voocel/ainovel-cli/internal/utils"
)

const maxEvents = 500

// maxStreamRounds limits the number of rounds kept in the streaming panel. Each LLM call end triggers streamClear
// to start a new round. A single chapter writer takes about 3-5 rounds (agent header / thinking / draft / commit), and 32 rounds is roughly equivalent
// to looking back at the streaming output of the last 6-10 chapters. Committed chapter texts are stored in store/drafts, and excess rounds are discarded
// to prevent each token delta from triggering O(full text) re-rendering. Steady-state memory limit is around 512KB, far below the stutter threshold.
const maxStreamRounds = 32

type focusPane int

const (
	focusEvents focusPane = iota
	focusStream
	focusDetail
	focusState // left status sidebar (scrollable)

	focusPaneCount // total focus count, used for Tab rotation
)

type appMode int

const (
	modeNew     appMode = iota // waiting for user to input novel requirements
	modeRunning                // writing in progress (includes error stops, input can resume)
	modeDone                   // writing completed
)

// spinner frame sequence shared by top bar / streaming activity (bubbles.Spinner.MiniDot).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner frame sequence dedicated to event stream "in progress" lines (bubbles.Spinner.Dot).
// 7 dots + 1 gap rotating clockwise along a 3x3 grid, visually resembling a complete loading circle.
// Uses an independent frame index + faster tick, not affecting the tempo of the header and stars animations.
var toolSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Model is the top-level state of the TUI.
type Model struct {
	runtime        *host.Host
	askBridge      *askUserBridge
	askState       *askUserState
	cocreate       *cocreateState
	help           *helpState
	modelSwitch    *modelSwitchState
	report         *reportState
	version        string
	importer       *importState
	importSeq      int
	simulator      *simulationState
	simSeq         int
	compItems      []commandPaletteItem
	compIdx        int
	compActive     bool
	snapshot       host.UISnapshot
	events         []host.Event
	eventIndex     map[string]int   // event.ID -> m.events index; updated in place when call-type events arrive
	viewport       viewport.Model   // event stream viewport
	streamVP       viewport.Model   // streaming output viewport
	detailVP       viewport.Model   // right detail viewport
	stateVP        viewport.Model   // left status sidebar viewport (scrollable)
	streamBuf      *strings.Builder // streaming text accumulation buffer
	streamRounds   []string
	textarea       textarea.Model
	width          int
	height         int
	autoScroll     bool
	streamScroll   bool      // streaming panel auto-follow
	streamDirty    bool      // streamRounds has unflushed delta; merged at 60fps by streamFlushTick
	lastKeyAt      time.Time // time of last non-Enter keypress; KeyEnter throttle prevents paste \n stream from mis-triggering submission
	inputHistory   []string  // submitted input history (deduplicated: adjacent duplicates removed)
	historyIdx     int       // current browsing index; == len(inputHistory) means "not browsing, editing draft"
	historyDraft   string    // draft saved before entering history browsing, restored when returning to the end
	focusPane      focusPane
	hoverPane      focusPane
	hoverActive    bool
	mode           appMode
	startupMode    startupMode
	cocreateSeq    int
	reportSeq      int
	err            error
	spinnerIdx     int
	toolSpinnerIdx int  // independent frame index of the event stream in-progress line (150ms tick, does not affect header/stars)
	cursorIdx      int  // streaming cursor frame index (independent tick)
	streamRound    int  // streaming output round count
	quitPending    bool // double Ctrl+C exit confirmation
	abortPending   bool // manual pause waiting for Done to return
	mouseOff       bool // when true, mouse reporting is disabled to let the user copy text natively via drag-and-select; toggle again to restore
}

// NewModel creates the TUI Model.
func NewModel(rt *host.Host, bridge *askUserBridge, version string) Model {
	ta := textarea.New()
	ta.Placeholder = placeholderForNewMode(startupModeQuick)
	ta.CharLimit = 2000
	ta.SetHeight(1)
	// MaxHeight=6 lets extra-long input wrap automatically by width to show as multiple lines (visual limit of 6 lines).
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.Focus()

	// By default Enter does not insert a newline (submitted by handleEnterKey);
	// manual newlines are rebound to ctrl+j (unix \n) and alt+enter (GUI convention).
	// The terminal protocol layer cannot distinguish Shift+Enter from Enter, so Shift+Enter is not supported.
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")

	vp := viewport.New(80, 20)
	vp.SetContent("")

	svp := viewport.New(80, 10)
	svp.SetContent("")

	dvp := viewport.New(40, 20)
	dvp.SetContent("")

	stvp := viewport.New(32, 20)
	stvp.SetContent("")

	return Model{
		runtime:      rt,
		askBridge:    bridge,
		version:      strings.TrimSpace(version),
		autoScroll:   true,
		streamScroll: true,
		mode:         modeNew,
		startupMode:  startupModeQuick,
		textarea:     ta,
		viewport:     vp,
		streamVP:     svp,
		detailVP:     dvp,
		stateVP:      stvp,
		streamBuf:    &strings.Builder{},
		eventIndex:   make(map[string]int),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		listenEvents(m.runtime),
		listenAskUser(m.askBridge),
		listenDone(m.runtime),
		listenStream(m.runtime),
		tickSnapshot(m.runtime),
		bootstrapRuntime(m.runtime),
		tickSpinner(),
		tickToolSpinner(),
		tickCursor(),
		tickStreamFlush(),
	)
}

func (m *Model) paneAtMouse(x, y int) (focusPane, bool) {
	if m.width == 0 || m.height == 0 {
		return focusEvents, false
	}

	topH, _, bodyH := m.layoutHeights()
	if bodyH < 1 {
		return focusEvents, false
	}

	bodyStartY := topH
	bodyEndY := topH + bodyH
	if y < bodyStartY || y >= bodyEndY {
		return focusEvents, false
	}

	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	centerStartX := leftW
	rightStartX := m.width - rightW

	if x >= rightStartX {
		return focusDetail, true
	}
	if x < centerStartX {
		return focusState, true
	}

	eventH, _ := m.splitHeights(bodyH)
	if y-bodyStartY < eventH {
		return focusEvents, true
	}
	return focusStream, true
}

func (m *Model) paneHighlighted(pane focusPane) bool {
	if m.focusPane == pane {
		return true
	}
	return m.hoverActive && m.hoverPane == pane
}

// hasRunningEvent returns whether there are unfinished (spinner still turning) call-type events.
// toolSpinnerTick uses this to judge if re-rendering is worthwhile: when there are no running events, the spinner frame does not affect output,
// making the entire refreshEventViewport definitely useless work.
func (m *Model) hasRunningEvent() bool {
	for i := range m.events {
		if m.events[i].Running() {
			return true
		}
	}
	return false
}

// flushStreamIfDirty renders accumulated streamRounds to viewport; marked as flushed.
// Returns whether it was actually flushed, helping the caller decide if they should GotoBottom.
func (m *Model) flushStreamIfDirty() bool {
	if !m.streamDirty {
		return false
	}
	m.refreshStreamViewport()
	m.streamDirty = false
	return true
}

// refreshEventViewport re-renders event stream content and sets the viewport.
func (m *Model) refreshEventViewport() {
	centerW := m.eventFlowWidth()
	content := renderEventContent(m.events, centerW, m.toolSpinnerIdx)
	if activity := renderEventActivity(m.snapshot, m.spinnerIdx, centerW); activity != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n" + activity
		} else {
			content = activity
		}
	}
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) refreshStreamViewport() {
	cursor := ""
	if m.snapshot.IsRunning {
		cursor = renderStreamCursor(m.cursorIdx)
	}
	m.streamVP.SetContent(renderStreamContent(m.streamRounds, m.streamVP.Width, cursor))
}

func (m *Model) refreshDetailViewport() {
	rightW := m.detailWidth()
	if rightW <= 4 {
		return
	}
	m.detailVP.SetContent(renderDetailContent(m.snapshot, rightW-4))
}

// refreshStateViewport flushes left status sidebar content to the viewport.
// Sidebar content is purely derived from snapshot, so it must be re-flushed on snapshot or size changes.
func (m *Model) refreshStateViewport() {
	leftW := m.sidebarWidth()
	if leftW <= 4 {
		return
	}
	m.stateVP.SetContent(renderStateContent(m.snapshot, leftW-4))
}

// updateViewportSize updates viewport sizes based on current window dimensions.
func (m *Model) updateViewportSize() {
	centerW := m.eventFlowWidth()
	rightW := m.detailWidth()
	bodyH := m.bodyHeight()
	eventH, streamH := m.splitHeights(bodyH)
	m.viewport.Width = centerW - 2
	m.viewport.Height = eventH - 1 // -1 for event panel header line
	m.streamVP.Width = centerW - 2
	m.streamVP.Height = streamH - 1 // -1 for stream panel header line
	m.detailVP.Width = rightW - 2
	m.detailVP.Height = bodyH
	leftW := m.sidebarWidth()
	m.stateVP.Width = max(1, leftW-2)
	m.stateVP.Height = max(1, bodyH-2) // -2 for status bar Padding(1,1) vertical margins
}

// splitHeights calculates height allocation between event stream and streaming output.
func (m *Model) splitHeights(bodyH int) (eventH, streamH int) {
	eventH = bodyH * 40 / 100
	if eventH < 3 {
		eventH = 3
	}
	streamH = bodyH - eventH - 1 // -1 for divider
	if streamH < 3 {
		streamH = 3
	}
	return
}

func (m *Model) inputWidth() int {
	if m.width == 0 {
		return 60
	}
	return m.width - 6 // border + padding + prompt symbol "❯ "
}

func (m *Model) currentInputWidth() int {
	if m.cocreate != nil {
		return coCreateInputWidth(m.width, m.height)
	}
	return m.inputWidth()
}

// refitTextareaHeight estimates visual lines based on current content and dynamically sets SetHeight.
// Visual lines = logic lines (split by \n) with each segment wrapped by width. Works with MaxHeight=6
// to display extra-long content / manual newlines automatically as multiple lines, up to 6 lines.
func (m *Model) refitTextareaHeight() {
	w := m.textarea.Width()
	if w <= 0 {
		return
	}
	// In co-creation mode, input is fixed to 1 line: multiline content in the textarea will be scrolled by the textarea itself based on the cursor.
	// Otherwise, inputBox height would change with content, shrinking the left conversation pane
	// and causing input to drift vertically, breaking layout stability.
	if m.cocreate != nil {
		m.textarea.SetHeight(1)
		return
	}
	text := m.textarea.Value()
	if text == "" {
		m.textarea.SetHeight(1)
		return
	}
	// Deduct 2 columns of redundancy (textarea internal prompt symbol + cursor); slightly overestimating by 1 line is acceptable.
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}
	total := 0
	for line := range strings.SplitSeq(text, "\n") {
		lw := lipgloss.Width(line)
		if lw == 0 {
			total++
			continue
		}
		total += (lw + contentW - 1) / contentW
	}
	if total < 1 {
		total = 1
	}
	m.textarea.SetHeight(total) // SetHeight internally clamps by MaxHeight
}

// resizeTextarea synchronously sets width and content-based height.
// Replaces scattered SetWidth(currentInputWidth()) calls, ensuring height follows width changes.
func (m *Model) resizeTextarea() {
	m.textarea.SetWidth(m.currentInputWidth())
	m.refitTextareaHeight()
}

// maxInputHistory limits history length to prevent memory growth in long sessions.
const maxInputHistory = 200

// pushInputHistory appends successfully submitted content to history, deduplicating adjacent items. Synchronously resets browsing index.
func (m *Model) pushInputHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
		if len(m.inputHistory) > maxInputHistory {
			m.inputHistory = m.inputHistory[len(m.inputHistory)-maxInputHistory:]
		}
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

// tryHistoryUp moves to an earlier history entry; returns whether the keypress was handled.
// Upon entering history browsing for the first time, it stores the current textarea content as draft, restoring it when returning to the end.
// Callers must judge whether to bypass in multiline scenarios (letting textarea handle inline cursor movement).
func (m *Model) tryHistoryUp() bool {
	if len(m.inputHistory) == 0 || m.historyIdx <= 0 {
		return false
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyDraft = m.textarea.Value()
	}
	m.historyIdx--
	m.textarea.SetValue(m.inputHistory[m.historyIdx])
	m.textarea.CursorEnd()
	m.refitTextareaHeight()
	return true
}

// tryHistoryDown moves to a newer history entry; restores draft when returning to the end.
func (m *Model) tryHistoryDown() bool {
	if m.historyIdx >= len(m.inputHistory) {
		return false
	}
	m.historyIdx++
	if m.historyIdx == len(m.inputHistory) {
		m.textarea.SetValue(m.historyDraft)
		m.historyDraft = ""
	} else {
		m.textarea.SetValue(m.inputHistory[m.historyIdx])
	}
	m.textarea.CursorEnd()
	m.refitTextareaHeight()
	return true
}

// textareaIsMultiline returns whether the current textarea content contains manual newlines; decides if ↑↓ scrolls history or moves inline.
func (m *Model) textareaIsMultiline() bool {
	return strings.Contains(m.textarea.Value(), "\n")
}

// inputHints generates bottom hint text based on current state.
// Uniformly appends copySuffix at the end, letting the user see selection copying method in any non-urgent state;
// when mouse is off, displays a conspicuous red text prompt, reminding to press key again to restore mouse interaction.
func (m *Model) inputHints() string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	if m.quitPending {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Bold(true).Render("Press Ctrl+C again to exit")
	}
	// The welcome page (modeNew) does not enable mouse reporting, native terminal drag-and-select copying works directly without Ctrl+R hints;
	// workspace enables reporting, requiring Ctrl+R to temporarily disable it for copying.
	suffix := " · Ctrl+R selection copy mode"
	if m.mode == modeNew {
		suffix = ""
	}
	if m.mouseOff && m.mode != modeNew {
		// Workspace manually toggles to select and copy: prompts with accent color that it is currently in "free drag and select" state, press Ctrl+R to restore
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render("✂ Selection Copy Mode: drag to select text to copy · Ctrl+R to exit and restore mouse interaction")
	}
	if m.cocreate != nil {
		scrollHint := " · Tab Scroll: Conversation"
		if m.cocreate.focusPrompt {
			scrollHint = " · Tab Scroll: Prompt"
		}
		switch {
		case m.cocreate.awaiting:
			return dimStyle.Render("Waiting for AI reply · Esc Exit Co-creation" + scrollHint + suffix)
		case m.cocreate.canStart():
			startLabel := "Ctrl+S Start Writing"
			if m.cocreate.stage {
				startLabel = "Ctrl+S Apply and Continue"
			}
			return dimStyle.Render("Enter Send · " + startLabel + " · Esc Exit Co-creation" + scrollHint + suffix)
		default:
			return dimStyle.Render("Enter Send · Esc Exit Co-creation" + scrollHint + suffix)
		}
	}
	if m.mode == modeNew {
		if m.startupMode == startupModeQuick {
		}
		return dimStyle.Render("Tab Switch Startup Mode · Type / Search commands · Enter Start Co-creation · Esc Clear input" + suffix)
	}
	switch m.snapshot.RuntimeState {
	case "pausing":
		return dimStyle.Render("Pausing creation · Please wait for the current round to complete" + suffix)
	case "paused":
		return dimStyle.Render("Type / Search commands · Enter Continue writing · Esc Clear input" + suffix)
	}
	return dimStyle.Render("Type / Search commands · Click/Tab Switch panels · ↑↓ Scroll · End Go to end · Ctrl+L Clear screen · Esc Pause · Enter Send" + suffix)
}

func (m *Model) eventFlowWidth() int {
	if m.width == 0 {
		return 80
	}
	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	return m.width - leftW - rightW
}

func (m *Model) sidebarWidth() int {
	if m.width == 0 {
		return 32
	}
	return m.width * 23 / 100
}

func (m *Model) detailWidth() int {
	if m.width == 0 {
		return 40
	}
	return m.width * 27 / 100
}

func (m *Model) bodyHeight() int {
	_, _, bodyH := m.layoutHeights()
	return bodyH
}

func (m *Model) currentSpinnerFrame() string {
	if !m.snapshot.IsRunning {
		return ""
	}
	return spinnerFrames[m.spinnerIdx%len(spinnerFrames)]
}

func (m *Model) outputDir() string {
	if m.runtime == nil {
		return ""
	}
	return m.runtime.Dir()
}

func defaultSteerPlaceholder() string {
	return "Input plot steering, e.g. move romance to chapter 4"
}

func (m *Model) syncRuntimePlaceholder() {
	if m.mode != modeRunning || m.cocreate != nil {
		return
	}
	switch m.snapshot.RuntimeState {
	case "completed":
		m.textarea.Placeholder = "Writing completed"
	case "pausing":
		m.textarea.Placeholder = "Pausing writing..."
	case "paused":
		m.textarea.Placeholder = "Writing paused, type anything to resume"
	default:
		if !m.snapshot.IsRunning {
			m.textarea.Placeholder = "Execution paused, type anything to resume writing"
		} else {
			m.textarea.Placeholder = defaultSteerPlaceholder()
		}
	}
}

func (m *Model) renderBottomBar() string {
	inputBox := renderInputBox(
		m.textarea.View(),
		m.inputHints(),
		m.snapshot,
		m.outputDir(),
		m.width,
	)
	if m.mode != modeNew || m.cocreate != nil {
		return inputBox
	}
	return renderStartupModeBar(m.width, m.startupMode) + "\n" + inputBox
}

func (m *Model) layoutHeights() (topH, inputH, bodyH int) {
	if m.width == 0 || m.height == 0 {
		return 1, 4, 20
	}
	topH = lipgloss.Height(renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version))
	inputH = lipgloss.Height(m.renderBottomBar())
	bodyH = m.height - topH - inputH
	if bodyH < 3 {
		bodyH = 3
	}
	return
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}
	if m.width < 100 {
		return lipgloss.NewStyle().
			Width(m.width).Height(m.height).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render("Terminal width insufficient, please expand to at least 100 columns")
	}
	if m.askState != nil {
		return renderAskUserModal(m.width, m.height, m.askState)
	}
	if m.cocreate != nil {
		return renderCoCreateModal(m.width, m.height, m.cocreate, errorText(m.err), m.textarea.View(), m.spinnerIdx, m.quitPending)
	}
	if m.help != nil {
		return renderHelpModal(m.width, m.height, m.help)
	}
	if m.report != nil {
		return renderReportModal(m.width, m.height, m.report)
	}
	if m.importer != nil {
		return renderImportModal(m.width, m.height, m.importer)
	}
	if m.simulator != nil {
		return renderSimulationModal(m.width, m.height, m.simulator)
	}

	topBar := renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version)
	inputBox := m.renderBottomBar()
	_, inputH, bodyH := m.layoutHeights()

	var body string
	if m.mode == modeNew {
		errMsg := ""
		if m.err != nil {
			errMsg = m.err.Error()
		}
		body = renderWelcome(m.width, bodyH, errMsg, m.startupMode)
	} else {
		leftW := m.sidebarWidth()
		rightW := m.detailWidth()
		centerW := m.width - leftW - rightW
		eventH, streamH := m.splitHeights(bodyH)

		if m.viewport.Width != centerW-2 || m.viewport.Height != eventH-1 {
			m.viewport.Width = centerW - 2
			m.viewport.Height = eventH - 1 // -1 for the event panel header line
		}
		if m.streamVP.Width != centerW-2 || m.streamVP.Height != streamH-1 {
			m.streamVP.Width = centerW - 2
			m.streamVP.Height = streamH - 1 // -1 for the stream panel header line
		}

		eventFlow := renderEventFlowViewport(m.viewport, centerW, eventH, m.paneHighlighted(focusEvents))
		streamPanel := renderStreamPanel(m.streamVP, centerW, streamH, m.paneHighlighted(focusStream), m.snapshot.IsRunning, m.spinnerIdx)
		center := lipgloss.JoinVertical(lipgloss.Left, eventFlow, streamPanel)

		left := renderStatePanel(m.stateVP, leftW, bodyH, m.paneHighlighted(focusState))
		right := renderDetailPanel(m.detailVP, rightW, bodyH, m.paneHighlighted(focusDetail))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	}

	view := lipgloss.JoinVertical(lipgloss.Left, topBar, body, inputBox)

	// Modal overlay stack: floats above the bottom of the body without affecting the layout
	if m.modelSwitch != nil {
		commandBar := renderModelSwitchBar(m.width, m.modelSwitch)
		view = overlayAboveInput(view, commandBar, inputH)
	} else if m.compActive {
		commandBar := renderCommandPalette(m.width, m.compItems, m.compIdx)
		view = overlayAboveInput(view, commandBar, inputH)
	}
	return view
}

// sendCoCreate initiates a round of co-creation requests, uniformly handling reqID, textarea, and placeholder.
func (m *Model) sendCoCreate() tea.Cmd {
	m.cocreateSeq++
	m.cocreate.reqID = m.cocreateSeq
	m.cocreate.awaiting = true
	m.resizeTextarea()
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	m.textarea.Blur()
	return runCoCreate(m.runtime, m.cocreate)
}

func (m Model) handleCoCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil {
		return m, nil
	}
	state := m.cocreate

	// Keyboard ↑↓/PgUp/PgDn/Home/End scrolling; Tab switches scroll focus between left conversation and right prompt panels
	// (defaults to left panel for users to read the history). Welcome page disables mouse reporting to preserve native copy, right panel overflow relies on Tab
	// to switch focus and scroll with keyboard. Left panel: scroll up disables follow, scrolling to bottom re-enables follow (stream follow).
	switch msg.Type {
	case tea.KeyTab:
		state.focusPrompt = !state.focusPrompt
		return m, nil
	case tea.KeyUp, tea.KeyPgUp:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		state.convFollow = false
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		return m, cmd
	case tea.KeyDown, tea.KeyPgDown:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		if state.convVP.AtBottom() {
			state.convFollow = true
		}
		return m, cmd
	case tea.KeyHome:
		if state.focusPrompt {
			state.promptVP.GotoTop()
			return m, nil
		}
		state.convFollow = false
		state.convVP.GotoTop()
		return m, nil
	case tea.KeyEnd:
		if state.focusPrompt {
			state.promptVP.GotoBottom()
			return m, nil
		}
		state.convFollow = true
		state.convVP.GotoBottom()
		return m, nil
	case tea.KeyEsc:
		return m.exitCoCreate()
	}

	// Allow editing keys (character input/backspace/cursor/Ctrl+U/multiline newline) while waiting for AI reply —
	// users can pre-type the next sentence during AI thinking. Submission blocking is lowered into each case,
	// letting Enter throttle precede awaiting check — this way, pasted \n fragments can still be replaced with spaces.

	switch msg.Type {
	case tea.KeyCtrlS:
		if state.awaiting {
			return m, nil
		}
		if !state.canStart() {
			return m, nil
		}
		// Mid-stage co-creation: inject subsequent direction brief and resume writing, returning to the workspace.
		if state.stage {
			draft := state.draftPrompt()
			m.cocreate = nil
			m.err = nil
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(resumeFromCoCreate(m.runtime, draft), m.textarea.Focus())
		}
		// Cold-start co-creation: start writing with the organized prompt.
		plan, err := state.buildPlan()
		if err != nil {
			m.err = err
			return m, nil
		}
		state.awaiting = true
		m.textarea.Blur()
		return m, startRuntime(m.runtime, plan)
	case tea.KeyEnter:
		// Alt+Enter -> manual newline, let textarea.Update handle it (KeyMap.InsertNewline is bound to this key)
		if msg.Alt {
			break
		}
		// Interval with last character keypress is too short -> treated as a pasted \n fragment: pad space instead of submitting.
		// Must check before awaiting block — otherwise pasted \n fragments during awaiting would be blocked,
		// causing "abc\ndef" to be swallowed into "abcdef", inconsistent with the base path semantics.
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		// True submission intent: blocked during awaiting (no concurrent requests allowed)
		if state.awaiting {
			return m, nil
		}
		text := utils.CleanInputLine(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.err = nil
		state.appendUser(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		cmd := m.sendCoCreate()
		return m, cmd
	case tea.KeyCtrlU:
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m, nil
	}

	// Numeric keys 1/2/3 when textarea is empty and suggestions exist -> fill corresponding suggestion (no send, editable).
	// Only intercepted when input box is empty, avoiding affecting users typing numbers. When awaiting, suggestions are not shown,
	// and no extra check is needed here (skips if state.suggestions is empty).
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && !state.awaiting {
		if r := msg.Runes[0]; r >= '1' && r <= '3' {
			if strings.TrimSpace(m.textarea.Value()) == "" {
				if sugs := state.suggestions(); int(r-'0') <= len(sugs) {
					m.textarea.SetValue(sugs[r-'1'])
					m.refitTextareaHeight()
					return m, nil
				}
			}
		}
	}

	// Forward regular input to textarea
	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	return m, cmd
}

// exitCoCreate exits co-creation mode, cancels the in-progress LLM request, and restores input box state.
func (m Model) exitCoCreate() (tea.Model, tea.Cmd) {
	if m.cocreate.cancel != nil {
		m.cocreate.cancel()
	}
	stage := m.cocreate.stage
	initial := m.cocreate.initialInput()
	m.cocreate = nil
	m.resizeTextarea()
	// Mid-stage co-creation cancel: clears occupation flag, keeps paused, returns to workspace input state (does not backfill synthesized opening).
	if stage {
		m.textarea.SetValue("")
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(cancelCoCreate(m.runtime), fetchSnapshot(m.runtime), m.textarea.Focus())
	}
	m.textarea.SetValue(initial)
	m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
	return m, m.textarea.Focus()
}

func (m Model) handleAskUserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.askState == nil {
		return m, nil
	}
	state := m.askState
	q := state.currentQuestion()

	if state.typing {
		switch msg.Type {
		case tea.KeyEsc:
			state.cancelCurrentTyping()
			return m, nil
		case tea.KeyEnter:
			if state.finishCurrentAnswer() {
				state.submit()
				m.askState = nil
				return m, m.textarea.Focus()
			}
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			if state.input != "" {
				_, size := utf8.DecodeLastRuneInString(state.input)
				state.input = state.input[:len(state.input)-size]
			}
			return m, nil
		default:
			if msg.Type == tea.KeyRunes {
				state.input += utils.CleanInputRunes(msg.Runes)
			}
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyEsc:
		// Close popup, return empty answers
		state.request.resultCh <- askUserResult{
			resp: &tools.AskUserResponse{
				Answers: make(map[string]string),
				Notes:   make(map[string]string),
			},
		}
		m.askState = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		state.moveCursor(-1)
	case tea.KeyDown:
		state.moveCursor(1)
	case tea.KeySpace:
		if q.MultiSelect {
			state.toggleSelection()
			if state.cursor == len(q.Options) && !state.selected[state.cursor] {
				state.input = ""
			}
		}
	case tea.KeyEnter:
		if q.MultiSelect {
			if state.cursor == len(q.Options) {
				state.toggleSelection()
				if state.selected[state.cursor] {
					state.typing = true
				}
				return m, nil
			}
			if len(state.selected) == 0 {
				state.toggleSelection()
			}
		}
		if state.finishCurrentAnswer() {
			state.submit()
			m.askState = nil
			return m, m.textarea.Focus()
		}
	}
	return m, nil
}

// overlayAboveInput floats and overlays overlay on the bottom of the base view (above inputBox),
// without changing overall layout height. Only covers overlay card's own width, letting underlying content peek through on the right.
func overlayAboveInput(base, overlay string, inputLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(strings.TrimRight(overlay, "\n"), "\n")

	endY := len(baseLines) - inputLineCount
	startY := endY - len(overLines)
	if startY < 0 {
		startY = 0
	}

	for i, ol := range overLines {
		y := startY + i
		if y >= 0 && y < endY {
			olW := lipgloss.Width(ol)
			// Truncate olW visible characters from the left side of the baseline, concatenate overlay + remaining right side content
			right := ansi.TruncateLeft(baseLines[y], olW, "")
			baseLines[y] = ol + right
		}
	}
	return strings.Join(baseLines, "\n")
}

// isCSILeak checks if KeyRunes is a fragment leaked from a CSI escape sequence.
// When the terminal sends arrow key \x1b[A, rapid keypresses may cause sequence splitting:
// \x1b is parsed as Escape, and "[" or "[A" leaks into textarea as KeyRunes.
func isCSILeak(runes []rune) bool {
	if len(runes) == 0 || runes[0] != '[' {
		return false
	}
	for _, r := range runes[1:] {
		if (r >= '0' && r <= '9') || r == ';' ||
			(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
			continue
		}
		return false
	}
	return true
}

// containsSGRFragment checks if text contains an SGR mouse sequence fragment ("<number;number;" pattern).
func containsSGRFragment(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		j := i + 1
		if j >= len(s) || s[j] < '0' || s[j] > '9' {
			continue
		}
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == ';' {
			return true
		}
	}
	return false
}

func cleanHumanKeyRunes(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if msg.Type != tea.KeyRunes {
		return msg, true
	}
	cleaned := utils.CleanInputRunes(msg.Runes)
	if cleaned == "" {
		return msg, false
	}
	msg.Runes = []rune(cleaned)
	return msg, true
}
