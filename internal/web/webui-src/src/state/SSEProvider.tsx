import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { createSSEStore, type SSEStore } from './sseStore'

const SSEStoreContext = createContext<SSEStore | null>(null)

// Provides the store *instance* via context (not the data), so re-renders triggered by SSE
// traffic never propagate through this Provider component itself — only through the
// useSyncExternalStore selector hooks below, each scoped to the slice it reads.
export function SSEProvider({ children }: { children: ReactNode }) {
  const store = useMemo(() => createSSEStore(), [])

  useEffect(() => {
    store.connect()
    return () => store.disconnect()
  }, [store])

  return <SSEStoreContext.Provider value={store}>{children}</SSEStoreContext.Provider>
}

export function useSSEStore(): SSEStore {
  const store = useContext(SSEStoreContext)
  if (!store) throw new Error('useSSEStore must be used within an SSEProvider')
  return store
}
