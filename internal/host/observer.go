package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/utils"
	"github.com/voocel/litellm"
)

// errorKind classifies a runtime error into a stable, short label for log
// filtering and alert routing. Returns "" when no special tag applies.
//
// err is the live error chain (may be nil after JSON serialization); msg is
// the rendered string fallback used when the chain has been flattened
// (e.g. inside sub-agent JSON results).
func errorKind(err error, msg string) string {
	if err != nil && litellm.IsStreamIdleError(err) {
		return "stream_idle"
	}
	if msg != "" && agentcore.IsStreamIdleMessage(msg) {
		return "stream_idle"
	}
	return ""
}

// Monotonically increasing event ID counter; used with timestamps to generate stable IDs.
var eventIDCounter uint64

func nextEventID() string {
	return fmt.Sprintf("e%d", atomic.AddUint64(&eventIDCounter, 1))
}

// activeCall records the ID, start time, and summary of an in-progress call (TOOL / DISPATCH).
// The summary is filled back into the finish Event when completed, ensuring replay (runtime queue) can reconstruct row content.
type activeCall struct {
	id      string
	start   time.Time
	summary string
	depth   int
}

// observer subscribes to the coordinator event stream and projects it to the Host's output channel.
// It is a pure observer and does not participate in any control decisions.
type observer struct {
	unsub   func()
	emitEv  func(Event)
	emitD   func(string)
	emitC   func()
	store   *storepkg.Store // Used for runtime queue persistence (consumed by ReplayQueue)
	agents  map[string]*agentState
	agentMu sync.Mutex

	// aborting is set by Host at Abort()/Close() entries and cleared at Start/Resume/Continue.
	// During this period, all error events derived from context-cancel are suppressed (both expected by the user and avoiding
	// duplication with "user manual pause" events). Real anomalies (non-cancel) are still reported normally.
	aborting atomic.Bool

	streamThinking        bool
	lastThinkingByAgent   map[string]string          // agent -> recent accumulated thinking text (used to extract incremental delta)
	dispatchStarts        map[string]*activeCall     // dispatched agent -> in-progress DISPATCH calls
	currentDispatchTarget string                     // Name of the currently executing subagent (Args might be empty in handleToolEnd)
	toolStarts            map[string]*activeCall     // agent -> in-progress TOOL calls
	streamExtractors      map[string]*agentExtractor // agent -> content extractor for current tool call JSON parameters
	streamHasContent      bool                       // Whether the current streamRound has output content (used to determine if paragraph separation is needed)
	streamLastByte        byte                       // The last byte of the stream output (used to accurately pad newlines)
}

// agentExtractor records the tool name and extractor instance currently being extracted by a certain agent.
// The tool name is used to detect "a new tool call has started", preventing the cache from being polluted by residues of the previous round.
type agentExtractor struct {
	tool       string
	ext        *jsonFieldExtractor
	emittedAny bool // Whether this extractor has produced content; used to pad paragraph separation before first output
}

type agentState struct {
	name    string
	state   string
	tool    string
	summary string
	turn    int
	context AgentContextSnapshot
	updated time.Time
}

func newObserver(coordinator *agentcore.Agent, s *storepkg.Store, emitEv func(Event), emitD func(string), emitC func()) *observer {
	o := &observer{
		emitEv:              emitEv,
		emitD:               emitD,
		emitC:               emitC,
		store:               s,
		agents:              make(map[string]*agentState),
		lastThinkingByAgent: make(map[string]string),
		dispatchStarts:      make(map[string]*activeCall),
		toolStarts:          make(map[string]*activeCall),
		streamExtractors:    make(map[string]*agentExtractor),
	}
	o.unsub = coordinator.Subscribe(o.handle)
	return o
}

func (o *observer) finalize() {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	for _, a := range o.agents {
		a.state = "idle"
		a.tool = ""
	}
}

// setAborting is called by Host at lifecycle switches such as Abort/Close/Start, controlling
// whether "context canceled" derived events need to be suppressed (avoiding duplication with "user manual pause").
func (o *observer) setAborting(v bool) { o.aborting.Store(v) }

// isCancellationNoise determines whether an error is a derived noise caused by abort.
// It is meaningful to return true only when Host is in the aborting state—context.Canceled during non-abort
// periods may reflect a real issue (such as external ctx cancelled) and should still be reported.
func (o *observer) isCancellationNoise(err error, msg string) bool {
	if !o.aborting.Load() {
		return false
	}
	if err != nil && errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(msg), "context canceled")
}

