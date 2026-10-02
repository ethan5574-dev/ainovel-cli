import { useEffect, useState } from 'react'
import { getSetupStatus } from './api/client'
import { Dashboard } from './routes/Dashboard'
import { Onboarding } from './routes/Onboarding'
import { SSEProvider } from './state/SSEProvider'

type SetupState = 'checking' | 'needed' | 'ready'

// Root gate: decide onboarding vs. dashboard from GET /api/setup/status before mounting the
// SSEProvider (connecting /api/events against an unattached runtime would just 503-loop).
function App() {
  const [setup, setSetup] = useState<SetupState>('checking')

  useEffect(() => {
    let cancelled = false
    getSetupStatus()
      .then((res) => !cancelled && setSetup(res.needsSetup ? 'needed' : 'ready'))
      .catch(() => !cancelled && setSetup('ready')) // fail open: older backends without this endpoint
    return () => {
      cancelled = true
    }
  }, [])

  if (setup === 'checking') return null
  if (setup === 'needed') return <Onboarding onComplete={() => setSetup('ready')} />

  return (
    <SSEProvider>
      <Dashboard />
    </SSEProvider>
  )
}

export default App
