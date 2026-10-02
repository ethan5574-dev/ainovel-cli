// Wire types for the Go backend (internal/web, internal/host).
//
// Two JSON casing conventions coexist on purpose — don't "fix" one to match the other:
//  - Structs owned by internal/host (Event, UISnapshot, OutlineSnapshot, AgentSnapshot,
//    AgentCacheStat, AgentContextSnapshot, CoCreateReply) have NO `json` tags in Go, so they
//    marshal using the exact Go field name (PascalCase). Mirrored verbatim here.
//  - Request/response DTOs defined in internal/web/server.go (textRequest, reviewRequest,
//    setupCompleteRequest) and domain structs DO have explicit
//    `json` tags (lowerCamelCase). Mirrored per-tag here.
// Verify with a live `curl` against a running `ainovel-cli web` before trusting a shape you
// haven't seen on the wire yet — this file is derived from reading Go source, not from
// observed responses.

export interface OutlineSnapshot {
  Chapter: number
  Title: string
  CoreEvent: string
}

export interface AgentContextSnapshot {
  Tokens: number
  ContextWindow: number
  Percent: number
  Scope: string
  Strategy: string
  ActiveMessages: number
  SummaryMessages: number
  CompactedCount: number
  KeptCount: number
}

export interface AgentSnapshot {
  Name: string
  State: string
  TaskID: string
  TaskKind: string
  Summary: string
  Tool: string
  Turn: number
  Context: AgentContextSnapshot
  UpdatedAt: string // time.Time -> RFC3339
}

export interface AgentCacheStat {
  Role: string
  Model: string
  Input: number
  Output: number
  CacheRead: number
  CacheWrite: number
  Cost: number
  Saved: number
  CacheCapable: boolean
  RecentCacheRead: number
  RecentInput: number
  RecentSamples: number
}

// Mirrors internal/host/events.go UISnapshot exactly (field-for-field, same order).
export interface UISnapshot {
  Provider: string
  BookTitle: string
  ModelName: string
  ModelContextWindow: number
  ThinkingLevel: string
  Style: string
  RuntimeState: string // idle / running / pausing / paused / completed
  StatusLabel: string
  Phase: string
  Flow: string
  CurrentChapter: number
  TotalChapters: number
  CompletedCount: number
  TotalWordCount: number
  InProgressChapter: number
  PendingRewrites: number[]
  RewriteReason: string
  PendingSteer: string
  AdvanceMode: string
  AdvancePermitChapter: number
  HasAdvanceHold: boolean
  AdvanceHoldReason: string
  RecoveryLabel: string
  IsRunning: boolean
  Agents: AgentSnapshot[]

  TotalInputTokens: number
  TotalOutputTokens: number
  TotalCacheReadTokens: number
  TotalCacheWriteTokens: number
  TotalCostUSD: number
  TotalSavedUSD: number
  BudgetLimitUSD: number

  OverallCacheCapable: boolean
  OverallRecentCacheRead: number
  OverallRecentInput: number
  OverallRecentSamples: number
  TotalCacheBreaks: number
  MissingAssistantUsage: number

  CachePerAgent: AgentCacheStat[]
  CachePerModel: AgentCacheStat[]

  Synopsis: string
  Premise: string
  Outline: OutlineSnapshot[]
  Characters: string[]
  SupportingCount: number
  RecentSupporting: string[]
  Layered: boolean
  CurrentVolumeArc: string
  NextVolumeTitle: string
  CompassDirection: string
  CompassScale: string

  LastCommitSummary: string
  LastReviewSummary: string
  LastCheckpointName: string
  RecentSummaries: string[]
}

// Mirrors internal/host/events.go Event. Category: DISPATCH/TOOL/DECISION/SYSTEM/REVIEW/
// CHECK/ERROR/CONTEXT/USER/COMPACT. Level: info/warn/error/success.
// Running() semantics: ID !== "" && FinishedAt is zero -> in-place update to an existing
// row, not a new row (Go zero time.Time marshals as "0001-01-01T00:00:00Z").
export interface HostEvent {
  ID: string
  Time: string
  FinishedAt: string
  Failed: boolean
  Category: string
  Agent: string
  Summary: string
  Detail: string
  Kind: string
  Level: string
  Depth: number
  Duration: number // time.Duration -> nanoseconds
  RetryAt: string
}

export const ZERO_TIME = '0001-01-01T00:00:00Z'

// internal/bootstrap/setup.go ProviderPreset — no json tags, PascalCase.
export interface ProviderPreset {
  Name: string
  Label: string
  BaseURL: string
  NeedType: boolean
  APIKeyOptional: boolean
}

// internal/web/server.go setupProtocolType — has json tags.
export interface SetupProtocolType {
  name: string
  label: string
}

// internal/web/server.go setupCompleteRequest — has json tags.
export interface SetupCompleteRequest {
  provider: string
  type?: string
  apiKey?: string
  baseURL?: string
  model: string
}

export function isEventRunning(e: HostEvent): boolean {
  return e.ID !== '' && (e.FinishedAt === '' || e.FinishedAt === ZERO_TIME)
}

export interface CoCreateReply {
  Message: string
  Prompt: string
  Ready: boolean
  Suggestions: string[]
  Raw: string
}

export interface ModelConfig {
  name: string
  context_window?: number
  json_schema?: boolean
}

export interface ProviderSnapshot {
  Name: string
  Type: string
  API: string
  BaseURL: string
  Models: ModelConfig[]
  HasAPIKey: boolean
  APIKeyHint: string
  RequiresAPIKey: boolean
}

export interface ModelConfiguration {
  Providers: ProviderSnapshot[]
  DefaultProvider: string
  DefaultModel: string
  ConfigPath: string
  References: Record<string, string[]>
}

export interface ModelDraft {
  provider: string
  type?: string
  api?: string
  baseURL?: string
  models: ModelConfig[]
  apiKeyAction: 'keep' | 'replace' | 'clear'
  apiKey?: string
  testModel?: string
}

export interface ChapterListItem {
  chapter: number
  title: string
  coreEvent?: string
  summary?: string
  status: 'planned' | 'writing' | 'completed'
  wordCount: number
  volume?: number
  arc?: number
}

export interface BookSnapshot {
  name: string
  premise: string
  phase: string
  chapters: ChapterListItem[]
}

export interface ChapterDetail {
  chapter: number
  status: string
  content: string
  outline: { chapter: number; title: string; core_event: string; hook: string; scenes: string[] } | null
  summary: { chapter: number; title: string; summary: string; characters: string[]; key_events: string[] } | null
  plan: Record<string, unknown> | null
}

export interface CoCreateMessage {
  role: 'user' | 'assistant'
  content: string
}

export interface ImportEvent {
  stage: string
  current: number
  total: number
  message: string
  level: string
  key: string
  retryAt: string
  error: string
  continued: boolean
}

// SSE envelope: server sends `event: <name>\ndata: <json>\n\n`; the event name is not part
// of the JSON body (see writeSSE in server.go), so each SSE `data:` payload below is typed
// per event name, not wrapped in a shared envelope.
export type SSEMessage =
  | { event: 'snapshot'; data: UISnapshot }
  | { event: 'event'; data: HostEvent }
  | { event: 'delta'; data: string }
  | { event: 'clear'; data: boolean }
  | { event: 'import'; data: ImportEvent }