// emitAndLog is used for the "started" state of call-like events: sent to TUI but not written to runtime queue,
// preventing duplication of "started one line, finished another" during replay. slog is uniformly logged by host.emitEvent.
func (o *observer) emitAndLog(ev Event) {
	o.emitEv(ev)
}

// persistEvent writes the event into the runtime queue (slog is uniformly logged by host.emitEvent).
func (o *observer) persistEvent(ev Event) {
	if o.store == nil || o.store.Runtime == nil {
		return
	}
	priority := domain.RuntimePriorityBackground
	switch ev.Category {
	case "SYSTEM", "ERROR":
		priority = domain.RuntimePriorityControl
	}
	_, _ = o.store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Time:     ev.Time,
		Kind:     domain.RuntimeQueueUIEvent,
		Priority: priority,
		Category: ev.Category,
		Summary:  ev.Summary,
		Payload:  ev,
	})
}

func (o *observer) handle(ev agentcore.Event) {
	switch ev.Type {
	case agentcore.EventToolExecStart:
		o.handleToolStart(ev)
	case agentcore.EventToolExecUpdate:
		o.handleToolUpdate(ev)
	case agentcore.EventToolExecEnd:
		o.handleToolEnd(ev)
	case agentcore.EventMessageUpdate:
		o.handleMessageUpdate(ev)
	case agentcore.EventMessageEnd:
		o.streamClear()
	case agentcore.EventTurnStart:
		if ev.Progress != nil && ev.Progress.Kind == agentcore.ProgressTurnCounter {
			o.updateAgent(ev.Progress.Agent, func(a *agentState) {
				a.turn = ev.Progress.Turn
			})
		}
	case agentcore.EventRetry:
		if ev.RetryInfo != nil {
			msg := ""
			if ev.RetryInfo.Err != nil {
				msg = ev.RetryInfo.Err.Error()
			}
			prefix := fmt.Sprintf("Retry (%d/%d): ", ev.RetryInfo.Attempt, ev.RetryInfo.MaxRetries)
			retryEv := Event{
				Time:     time.Now(),
				Category: "SYSTEM",
				Summary:  prefix + truncate(msg, 80),
				Detail:   prefix + msg,
				Kind:     errorKind(ev.RetryInfo.Err, msg),
				Level:    "warn",
			}
			o.emitEv(retryEv)
			o.persistEvent(retryEv)
		}
	case agentcore.EventError:
		if ev.Err != nil {
			fullMsg := ev.Err.Error()
			if o.isCancellationNoise(ev.Err, fullMsg) {
				// Ctx-cancel error derived from user-initiated abort; "User manual pause" event already exists, do not duplicate.
				slog.Debug("suppressed cancel-derived error", "module", "agent", "msg", fullMsg)
				return
			}
			errEv := Event{
				Time:     time.Now(),
				Category: "ERROR",
				Summary:  truncate(fullMsg, 120),
				Detail:   fullMsg,
				Kind:     errorKind(ev.Err, fullMsg),
				Level:    "error",
			}
			o.emitEv(errEv)
			o.persistEvent(errEv)
		}
	}
}

func (o *observer) handleMessageUpdate(ev agentcore.Event) {
	if ev.Delta == "" {
		return
	}
	// The Coordinator's tool-call parameters are sub-agent task JSON with no readable content, discarded directly.
	if ev.DeltaKind == agentcore.DeltaToolCall {
		return
	}
	o.emitStreamDelta(ev.Delta, ev.DeltaKind == agentcore.DeltaThinking)
}

func (o *observer) handleToolStart(ev agentcore.Event) {
	if ev.Tool == "" {
		return
	}
	agent := agentFromEvent(ev)

	// subagent call -> DISPATCH event (in progress)
	if ev.Tool == "subagent" {
		sub := parseSubagentArgs(ev.Args)
		target := sub.agent
		if target == "" {
			target = "subagent"
		}
		dispatchSummary := target
		if sub.task != "" {
			firstLine := strings.TrimSpace(strings.SplitN(sub.task, "\n", 2)[0])
			if firstLine != "" {
				dispatchSummary += " (" + truncate(firstLine, 30) + ")"
			}
		}
		o.updateAgent(agent, func(a *agentState) {
			a.state = "working"
			a.tool = ev.Tool
			a.summary = fmt.Sprintf("%s → %s", agent, dispatchSummary)
		})
		o.currentDispatchTarget = target
		id := nextEventID()
		o.dispatchStarts[target] = &activeCall{id: id, start: time.Now(), summary: dispatchSummary}
		o.emitAndLog(Event{
			ID:       id,
			Time:     time.Now(),
			Category: "DISPATCH",
			Agent:    agent,
			Summary:  dispatchSummary,
			Level:    "info",
		})
		return
	}

	// coordinator own tool (in progress)
	toolName := displayToolName(ev.Tool, ev.Args)
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = ev.Tool
		a.summary = fmt.Sprintf("%s → %s", agent, toolName)
	})
	id := nextEventID()
	o.toolStarts[agent] = &activeCall{id: id, start: time.Now(), summary: toolName}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "TOOL",
		Agent:    agent,
		Summary:  toolName,
		Level:    "info",
	})
	o.emitFallbackStreamHeader(ev.Tool)
}

