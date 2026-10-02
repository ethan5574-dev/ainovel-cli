import { useSyncExternalStore } from 'react'
import { useSSEStore } from './SSEProvider'

export function useConnectionStatus() {
  const store = useSSEStore()
  return useSyncExternalStore(store.status.subscribe, store.status.get)
}

export function useSnapshot() {
  const store = useSSEStore()
  return useSyncExternalStore(store.snapshot.subscribe, store.snapshot.get)
}

export function useEventLog() {
  const store = useSSEStore()
  return useSyncExternalStore(store.events.subscribe, store.events.get)
}

export function useStreamBuffer() {
  const store = useSSEStore()
  return useSyncExternalStore(store.streamBuffer.subscribe, store.streamBuffer.get)
}

export function useImportEvent() {
  const store = useSSEStore()
  return useSyncExternalStore(store.importEvent.subscribe, store.importEvent.get)
}
