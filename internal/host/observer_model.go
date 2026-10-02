package host

import (
	"time"

	"github.com/voocel/agentcore"
)

// handleWorkerEvent projects model responses using actual AgentLoop boundaries:
// TurnStart precedes the request; assistant MessageEnd precedes tool execution.
func (o *observer) handleWorkerEvent(agent string, ev agentcore.Event) {
	switch ev.Type {
	case agentcore.EventTurnStart:
		o.startModelResponse(agent)
	case agentcore.EventMessageEnd:
		if ev.Message != nil && ev.Message.GetRole() == agentcore.RoleAssistant {
			o.finishModelResponse(agent)
		}
	}
}

func (o *observer) startModelResponse(agent string) {
	if agent == "" {
		return
	}
	now := time.Now()
	call := &activeCall{id: nextEventID(), start: now, summary: "Waiting for model", depth: 1}
	o.modelStarts[agent] = call
	o.emitAndLog(Event{
		ID:       call.id,
		Time:     now,
		Category: "MODEL",
		Agent:    agent,
		Summary:  call.summary,
		Level:    "info",
		Depth:    call.depth,
	})
}

func (o *observer) updateModelState(agent, summary string) {
	call := o.modelStarts[agent]
	if call == nil || summary == "" || call.summary == summary {
		return
	}
	call.summary = summary
	o.emitEv(Event{
		ID:       call.id,
		Time:     call.start,
		Category: "MODEL",
		Agent:    agent,
		Summary:  summary,
		Level:    "info",
		Depth:    call.depth,
	})
}

func (o *observer) finishModelResponse(agent string) {
	call := o.modelStarts[agent]
	if call == nil {
		return
	}
	delete(o.modelStarts, agent)
	call.summary = "Model response"
	// MODEL is a live observation event: log and deliver to UI, without entering the runtime queue.
	o.emitEv(Event{
		ID:         call.id,
		Time:       call.start,
		FinishedAt: time.Now(),
		Category:   "MODEL",
		Agent:      agent,
		Summary:    call.summary,
		Level:      "success",
		Depth:      call.depth,
		Duration:   time.Since(call.start),
	})
}