func (o *observer) handleToolUpdate(ev agentcore.Event) {
	if ev.Progress == nil {
		return
	}
	switch ev.Progress.Kind {
	case agentcore.ProgressToolDelta:
		if ev.Progress.Delta != "" {
			o.handleSubagentDelta(ev.Progress)
		}
	case agentcore.ProgressToolStart:
		// Tool call inside a sub-agent (e.g. writer -> draft_chapter).
		// Note: The TOOL line might have been emitted early by handleSubagentDelta during the streaming phase.
		// Here: If already emitted -> only update summary (args are complete now, showing "tool(ChN)"); otherwise emit normally.
		if ev.Progress.Agent == "" || ev.Progress.Tool == "" {
			break
		}
		toolName := displayToolName(ev.Progress.Tool, ev.Progress.Args)
		if call, ok := o.toolStarts[ev.Progress.Agent]; ok {
			if toolName != "" && toolName != call.summary {
				call.summary = toolName
				// Emit summary-only update event (same ID), TUI applyEvent will merge them
				o.emitEv(Event{
					ID:       call.id,
					Time:     call.start,
					Category: "TOOL",
					Agent:    ev.Progress.Agent,
					Summary:  toolName,
					Level:    "info",
					Depth:    call.depth,
				})
			}
			o.updateAgent(ev.Progress.Agent, func(a *agentState) {
				a.state = "working"
				a.tool = ev.Progress.Tool
				a.summary = fmt.Sprintf("%s → %s", ev.Progress.Agent, toolName)
			})
			break
		}
		// Not emitted early -> normal flow
		// (Models with non-streaming tool args will not trigger ensureSubagentToolStarted,
		// fallback header must be padded along this path, otherwise read-only tools like read_chapter
		// without an extractor will not have a ✻ header in the streaming panel, sticking directly to the previous thinking block.)
		id := nextEventID()
		o.toolStarts[ev.Progress.Agent] = &activeCall{id: id, start: time.Now(), summary: toolName, depth: 1}
		o.emitAndLog(Event{
			ID:       id,
			Time:     time.Now(),
			Category: "TOOL",
			Agent:    ev.Progress.Agent,
			Summary:  toolName,
			Level:    "info",
			Depth:    1,
		})
		o.updateAgent(ev.Progress.Agent, func(a *agentState) {
			a.state = "working"
			a.tool = ev.Progress.Tool
			a.summary = fmt.Sprintf("%s → %s", ev.Progress.Agent, toolName)
		})
		o.emitFallbackStreamHeader(ev.Progress.Tool)
	case agentcore.ProgressToolEnd:
		delete(o.streamExtractors, ev.Progress.Agent)
		if ev.Progress.Agent == "" {
			return
		}
		call, ok := o.toolStarts[ev.Progress.Agent]
		if !ok {
			return
		}
		delete(o.toolStarts, ev.Progress.Agent)
		// Same-ID update event: TUI locates the original TOOL line by ID, filling in FinishedAt / Duration.
		// Summary / Depth are also included, ensuring the complete line can be restored during runtime queue replay.
		finishEv := Event{
			ID:         call.id,
			Time:       call.start,
			FinishedAt: time.Now(),
			Category:   "TOOL",
			Agent:      ev.Progress.Agent,
			Summary:    call.summary,
			Level:      "info",
			Depth:      call.depth,
			Duration:   time.Since(call.start),
		}
		o.emitEv(finishEv)
		o.persistEvent(finishEv)
	case agentcore.ProgressThinking:
		o.handleThinkingProgress(ev)
	case agentcore.ProgressRetry:
		prefix := fmt.Sprintf("Retry (%d/%d): ", ev.Progress.Attempt, ev.Progress.MaxRetries)
		retryEv := Event{
			Time:     time.Now(),
			Category: "SYSTEM",
			Agent:    ev.Progress.Agent,
			Summary:  prefix + truncate(ev.Progress.Message, 80),
			Detail:   prefix + ev.Progress.Message,
			Kind:     errorKind(nil, ev.Progress.Message),
			Level:    "warn",
			Depth:    1,
		}
		o.emitEv(retryEv)
		o.persistEvent(retryEv)
	case agentcore.ProgressToolError:
		delete(o.streamExtractors, ev.Progress.Agent)
		msg := ev.Progress.Message
		if msg == "" {
			msg = "unknown error"
		}
		// If there is an in-progress TOOL row, mark it as failed in place; otherwise append an independent ERROR row.
		if call, ok := o.toolStarts[ev.Progress.Agent]; ok {
			delete(o.toolStarts, ev.Progress.Agent)
			finishEv := Event{
				ID:         call.id,
				Time:       call.start,
				FinishedAt: time.Now(),
				Failed:     true,
				Category:   "TOOL",
				Agent:      ev.Progress.Agent,
				Summary:    call.summary,
				Level:      "error",
				Depth:      call.depth,
				Duration:   time.Since(call.start),
			}
			o.emitEv(finishEv)
			o.persistEvent(finishEv)
		}
		// Append ERROR detail row (supplementary error message for troubleshooting)
		errEv := Event{
			Time:     time.Now(),
			Category: "ERROR",
			Agent:    ev.Progress.Agent,
			Summary:  fmt.Sprintf("%s Error: %s", ev.Progress.Tool, truncate(msg, 100)),
			Detail:   fmt.Sprintf("%s Error: %s", ev.Progress.Tool, msg),
			Kind:     errorKind(nil, msg),
			Level:    "error",
			Depth:    1,
		}
		o.emitEv(errEv)
		o.persistEvent(errEv)
	case agentcore.ProgressContext:
		o.handleContextProgress(ev)
	}
}

