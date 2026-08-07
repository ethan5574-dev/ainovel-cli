// Typed REST client for the endpoints internal/web/server.go exposes today.
// New endpoints land here as later phases add them (see the plan's API surface table).
import type { UISnapshot } from './types'

export class ApiError extends Error {}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  const data = await res.json().catch(() => ({ error: res.statusText }))
  if (!res.ok) {
    throw new ApiError(data?.error || res.statusText)
  }
  return data as T
}

function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined })
}

export function getSnapshot(): Promise<UISnapshot> {
  return request('/api/snapshot')
}

export function getHealth(): Promise<{ ok: boolean; dir: string }> {
  return request('/api/health')
}

export function start(prompt: string): Promise<{ ok: boolean }> {
  return post('/api/start', { prompt })
}

export function resume(): Promise<{ ok: boolean; label?: string }> {
  return post('/api/resume')
}

export function continueIdle(text: string): Promise<{ ok: boolean }> {
  return post('/api/continue', { text })
}

export function steer(text: string): Promise<{ ok: boolean }> {
  return post('/api/steer', { text })
}

export function abort(): Promise<{ ok: boolean }> {
  return post('/api/abort')
}

export function advanceNext(): Promise<{ ok: boolean }> {
  return post('/api/next')
}

export function reopen(direction: string): Promise<{ ok: boolean }> {
  return post('/api/reopen', { direction })
}

export type AdvanceMode = 'auto' | 'review'

export function setReviewMode(mode: AdvanceMode): Promise<{ ok: boolean }> {
  return post('/api/review', { mode })
}

export function answerAsk(
  id: string,
  answers: Record<string, string>,
  notes: Record<string, string>,
): Promise<{ ok: boolean }> {
  return post(`/api/ask/${encodeURIComponent(id)}`, { answers, notes })
}

export function getSetupStatus(): Promise<{ needsSetup: boolean }> {
  return request('/api/setup/status')
}
