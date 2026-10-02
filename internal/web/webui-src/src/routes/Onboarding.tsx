import { useEffect, useState } from 'react'
import * as api from '../api/client'
import type { ProviderPreset, SetupProtocolType } from '../api/types'

// Mirrors bootstrap.RunSetup()'s TUI wizard (internal/bootstrap/setup.go): same four fields
// (provider, API key, base URL, model), same POST /api/setup/complete backing it. On success,
// the parent (App.tsx) re-polls /api/setup/status and swaps to the dashboard with no reload —
// the runtime is already attached server-side by the time this resolves.
export function Onboarding({ onComplete }: { onComplete: () => void }) {
  const [providers, setProviders] = useState<ProviderPreset[] | null>(null)
  const [types, setTypes] = useState<SetupProtocolType[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)

  const [selected, setSelected] = useState('')
  const [customName, setCustomName] = useState('')
  const [protocolType, setProtocolType] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [model, setModel] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  useEffect(() => {
    api
      .getSetupProviders()
      .then((res) => {
        setProviders(res.providers)
        setTypes(res.types)
        if (res.providers.length > 0) setSelected(res.providers[0].Name)
      })
      .catch((err) => setLoadError(err instanceof Error ? err.message : String(err)))
  }, [])

  const preset = providers?.find((p) => p.Name === selected)
  const isCustom = preset?.NeedType ?? false

  useEffect(() => {
    if (preset) setBaseURL(preset.BaseURL)
  }, [preset])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setSubmitError(null)
    setSubmitting(true)
    try {
      await api.completeSetup({
        provider: isCustom ? customName.trim() : selected,
        type: isCustom ? protocolType : undefined,
        apiKey: apiKey.trim(),
        baseURL: baseURL.trim(),
        model: model.trim(),
      })
      onComplete()
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : String(err))
      setSubmitting(false)
    }
  }

  if (loadError) {
    return (
      <div className="placeholder-screen">
        <h1>Setup required</h1>
        <p style={{ color: 'red' }}>Failed to load provider list: {loadError}</p>
      </div>
    )
  }
  if (!providers) {
    return (
      <div className="placeholder-screen">
        <h1>Setup required</h1>
        <p>Loading providers…</p>
      </div>
    )
  }

  const apiKeyOptional = isCustom || (preset?.APIKeyOptional ?? false)

  return (
    <div className="placeholder-screen">
      <h1>Welcome to ainovel-cli</h1>
      <p>No configuration found yet. Choose a provider and model to get started.</p>

      <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 12, maxWidth: 420 }}>
        <label>
          Provider
          <select value={selected} onChange={(e) => setSelected(e.target.value)} style={{ display: 'block', width: '100%' }}>
            {providers.map((p) => (
              <option key={p.Name} value={p.Name}>
                {p.Label}
              </option>
            ))}
          </select>
        </label>

        {isCustom && (
          <>
            <label>
              Provider name
              <input
                value={customName}
                onChange={(e) => setCustomName(e.target.value)}
                placeholder="my-proxy"
                required
                style={{ display: 'block', width: '100%' }}
              />
            </label>
            <label>
              API protocol type
              <select value={protocolType} onChange={(e) => setProtocolType(e.target.value)} required style={{ display: 'block', width: '100%' }}>
                <option value="" disabled>
                  Select a protocol…
                </option>
                {types.map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.label}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}

        <label>
          API Key {apiKeyOptional && '(optional)'}
          <input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="sk-xxx"
            required={!apiKeyOptional}
            style={{ display: 'block', width: '100%' }}
          />
        </label>

        <label>
          Base URL {!isCustom && '(leave blank for the official endpoint)'}
          <input value={baseURL} onChange={(e) => setBaseURL(e.target.value)} style={{ display: 'block', width: '100%' }} />
        </label>

        <label>
          Model name
          <input
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="e.g. gpt-4o / claude-sonnet-4 / gemini-2.5-pro"
            required
            style={{ display: 'block', width: '100%' }}
          />
        </label>

        {submitError && <p style={{ color: 'red' }}>{submitError}</p>}

        <button type="submit" disabled={submitting}>
          {submitting ? 'Saving…' : 'Save and continue'}
        </button>
      </form>
    </div>
  )
}