// handleSubagentDelta splits the subagent's text and tool call parameters:
// - DeltaText flows out directly as markdown
// - DeltaToolCall only extracts fields for known long-content tools (such as draft_chapter.content); parameters JSON of other tools are discarded entirely
func (o *observer) handleSubagentDelta(p *agentcore.ProgressPayload) {
	if p.DeltaKind != agentcore.DeltaToolCall {
		o.emitStreamDelta(p.Delta, false)
		return
	}
	if p.Tool == "" {
		return // Tool name not ready, try again in next delta
	}

	// Emit TOOL in-progress event early when the tool name is stream-recognized, making the spinner cover the entire LLM generation period
	// (otherwise the "in-progress" state for tools like draft_chapter would only show during the tens of milliseconds of actual Execute).
	// When the real ProgressToolStart arrives, it recognizes that toolStarts already has a record and will only fill in the summary.
	o.ensureSubagentToolStarted(p.Agent, p.Tool)

	cur, ok := o.streamExtractors[p.Agent]
	// After the same tool call args are closed (top-level } hit), trailing deltas may still be received:
	// Some providers (tested with deepseek-v4-flash) split a single args into multiple chunks,
	// and the last chunk still has spaces or duplicate characters after `}`. In this case, if processed by "tool name match +
	// Done i.e. rebuild", the new extractor will emit a ✻ header again and parse the tail token as new args.
	// These deltas are redundant tails, discard them.
	if ok && cur.tool == p.Tool && cur.ext.Done() {
		return
	}
	// Tool name changed or not created yet: create a new one.
	if !ok || cur.tool != p.Tool {
		ext := newToolExtractor(p.Tool)
		if ext == nil {
			delete(o.streamExtractors, p.Agent)
			return
		}
		cur = &agentExtractor{tool: p.Tool, ext: ext}
		o.streamExtractors[p.Agent] = cur
	}
	if emitted := cur.ext.Feed(p.Delta); emitted != "" {
		if !cur.emittedAny {
			cur.emittedAny = true
			// streamClear makes the extractor's ✻ header fall on the new round starting point, cooperating with
			// renderStreamContent's HasPrefix("✻") check to take the renderAgentBlock highlight
			// path; using ensureStreamParagraphBreak only inserts empty lines without opening a new round, and ✻ will still be
			// wrapped by previous thinking/body text, falling to renderChapterBlock and rendered in default color.
			o.streamClear()
			// streamClear defensively cleared streamExtractors. The current cur still needs to continue to Feed
			// subsequent deltas of this tool call, so it must be re-registered immediately; otherwise the next delta
			// will create a new extractor, parsing from the middle of args (only entering psBeforeKey at nested object `{`),
			// treating timeline_events.time / foreshadow_updates.id etc. as top-level fields, causing ✻ headers to repeat on TUI.
			o.streamExtractors[p.Agent] = cur
		}
		o.emitStreamDelta(emitted, false)
	}
}

