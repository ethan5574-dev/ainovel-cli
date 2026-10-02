package host

import "time"

// runObservedStep adds an observable lifecycle to one complete host-side LLM call.
// It reuses start/end updates under the existing event ID, without additional state,
// and keeps structured JSON out of the Worker live-output pane.
func runObservedStep[T any](o *observer, category, agent, label string, call func() (T, error)) (T, error) {
	if o == nil {
		return call()
	}
	started := time.Now()
	id := nextEventID()
	o.emitAndLog(Event{
		ID:       id,
		Time:     started,
		Category: category,
		Agent:    agent,
		Summary:  label,
		Level:    "info",
	})

	result, err := call()
	finished := time.Now()
	ev := Event{
		ID:         id,
		Time:       started,
		FinishedAt: finished,
		Failed:     err != nil,
		Category:   category,
		Agent:      agent,
		Summary:    label,
		Level:      "success",
		Duration:   finished.Sub(started),
	}
	if err != nil {
		ev.Level = "error"
		ev.Detail = err.Error()
		ev.Kind = errorKind(err, err.Error())
	}
	o.emitEv(ev)
	o.persistEvent(ev)
	return result, err
}

// runObservedDecision provides the fixed Arbiter decision shape; Arbiter remains a non-streaming LLM function.
func runObservedDecision[T any](o *observer, label string, call func() (T, error)) (T, error) {
	return runObservedStep(o, "DECISION", "arbiter", label, call)
}
