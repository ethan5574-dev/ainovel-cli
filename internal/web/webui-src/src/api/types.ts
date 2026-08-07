// Wire types for the Go backend (internal/web, internal/host).
//
// Two JSON casing conventions coexist on purpose — don't "fix" one to match the other:
//  - Structs owned by internal/host (Event, UISnapshot, OutlineSnapshot, AgentSnapshot,
//    AgentCacheStat, AgentContextSnapshot, CoCreateReply) have NO `json` tags in Go, so they
//    marshal using the exact Go field name (PascalCase). Mirrored verbatim here.
//  - Request/response DTOs defined in internal/web/server.go (textRequest, reviewRequest,
//    answerRequest, askPayload) and domain/tools structs (Question, Option) DO have explicit
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
  NovelName: string
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

  ContextTokens: number
  ContextWindow: number
  ContextPercent: number
  ContextScope: string
  ContextStrategy: string
  ContextActiveMessages: number
  ContextSummaryCount: number
  ContextCompactedCount: number
  ContextKeptCount: number

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

// internal/tools/ask_user.go — has explicit json tags.
export interface QuestionOption {
  label: string
  description: string
}

export interface Question {
  question: string
  header: string
  options: QuestionOption[]
  multiSelect: boolean
}

// internal/web/server.go askPayload — has explicit json tags.
export interface AskPayload {
  id: string
  questions: Question[]
}

// SSE envelope: server sends `event: <name>\ndata: <json>\n\n`; the event name is not part
// of the JSON body (see writeSSE in server.go), so each SSE `data:` payload below is typed
// per event name, not wrapped in a shared envelope.
export type SSEMessage =
  | { event: 'snapshot'; data: UISnapshot }
  | { event: 'event'; data: HostEvent }
  | { event: 'delta'; data: string }
  | { event: 'clear'; data: boolean }
  | { event: 'ask'; data: AskPayload }