func (o *observer) handleThinkingProgress(ev agentcore.Event) {
	agent := ev.Progress.Agent
	thinking := ev.Progress.Thinking
	if agent == "" || thinking == "" {
		return
	}

	prev := o.lastThinkingByAgent[agent]
	delta := thinking
	if strings.HasPrefix(thinking, prev) {
		delta = thinking[len(prev):]
	}
	o.lastThinkingByAgent[agent] = thinking
	if delta == "" {
		return
	}
	o.emitStreamDelta(delta, true)
}

func (o *observer) handleContextProgress(ev agentcore.Event) {
	if ev.Progress == nil || len(ev.Progress.Meta) == 0 {
		return
	}
	var payload struct {
		Tokens        int     `json:"tokens"`
		ContextWindow int     `json:"context_window"`
		Percent       float64 `json:"percent"`
		Scope         string  `json:"scope"`
		Strategy      string  `json:"strategy"`
	}
	if json.Unmarshal(ev.Progress.Meta, &payload) != nil {
		return
	}

	agent := ev.Progress.Agent
	if agent == "" {
		agent = "coordinator"
	}

	// Update agent snapshot (always visible in TUI sidebar)
	o.updateAgent(agent, func(a *agentState) {
		a.context = AgentContextSnapshot{
			Tokens:        payload.Tokens,
			ContextWindow: payload.ContextWindow,
			Percent:       payload.Percent,
			Scope:         payload.Scope,
			Strategy:      payload.Strategy,
		}
	})

	level := "info"
	if payload.Percent > 85 {
		level = "warn"
	}
	summary := fmt.Sprintf("%s Context %.0f%% (%d/%d) Strategy: %s", agent, payload.Percent, payload.Tokens, payload.ContextWindow, payload.Strategy)

	depth := 0
	if agent != "coordinator" {
		depth = 1
	}

	if payload.Strategy != "" {
		// Compression triggered -> event stream + log
		ctxEv := Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: summary, Level: level, Depth: depth}
		o.emitEv(ctxEv)
		o.persistEvent(ctxEv)
	} else {
		// Normal usage report -> log only
		slogLevel := slog.LevelInfo
		if level == "warn" {
			slogLevel = slog.LevelWarn
		}
		slog.Log(context.Background(), slogLevel, summary, "module", "context", "agent", agent)
	}
}

