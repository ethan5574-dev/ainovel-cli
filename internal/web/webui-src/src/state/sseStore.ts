// External store for the single /api/events SSE connection, consumed via useSyncExternalStore
// selector hooks (see sseHooks.ts) so a `delta` tick doesn't re-render panels that only care
// about `snapshot`. No Redux/Zustand — the data shape (one replace-on-push snapshot, one
// append/patch event log, one transient text buffer, import progress) fits a plain
// external store cleanly.
//
// Reconnection has two layers, matching internal/web/server.go's handleEvents:
//  - In-tab network blips: the browser's native EventSource retry automatically resends the
//    last SSE `id:` it saw as a `Last-Event-ID` request header — zero client code needed for
//    this case, the server just has to honor that header (it does).
//  - Full page reloads: EventSource can't set request headers on initial construction, so
//    Last-Event-ID can't survive a reload. We persist the last-seen id to sessionStorage
//    ourselves and pass it as `?after=` when constructing a fresh EventSource.
// Either path can occasionally re-deliver an event the client already rendered (e.g. a stale
// sessionStorage id after a long gap) — upsertEvent's dedupe key handles that for events with a
// stable ID (call-correlated DISPATCH/TOOL/DECISION rows); for ID-less rows (SYSTEM/ERROR/...)
// it falls back to a content key, since those are byte-identical on replay.
import type { HostEvent, ImportEvent, UISnapshot } from '../api/types'

export type ConnectionStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

const MAX_EVENTS = 500
const MAX_STREAM_CHARS = 120_000
const STREAM_TRIM_TO = 100_000
const LAST_EVENT_ID_KEY = 'ainovel:sse:lastEventId'

type Listener = () => void

function makeSlice<T>(initial: T) {
  let value = initial
  const listeners = new Set<Listener>()
  return {
    get: () => value,
    set(next: T) {
      value = next
      for (const l of listeners) l()
    },
    subscribe(l: Listener) {
      listeners.add(l)
      return () => listeners.delete(l)
    },
  }
}

function eventKey(ev: HostEvent): string {
  return ev.ID || `${ev.Category}|${ev.Time}|${ev.Summary}`
}

export function createSSEStore() {
  const status = makeSlice<ConnectionStatus>('connecting')
  const snapshot = makeSlice<UISnapshot | null>(null)
  const events = makeSlice<HostEvent[]>([])
  const streamBuffer = makeSlice<string>('')
  const importEvent = makeSlice<ImportEvent | null>(null)

  let source: EventSource | null = null
  const seenKeys = new Set<string>()

  function upsertEvent(list: HostEvent[], ev: HostEvent): HostEvent[] {
    const key = eventKey(ev)
    if (ev.ID) {
      const idx = list.findIndex((e) => e.ID === ev.ID)
      if (idx !== -1) {
        const next = list.slice()
        next[idx] = ev
        return next
      }
    } else if (seenKeys.has(key)) {
      return list // duplicate replay of a non-call event we've already rendered
    }
    seenKeys.add(key)
    const next = list.length >= MAX_EVENTS ? list.slice(list.length - MAX_EVENTS + 1) : list.slice()
    next.push(ev)
    return next
  }

  function connect() {
    if (source) return
    status.set('connecting')
    const after = sessionStorage.getItem(LAST_EVENT_ID_KEY)
    const url = after ? `/api/events?after=${encodeURIComponent(after)}` : '/api/events'
    source = new EventSource(url)

    source.onopen = () => status.set('open')
    source.onerror = () => {
      // readyState CLOSED means EventSource gave up for good (e.g. the server responded
      // 503 because setup isn't complete) — it will not retry on its own. CONNECTING means
      // it's mid-retry already.
      status.set(source?.readyState === EventSource.CLOSED ? 'closed' : 'reconnecting')
    }

    source.addEventListener('snapshot', (e) => {
      snapshot.set(JSON.parse((e as MessageEvent).data))
    })
    source.addEventListener('event', (e) => {
      const msg = e as MessageEvent
      if (msg.lastEventId) sessionStorage.setItem(LAST_EVENT_ID_KEY, msg.lastEventId)
      events.set(upsertEvent(events.get(), JSON.parse(msg.data)))
    })
    source.addEventListener('delta', (e) => {
      const chunk = JSON.parse((e as MessageEvent).data) as string
      let next = streamBuffer.get() + chunk
      if (next.length > MAX_STREAM_CHARS) next = next.slice(next.length - STREAM_TRIM_TO)
      streamBuffer.set(next)
    })
    source.addEventListener('clear', () => streamBuffer.set(''))
    source.addEventListener('import', (e) => {
      importEvent.set(JSON.parse((e as MessageEvent).data))
    })
  }

  function disconnect() {
    source?.close()
    source = null
  }

  return {
    status,
    snapshot,
    events,
    streamBuffer,
    importEvent,
    connect,
    disconnect,
  }
}

export type SSEStore = ReturnType<typeof createSSEStore>
