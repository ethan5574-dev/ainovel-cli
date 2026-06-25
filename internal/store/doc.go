// Package store provides file system-based persistent storage.
//
// Architecture: 1 IO base + multiple sub-stores + 1 composition root.
// Each sub-store holds an independent IO instance and an independent sync.RWMutex.
// Reads and writes for main domains (Progress, Outline, Drafts, Summaries, etc.) do not block each other;
// WorldStore combines several low-frequency small domains to share a single lock.
//
// The composition root Store holds references to all sub-stores and is responsible for cross-domain atomic operations
// (ExpandArc, AppendVolume, ClearHandledSteer).
//
// Sub-store divisions:
//   - ProgressStore: Main progress state (meta/progress.json)
//   - OutlineStore: Premise, outline (flat/layered), compass
//   - DraftStore: Chapter plan, drafts, final chapters
//   - SummaryStore: Chapter/Arc/Volume summaries
//   - RunMetaStore: Run metadata (model, steer history)
//   - SignalStore: One-off signal files (PendingCommit recovery)
//   - CheckpointStore: Step-level checkpoints (meta/checkpoints.jsonl)
//   - RuntimeStore: Runtime event queue (meta/runtime/*.jsonl)
//   - CharacterStore: Character profiles, state snapshots
//   - WorldStore: Timeline, foreshadow, relations, state changes, world rules, style rules, reviews
package store