func (o *observer) handleToolEnd(ev agentcore.Event) {
	agent := agentFromEvent(ev)
	// Tool finished: switch state back to idle, otherwise the sidebar will stay on working forever.
	// When sub-agent dispatch finishes, the state of dispatchTarget will be cleared separately below.
	o.updateAgent(agent, func(a *agentState) {
		a.tool = ""
		a.state = "idle"
	})
	delete(o.lastThinkingByAgent, agent)

	// Retrieve the DISPATCH record in progress (ev.Args of handleToolEnd may be empty, retrieve from currentDispatchTarget)
	var dispatchCall *activeCall
	var dispatchTarget string
	if ev.Tool == "subagent" {
		dispatchTarget = o.currentDispatchTarget
		o.currentDispatchTarget = ""
		if dispatchTarget == "" {
			if sub := parseSubagentArgs(ev.Args); sub.agent != "" {
				dispatchTarget = sub.agent
			}
		}
		if dispatchTarget == "" {
			dispatchTarget = "subagent"
		}
		if call, ok := o.dispatchStarts[dispatchTarget]; ok {
			dispatchCall = call
			delete(o.dispatchStarts, dispatchTarget)
		}
		// Dispatch finished: reset sub-agent state to idle (this cleanup is needed for success/failure/error paths)
		if dispatchTarget != "subagent" {
			o.updateAgent(dispatchTarget, func(a *agentState) {
				a.state = "idle"
				a.tool = ""
			})
		}
	}

	// Retrieve in-progress record of coordinator's direct tool (non-subagent) (rare, but guarantees consistency)
	var toolCall *activeCall
	if ev.Tool != "subagent" {
		if call, ok := o.toolStarts[agent]; ok {
			toolCall = call
			delete(o.toolStarts, agent)
		}
	}

	// Unified call completion status (success/failure), updating the original line using the same ID
	emitFinish := func(call *activeCall, category, agentName string, failed bool) {
		if call == nil {
			return
		}
		level := "success"
		if failed {
			level = "error"
		}
		finishEv := Event{
			ID:         call.id,
			Time:       call.start,
			FinishedAt: time.Now(),
			Failed:     failed,
			Category:   category,
			Agent:      agentName,
			Summary:    call.summary,
			Level:      level,
			Depth:      call.depth,
			Duration:   time.Since(call.start),
		}
		o.emitEv(finishEv)
		o.persistEvent(finishEv)
	}
	emitDispatchFinish := func(failed bool) {
		emitFinish(dispatchCall, "DISPATCH", dispatchTarget, failed)
	}
	emitToolFinish := func(failed bool) {
		emitFinish(toolCall, "TOOL", agent, failed)
	}
	// Fallback: If subagent finishes while there are still uncompleted TOOL calls inside that subagent (e.g. ensureSubagentToolStarted
	// emitted an in-progress event early, but subsequent abort/context cancel caused ProgressToolEnd not to arrive),
	// force finish here to prevent the TOOL line from staying in "in-progress" forever. Status is synchronized with dispatch.
	flushOrphanSubagentTool := func(failed bool) {
		if dispatchTarget == "" {
			return
		}
		call, ok := o.toolStarts[dispatchTarget]
		if !ok {
			return
		}
		delete(o.toolStarts, dispatchTarget)
		delete(o.streamExtractors, dispatchTarget)
		emitFinish(call, "TOOL", dispatchTarget, failed)
	}

	if ev.IsError {
		depth := 0
		if agent != "coordinator" {
			depth = 1
		}
		errText := ""
		if len(ev.Result) > 0 {
			errText = string(ev.Result)
		}
		// User-initiated abort derived ctx-cancel: state cleanup is still needed (dispatch / tool lines must fall back to completion state),
		// but skip the independent ERROR line + error log, consistent with the EventError path.
		if o.isCancellationNoise(nil, errText) {
			slog.Debug("suppressed cancel-derived tool error", "module", "agent", "tool", ev.Tool, "msg", errText)
			flushOrphanSubagentTool(true)
			emitDispatchFinish(true)
			emitToolFinish(true)
			return
		}
		summary := fmt.Sprintf("%s Failed", ev.Tool)
		detail := summary
		kind := ""
		if errText != "" {
			kind = errorKind(nil, errText)
			detail = fmt.Sprintf("%s → %s: %s", agent, ev.Tool, errText)
			summary += ": " + truncate(errText, 120)
		}
		flushOrphanSubagentTool(true)
		emitDispatchFinish(true)
		emitToolFinish(true)
		errEv := Event{
			Time:     time.Now(),
			Category: "ERROR",
			Agent:    agent,
			Summary:  summary,
			Detail:   detail,
			Kind:     kind,
			Level:    "error",
			Depth:    depth,
		}
		o.emitEv(errEv)
		o.persistEvent(errEv)
		return
	}

	if errEv, fullErr := o.subagentResultErrorEvent(ev); errEv != nil {
		if o.isCancellationNoise(nil, fullErr) {
			slog.Debug("suppressed cancel-derived subagent error", "module", "agent", "tool", ev.Tool, "msg", fullErr)
			flushOrphanSubagentTool(true)
			emitDispatchFinish(true)
			return
		}
		if dispatchTarget != "" && dispatchTarget != "subagent" {
			errEv.Agent = dispatchTarget
		}
		flushOrphanSubagentTool(true)
		emitDispatchFinish(true)
		o.emitEv(*errEv)
		o.persistEvent(*errEv)
		return
	}

	// subagent completed successfully -> update original DISPATCH row to completion state (with duration)
	if ev.Tool == "subagent" {
		flushOrphanSubagentTool(false)
		emitDispatchFinish(false)
		return
	}

	// coordinator direct tool completed successfully
	emitToolFinish(false)
}

func (o *observer) emitStreamDelta(delta string, thinking bool) {
	if delta == "" {
		return
	}
	if thinking != o.streamThinking {
		o.emitD(utils.ThinkingSep)
		o.streamThinking = thinking
	}
	o.emitD(delta)
	o.streamHasContent = true
	o.streamLastByte = delta[len(delta)-1]
}

