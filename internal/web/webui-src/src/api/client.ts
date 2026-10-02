// Typed REST client for the endpoints internal/web/server.go exposes today.
// New endpoints land here as later phases add them (see the plan's API surface table).
import type {
  BookSnapshot,
  ChapterDetail,
  CoCreateMessage,
  CoCreateReply,
  ModelConfiguration,
  ModelDraft,
  ProviderPreset,
  SetupCompleteRequest,
  SetupProtocolType,
  UISnapshot,
} from './types'

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

export function getSetupStatus(): Promise<{ needsSetup: boolean }> {
  return request('/api/setup/status')
}

export function getSetupProviders(): Promise<{ providers: ProviderPreset[]; types: SetupProtocolType[] }> {
  return request('/api/setup/providers')
}

export function completeSetup(req: SetupCompleteRequest): Promise<{ ok: boolean }> {
  return post('/api/setup/complete', req)
}

export function getBook(): Promise<BookSnapshot> {
  return request('/api/book')
}

export function getChapter(chapter: number): Promise<ChapterDetail> {
  return request(`/api/chapters/${chapter}`)
}

export function getModelConfiguration(): Promise<ModelConfiguration> {
  return request('/api/config')
}

export function configureProvider(draft: ModelDraft): Promise<ModelConfiguration> {
  return post('/api/config/provider', draft)
}

export function testProvider(draft: ModelDraft): Promise<{ ok: boolean }> {
  return post('/api/config/test', draft)
}

export function switchModel(provider: string, model: string, role = 'default'): Promise<ModelConfiguration> {
  return post('/api/config/model', { provider, model, role })
}

export async function importNovel(
  file: File,
  options: { guidance: string; storyResolution: string; continueAfter: boolean },
): Promise<{ ok: boolean }> {
  const form = new FormData()
  form.set('file', file)
  form.set('autoConfirm', 'true')
  form.set('guidance', options.guidance)
  form.set('storyResolution', options.storyResolution)
  form.set('continueAfter', String(options.continueAfter))
  const res = await fetch('/api/import', { method: 'POST', body: form })
  const data = await res.json().catch(() => ({ error: res.statusText }))
  if (!res.ok) throw new ApiError(data?.error || res.statusText)
  return data
}

export function resumeImport(options: {
  acceptSegmentation?: boolean
  storyResolution?: string
  continueAfter?: boolean
}): Promise<{ ok: boolean }> {
  return post('/api/import/resume', options)
}

export async function exportNovel(format: 'txt' | 'epub', from = 0, to = 0): Promise<void> {
  const res = await fetch('/api/export', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ format, from, to }),
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(data?.error || res.statusText)
  }
  const blob = await res.blob()
  const disposition = res.headers.get('Content-Disposition') || ''
  const filename = disposition.match(/filename="([^"]+)"/)?.[1] || `novel.${format}`
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

export function coCreateRound(history: CoCreateMessage[], stage: boolean): Promise<CoCreateReply> {
  return post('/api/cocreate/round', { history, stage })
}

export function pauseForCoCreate(): Promise<{ ok: boolean }> {
  return post('/api/cocreate/pause')
}

export function resumeFromCoCreate(draft: string): Promise<{ ok: boolean }> {
  return post('/api/cocreate/resume', { draft })
}

export function cancelCoCreate(): Promise<{ ok: boolean }> {
  return post('/api/cocreate/cancel')
}
