import { useState } from 'react'
import * as api from '../api/client'
import { useConnectionStatus, useEventLog, useSnapshot, useStreamBuffer } from '../state/sseHooks'

// Phase 0/1 smoke-test shell: proves the SSE pipe + mutation endpoints work end-to-end.
// Replaced by the real StatusBar/OutlinePanel/Sidebar/ActivityLog/StreamingPanel components
// in Phase 1 (see internal/entry/tui/panels_*.go for what each one must reproduce).
export function Dashboard() {
  const status = useConnectionStatus()
  const snapshot = useSnapshot()
  const events = useEventLog()
  const stream = useStreamBuffer()
  const [prompt, setPrompt] = useState('')
  const [error, setError] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>) {
    setError(null)
    try {
      await action()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="placeholder-screen">
      <h1>ainovel-cli web (Phase 0 scaffold)</h1>
      <p>connection: {status}</p>
      {error && <p style={{ color: 'red' }}>{error}</p>}

      {!snapshot?.NovelName && (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void run(() => api.start(prompt))
          }}
        >
          <input
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Describe the novel to write"
            style={{ width: 320 }}
          />
          <button type="submit">Start</button>
        </form>
      )}

      {snapshot?.IsRunning && (
        <button type="button" onClick={() => void run(() => api.abort())}>
          Abort
        </button>
      )}

      <h2>Snapshot</h2>
      <pre>{JSON.stringify(snapshot, null, 2)}</pre>

      <h2>Stream buffer ({stream.length} chars)</h2>
      <pre>{stream.slice(-2000)}</pre>

      <h2>Events ({events.length})</h2>
      <ul>
        {events
          .slice(-20)
          .reverse()
          .map((ev, i) => (
            <li key={ev.ID || `${ev.Category}-${ev.Time}-${i}`}>
              [{ev.Category}] {ev.Summary}
            </li>
          ))}
      </ul>
    </div>
  )
}