// ensureSubagentToolStarted registers an in-progress TOOL call early for the agent when a tool_call is first identified in the stream,
// making the spinner in the event stream cover the time during which the LLM streams the tool_call parameters (usually accounting for 99% of total call duration).
// The args are incomplete at this stage, so the pure tool name is used as the temporary summary; it will be filled with parameters when the real ProgressToolStart arrives.
func (o *observer) ensureSubagentToolStarted(agent, tool string) {
	if agent == "" || tool == "" {
		return
	}
	if _, ok := o.toolStarts[agent]; ok {
		return // Already has in-progress call, idempotent
	}
	id := nextEventID()
	o.toolStarts[agent] = &activeCall{
		id:      id,
		start:   time.Now(),
		summary: tool, // Temporary use pure tool name, might be updated to tool(ChN) when ProgressToolStart arrives
		depth:   1,
	}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "TOOL",
		Agent:    agent,
		Summary:  tool,
		Level:    "info",
		Depth:    1,
	})
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = tool
	})
	o.emitFallbackStreamHeader(tool)
}

// emitFallbackStreamHeader appends a ✻ header line to the stream panel for tools without a configured extractor.
// Must be called by all three paths to guarantee consistency:
//  1. ensureSubagentToolStarted —— subagent streaming tool args (DeltaToolCall)
//  2. handleToolUpdate ProgressToolStart —— subagent non-streaming tool args
//  3. handleToolStart —— coordinator's own tool
//
// If any path is missing, the same tool might show ✻ when called by writer, but not when called by coordinator, or vice versa.
func (o *observer) emitFallbackStreamHeader(tool string) {
	if _, has := toolDisplays[tool]; has {
		return // Has extractor, header will be output by the extractor itself
	}
	o.streamClear()
	o.emitStreamDelta(streamHeaderFallback(tool)+"\n", false)
}

// streamHeaderFallback generates a streaming header text for tools without a configured extractor,
// allowing the user to see "what is being called" even for lightweight read-only tools.
//
// The prefix "✻ " is the agreed conventional "agent dispatch block" marker — when TUI's renderStreamContent detects this
// prefix, it goes through the renderAgentBlock path for rendering (icon + highlighted label + separator line);
// otherwise, it falls back to the body block path using the terminal default color, making the header look like ordinary text and not stand out.
func streamHeaderFallback(tool string) string {
	label := tool
	switch tool {
	case "ask_user":
		label = "Ask User"
	}
	return "✻ " + label
}

// streamClear notifies TUI to start a new streamRound, while resetting states related to paragraph separation.
// Logically, a new round is an "empty stream", otherwise the next first extractor emit would incorrectly add a leading empty line.
//
// streamThinking must be reset together: emitStreamDelta uses streamThinking to track across calls
// whether the previous segment was thinking. Since nothing has been output yet in the new round, the next emit(thinking=false)
// should not insert a ThinkingSep. Otherwise, the fallback header (like ✻ Read Chapter) will be occupied by \x02
// beforehand, causing renderStreamContent's HasPrefix("✻") to fail. The entire section would then fall to the body text path,
// split into a thinking segment by ThinkingSep, and the title color would be drawn in thinking color.
func (o *observer) streamClear() {
	o.emitC()
	o.streamHasContent = false
	o.streamLastByte = 0
	o.streamThinking = false
	// Before the last round of subagent ended, ProgressToolEnd had been deleted, defensively clear here.
	if len(o.streamExtractors) > 0 {
		o.streamExtractors = make(map[string]*agentExtractor)
	}
}

func (o *observer) subagentResultErrorEvent(ev agentcore.Event) (*Event, string) {
	if ev.Tool != "subagent" || len(ev.Result) == 0 {
		return nil, ""
	}
	sub := parseSubagentArgs(ev.Args)
	errMsg := parseSubagentResultError(ev.Result)
	if errMsg == "" {
		return nil, ""
	}

	target := "subagent"
	if sub.agent != "" {
		target = sub.agent
	}
	fullErr := fmt.Sprintf("%s Failed: %s", target, errMsg)
	return &Event{
		Time:     time.Now(),
		Category: "ERROR",
		Agent:    "coordinator",
		Summary:  fmt.Sprintf("%s Failed: %s", target, truncate(errMsg, 120)),
		Detail:   fullErr,
		Kind:     errorKind(nil, errMsg),
		Level:    "error",
	}, fullErr
}

func (o *observer) updateAgent(name string, fn func(*agentState)) {
	if name == "" {
		return
	}
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	a, ok := o.agents[name]
	if !ok {
		a = &agentState{name: name, state: "idle"}
		o.agents[name] = a
	}
	fn(a)
	a.updated = time.Now()
}

func (o *observer) agentSnapshots() []AgentSnapshot {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	snaps := make([]AgentSnapshot, 0, len(o.agents))
	for _, a := range o.agents {
		snaps = append(snaps, AgentSnapshot{
			Name:      a.name,
			State:     a.state,
			Summary:   a.summary,
			Tool:      a.tool,
			Turn:      a.turn,
			Context:   a.context,
			UpdatedAt: a.updated,
		})
	}
	return snaps
}

func agentFromEvent(ev agentcore.Event) string {
	if ev.Progress != nil && ev.Progress.Agent != "" {
		return ev.Progress.Agent
	}
	return "coordinator"
}

func displayToolName(tool string, args json.RawMessage) string {
	if len(args) == 0 {
		return tool
	}
	switch tool {
	case "save_foundation":
		var p struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(args, &p) == nil && p.Type != "" {
			return fmt.Sprintf("%s[%s]", tool, p.Type)
		}
	case "commit_chapter", "plan_chapter", "draft_chapter", "check_consistency":
		var p struct {
			Chapter int `json:"chapter"`
		}
		if json.Unmarshal(args, &p) == nil && p.Chapter > 0 {
			return fmt.Sprintf("%s(Ch%d)", tool, p.Chapter)
		}
	case "save_review":
		var p struct {
			Chapter int    `json:"chapter"`
			Scope   string `json:"scope"`
			Verdict string `json:"verdict"`
		}
		if json.Unmarshal(args, &p) == nil {
			label := ""
			switch p.Scope {
			case "arc":
				label = "Arc"
			case "global":
				label = "Global"
			default:
				if p.Chapter > 0 {
					label = fmt.Sprintf("Ch%d", p.Chapter)
				}
			}
			if label == "" {
				return tool
			}
			if p.Verdict != "" {
				return fmt.Sprintf("%s(%s·%s)", tool, label, p.Verdict)
			}
			return fmt.Sprintf("%s(%s)", tool, label)
		}
	case "novel_context":
		var p struct {
			Chapter int `json:"chapter"`
		}
		if json.Unmarshal(args, &p) == nil && p.Chapter > 0 {
			return fmt.Sprintf("%s(Ch%d)", tool, p.Chapter)
		}
	case "read_chapter":
		var p struct {
			Chapter   int    `json:"chapter"`
			Source    string `json:"source"`
			Character string `json:"character"`
		}
		if json.Unmarshal(args, &p) == nil && p.Chapter > 0 {
			suffix := ""
			if p.Character != "" {
				suffix = "·" + p.Character + " Dialogue"
			} else if p.Source == "draft" {
				suffix = "·Draft"
			}
			return fmt.Sprintf("%s(Ch%d%s)", tool, p.Chapter, suffix)
		}
	}
	return tool
}

type subagentInvocation struct {
	agent string
	task  string
}

func parseSubagentResultError(result json.RawMessage) string {
	if len(result) == 0 {
		return ""
	}
	// Mainstream error: {"error": "..."} object (unknown agent / invalid model / sub-agent execution failed)
	var obj struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &obj); err == nil && obj.Error != "" {
		return obj.Error
	}
	// Compatible with raw string error return of agentcore SubAgentTool:
	// "Invalid parameters: ..." / "background mode requires ..." / "Too many parallel tasks ..."
	// These are tool-layer parameter validation failures where is_error=false but content is error description; need to be recognized as errors to avoid misjudging as success.
	var s string
	if json.Unmarshal(result, &s) == nil && isSubagentErrorString(s) {
		return s
	}
	return ""
}

var subagentErrorPrefixes = []string{
	"Invalid parameters",
	"background mode requires",
	"Too many parallel tasks",
}

func isSubagentErrorString(s string) bool {
	for _, p := range subagentErrorPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func parseSubagentArgs(args json.RawMessage) subagentInvocation {
	if len(args) == 0 {
		return subagentInvocation{}
	}
	var p struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if json.Unmarshal(args, &p) == nil && p.Agent != "" {
		return subagentInvocation{agent: p.Agent, task: p.Task}
	}
	return subagentInvocation{}
}
