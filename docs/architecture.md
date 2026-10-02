 

# ainovel-cli Runtime Architecture

> Let the LLM finish a novel in a single run. The Host only handles startup, recovery, routing, and observation, keeping decision-making as much as possible with the model.

---

## 1. Goals (in priority order)

1. **Stability**: one-line input should reliably produce a full novel (200-500 chapters) without the architecture interrupting itself.
2. **Iteratable quality**: prompts, reference materials, review dimensions, and context strategy can all be adjusted independently without dragging the architecture along.
3. **Recoverability**: after a crash, disconnect, or pause, the run can continue from the latest checkpoint.
4. **Observability**: chapter-level progress, artifacts, and timing should be inspectable.

"Stability" is the prerequisite and "quality" is the layer above it. Every architectural decision should first serve stability.

---

## 2. Core Principles

### 2.1 LLM-driven Creation and Adjudication, Host-driven Process Routing

Vertical agents have a closed decision space: fixed flowcharts, limited branches, and fact-driven. The two types of decisions run on different carriers:

- **Creation and Adjudication** (semantics/quality/intent understanding) → LLM. The adjudication capabilities of the Writer/Editor/Architect/Coordinator benefit linearly with model upgrades.
- **Process Routing** (reading facts and looking up tables) → Code. The `flow.Router` is a pure function + unit test, with an error rate approaching 0.

The Host does not directly call SubAgents; instead, after each Coordinator TurnEnd, the Flow Router computes instructions and issues them via `coordinator.FollowUp("[Host issued command]…")`.

### 2.2 Tools are the Only Interface to the Fact Layer

All interactions with the file system, Progress, and Checkpoints are completed by tools. **Write-type tools must be atomic triple-actions**: write artifact to disk + advance Progress + append Checkpoint, all executed within a mutual exclusion lock. Running the same tool again yields the same result or skips directly (digest idempotency).

### 2.3 The Observation Layer Only Observes

UI, diagnostics, and event logs are passive consumers projected from the event stream / read-only artifacts. They read facts, do not produce facts, and do not affect the control flow.

Observation has three distinct layers: `agentcore.ProgressPayload` transports complete error text without UI truncation; `host.Event.Summary` is a short display description and `Detail` carries full diagnostics; file logs prefer full `Detail`, while TUI reads `Summary` and truncates only during final rendering. `Host` owns the file logger: acquire the novel-directory lease, open the log session, then assemble Store, models, and Engine. This preserves single-book exclusivity and captures assembly and shutdown. Complete logs preserve error chains, raw invalid arguments, and lifecycle metadata; they do not duplicate successfully generated prose in `tui.log`. Large content remains in Store artifacts and `meta/sessions`.

**`internal/diag` is the engine's sole observability subsystem**: first-class support infrastructure, not the product core. It reads artifacts, sessions, logs, and checkpoints for two purposes: **writing-quality diagnosis** (rules produce Findings displayed by `/diag`) and **runtime troubleshooting with redacted export** (behavioral skeletons without prose plus loop aggregation, overwriting `meta/diag-export.md`).

**Observer Discipline (unshakeable)**: `diag` can diagnose and suggest, but **never acts on its own**—it does not automatically fix, resume, or modify flows. The stronger it gets, the more people want it to "fix things along the way," but it must hold this line; otherwise, it will fall back into the deleted pitfalls of `idleResume`/`StallDetector` (see §10.5, §10.14). External structures (such as `RuntimeCapture`) are maintained as infrastructure contracts and should not be modified arbitrarily.

### 2.4 Flat Fact Layer

There are only three types of facts:

- **Progress** — progress index (what chapter is being written, pending rewrite list).
- **Checkpoint** — step-level progression record (plan / draft / commit / review / arc_summary).
- **Artifact** — chapter body, outline, characters, summaries, and other outputs.

There are no WorkflowInstance, TaskInstance, or Command abstractions. Persisted auxiliary facts (outline feedback and decision audits) are flat JSONL with explicit producers and consumers. Views reconstructible from chapter records are not persisted redundantly.

### 2.5 Three Iron Rules

**Iron Rule 1: Tools return only facts, not cross-scheduling commands.** `commit_chapter` returns structured fields like `arc_end_reached` / `next_skeleton_arc`; it does not carry system-level command strings like `[System]`. The `next_step` field inside sub-agents is an inline guide for factual statements ("I just saved the plan, the next step is draft"), which is not considered a violation—see §6.4.

**Iron Rule 2: Process routing is handled by the Flow Router.** The `Route(state) → *Instruction` in `internal/host/flow/router.go` is a pure function. It subscribes to `EventToolExecEnd` and issues commands via `FollowUp`. Returning `nil` means "adjudication scenario, let the LLM decide independently." **The instruction channel does not remain silent**: when `Route` consecutively computes the same instruction (indicating the state has not advanced since the last dispatch), the Dispatcher resends it with the fact "N-th time issued" instead of silently swallowing it—"routing result duplication" is a fact that only the Host can observe, and silence would trap the Coordinator in the double contradiction of "no action without command / StopGuard does not allow stopping." No threshold is set, no circuit breaker is triggered; how to break the loop is adjudicated by the LLM.

**Iron Rule 3: The Coordinator cannot physically `end_turn` unless Phase=Complete.** The StopGuard intercepts `end_turn` at the agentcore layer and injects a user message; consecutive 5 interceptions trigger termination. The three sub-agents (architect / writer / editor) have their own `CheckpointDeltaGuard`s.

---

## 3. Architectural Panorama

```
[Entry: TUI / headless]
        │ prompt / steer
[Host (thin shell)]
    ├── observer        Events → UI/Log Projection
    ├── flow.Dispatcher Subscribes ToolExecEnd → Route(state) → FollowUp
    └── usage / Model Management
         │
[Coordinator (LLM, MaxTurns=100_000)]
    ├── Adjudicates architect_short / long at startup
    ├── Receives [Host issued command] → generates subagent tool_call
    └── Receives [User intervention] → adjudicates independently
         │
[architect / writer / editor SubAgent (independent run + context + model)]
         │ Tool calls
[Tools]  novel_context · read_chapter · plan_chapter · draft_chapter · edit_chapter
         check_consistency · commit_chapter · save_review · save_arc_summary
         save_volume_summary · save_foundation
         │ Atomic triple-actions
[Store: File System (tmp + rename)]
    Progress · Checkpoints · Outline · Drafts · Summaries · Characters · World · Signals
```

| Layer       | What it does                                                               | What it does not do                                            |
| ----------- | -------------------------------------------------------------------------- | -------------------------------------------------------------- |
| Entry       | Displays UI, receives input                                                | Business decisions                                             |
| Host        | Startup/recovery/intervention/event projection/Flow routing                | Bypasses Coordinator to call SubAgents; writes state           |
| Coordinator | Executes Host commands, adjudicates user Steer, selects planner at startup | Decides the next step of each chapter on its own; writes files |
| Agents      | Thinks, writes, reviews                                                    | Directly reads/writes Store                                    |
| Tools       | Atomic IO + checkpoint + idempotency                                       | Dispatches cross-subagent commands                             |
| Store       | File system persistence                                                    | Business logic                                                 |

Unidirectional dependency: `entry → host → agents → tools → store → domain`. `tools/` does not reference `agents/host/`, and `host/` does not directly reference `tools/store/`. Horizontal independent modules: `errs/` can be referenced by any layer, and `diag/` subscribes to the host event stream + read-only `store/`.

---

## 4. Data Model

### 4.1 BookMetadata and Progress

`BookMetadata` is the sole source of truth for the title and reader-facing synopsis, saved in `meta/book.json`; `book.md` is a readable projection. Premise does not duplicate the title, and Progress does not hold book metadata.

```go
type BookMetadata struct {
    Title    string
    Synopsis string
}
```

Progress (`internal/domain/runtime.go`) records runtime state only:

```go
type Progress struct {
    Phase             Phase           // init / premise / outline / writing / complete
    CurrentChapter    int
    TotalChapters     int
    CompletedChapters []int
    TotalWordCount    int
    ChapterWordCounts map[int]int
    InProgressChapter int             // chapter currently being written
    Flow              FlowState       // writing / reviewing / rewriting / polishing / steering
    PendingRewrites   []int
    StrandHistory     []string        // dominant_strand sequence
    HookHistory       []string        // hook_type sequence
    CurrentVolume, CurrentArc int     // longform layering
    Layered           bool
}
```

Control logic only reads the above fact fields and does not rely on any "update timestamps"—time information is carried by the checkpoint's `OccurredAt`.

### 4.2 Checkpoint (`internal/domain/checkpoint.go`)

```go
type Scope      struct { Kind ScopeKind; Chapter, Volume, Arc int }
type Checkpoint struct {
    Seq        int64       // monotonically increasing
    Scope      Scope       // chapter / arc / volume / global
    Step       string      // plan / draft / commit / review / arc_summary / ...
    Artifact   string
    Digest     string
    OccurredAt time.Time
}
```

Storage: `meta/checkpoints.jsonl`, append-only. Writing the same `Scope+Step+Digest` repeatedly is treated as idempotent and does not produce new lines.

### 4.3 Artifacts and Signals

Artifacts are located in `store/outline.go`, `drafts.go`, `summaries.go`, `characters.go`, `world.go` — each product can be referenced by checkpoints.

- **Signals**: `PendingCommit` for interrupted-commit recovery, read during startup/recovery rather than the ordinary runtime loop.
- **Decisions** (`meta/decisions.jsonl`): auditable Arbiter facts, inputs, and decisions for offline replay. **Not a recovery source**: recovery relies on Progress, Checkpoint, and RunMeta.
- **Append-only world facts**: timeline and character-state changes append to `timeline.jsonl` and `meta/state_changes.jsonl`. In-process deduplication indexes allow normal commits to append only the current chapter's changes. Legacy JSON arrays migrate on the next append through an idempotent protocol: atomically write the new log before deleting the old file. `timeline.md` is a reconstructible readable projection.
- **Outline feedback** (`meta/outline_feedback.jsonl`): ordinary writer feedback is consumed at the next structural operation. External prose revisions affecting the plot go to the architect before further writing and are cleared after processing.
- **Mechanical violation view**: `novel_context(chapter=N)` computes violations from accepted prose and current `user_rules` for the editor; no separate artifact can drift out of sync with prose or rules.

### 4.4 Layered Outlines and Completion Convergence (Finale Volume)

Rolling planning (compass anchors, volume skeletons, and on-demand arc expansion) handles startup and progression, but turns completion from a fixed chapter count into an open decision at each volume end. Explicit convergence prevents two deadlocks: the outline is exhausted but the story cannot end (out-of-range continuation, now structurally guarded), or the story is finished but bookkeeping refuses completion (overestimated scale and hard completion vetoes cause filler or termination).

**The finale volume is a first-class convergence concept**: completion combines one direction decision with deterministic execution:

- **Declaration (LLM semantic decision)**: at a volume end, the architect chooses append_volume (continue), append_volume with `"final": true` (finale), or complete_book (all conditions already satisfied). estimated_scale is **evidence, not a veto**.
- **Execution (code-driven facts)**: finale status comes from `domain.FinaleVolume`. Once the final volume is structurally complete (`layeredStructurallyComplete`) **and arc review, arc summary, and volume summary are all present**, MarkComplete runs automatically, after the editor quality gate. Undeclared books retain the quality-level `layeredBookComplete` checks (no unresolved foreshadowing or open threads).
- **Revocation (derived from data, no undo tool)**: appending a new unmarked volume after declaration clears finale status naturally. State remains derivable from layered_outline.
- **Completion dispatch**: the volume-end Route branch sends architect_long through the completion checklist. The architect Worker, not the control plane, owns the completion decision.

---

## 5. Tool Specifications

Tools are the sole interaction point between the fact layer and the Agents.

### 5.1 Read Tools

`novel_context` / `read_chapter` provide context without a prerequisite state, sufficient for independent model decisions. `novel_context(chapter=N)` also injects mechanical violations, if any. The architect path provides completed-volume and current-arc summaries, character snapshots, outline feedback, and foundation status. Long-form planning overviews include chapter details only for the current arc; other arcs retain skeletons. Explicit `novel_context(volume=V, arc=A)` requests return details for expanded arcs and goals for skeleton arcs. Established events are facts; skeletons are plans. `expand_next_arc` revises the next skeleton's title/goal and expands its chapters; code determines the target.

### 5.2 Write Tools (Atomic Triple-Actions)

Single-file writes are atomic; cross-file steps do not promise database transactions. Normal and rework `commit_chapter` operations share `PendingCommit` and advance through complete intent, artifacts/state, Progress, checkpoint, then intent removal. Recovery uses only the originally persisted normalized payload and prose snapshot, never new model arguments or an overwritten draft. Structural operations such as `expand_next_arc` and `append_volume` have no persistent intent; they promise idempotent replay with identical arguments, derived-view repair, and explicit errors.

| Tool | Artifact | Step |
|---|---|---|
| `save_book` | meta/book.json + book.md | book |
| `plan_chapter` | drafts/chXX.plan.json | plan |
| `draft_chapter` | drafts/chXX.draft.md | draft |
| `edit_chapter` | drafts/chXX.draft.md | edit |
| `check_consistency` | None (read-only, returned inline) | consistency_check |
| `commit_chapter` | chapters/chXX.md + Progress | commit |
| `save_review` | reviews/chXX.json (global is chXX-global.json) | review |
| `save_arc_summary` | summaries/arc-vNNaNN.json | arc_summary |
| `save_volume_summary` | summaries/vol-vNN.json | volume_summary |
| `save_foundation` | foundation/*.json (successful append_volume/update_compass consumes feedback) | premise / outline / layered_outline / characters / world_rules / append_volume / update_compass / complete_book |
| `expand_next_arc` | layered_outline.json (success consumes feedback) | Code locates the next skeleton arc; the model supplies title / goal / chapters only |

`commit_chapter` handles arc/volume/book completion checks, returning 19 fact fields (`arc_end` / `needs_expansion` / `book_complete` etc.; plus `rule_violations` if mechanical rule check is enabled). `save_review` handles verdict upgrades (scorecard gates, contract missed → rewrite). These policies, previously scattered in the policy layer, are now codified inside the tools.

`edit_chapter` is a thin wrapper around `agentcore.EditTool`, restricted to completed chapters in `PendingRewrites`. Initial drafts of new chapters must be replaced in full through `draft_chapter(mode="write")`.

### 5.3 Error Layering

| Error Type                                            | Handling Layer                                     | Action                                                     |
| ----------------------------------------------------- | -------------------------------------------------- | ---------------------------------------------------------- |
| Network Timeout / Stream EOF                          | Tools                                              | Retry 3 times                                              |
| Provider 429/503                                      | litellm                                            | Failover to backup provider                                |
| Auth / Model Not Found                                | Tools                                              | Throw to terminal                                          |
| Missing Pre-requisite Artifact                        | Tools                                              | Throw conflict, LLM retries after calling`novel_context` |
| Invalid Tool Arguments                                | Tools                                              | Throw validation, LLM corrects parameters                  |
| MaxTurns Exhausted                                    | agentcore                                          | End of run, Host sends done                                |
| Non-compliant LLM Messages (thinking-only stop, etc.) | agentcore (`llm/litellm.go` `convertMessages`) | In-queue fallback + out-queue filtering; Host is unaware   |
| Stream Idle Response / Long Thinking                  | litellm (`StreamIdleTimeout=5min`)               | Watchdog triggers retry                                    |

### 5.4 Idempotency

Before executing each write-type tool, check the checkpoint: if the latest checkpoint of the current scope matches this `Step+Digest`, return the existing product directly. The LLM can safely retry without producing duplicate chapters or misplaced progress.

---

## 6. Agent Assembly

> Running an entire book on a single giant prompt + single Agent is theoretically possible, but three issues block stability: **context explosion** (reverts even with aggressive compression over 200 chapters), **role interference** (rigorous planning / imaginative writing / critical review dilute each other in the same prompt), and **loss of heterogeneous model benefits** (Opus for planning, Sonnet for writing, Pro for editing; independent model selection saves an order of magnitude of costs on long-form novels). A multi-agent topology is therefore necessary.

### 6.1 Coordinator

The sole driver of the main loop. Assembled in `internal/agents/build.go`:

Worker progress projects two kinds of facts: raw `AgentLoop` events define model-response lifecycles, while `ToolProgress` relays tool execution, streamed prose, thinking, retries, and context updates. Generating tool arguments belongs to MODEL; TOOL begins only at `ToolExecStart`.

```
Engine ── Runner.Run(agent, task) ──▶ architect_short/long · writer · editor
                                          │ Tool calls
                                        Store (shared artifacts; Workers do not communicate directly)
```

Responsibilities: Select planner at startup → plan completion loop → receive `[Host issued command]` and immediately generate corresponding `subagent` tool_call → handle `[User intervention]` and adjudicate independently → output summary after `book_complete=true`.

Does not: write files, read Progress directly (uses `novel_context`), or decide the next step on its own when Host instructions arrive.

> **Why not delete Coordinator and have Host call sub-agents directly?** It seems "cleaner," but it would lose four things: (1) the decision of "what to do next" remains in the LLM layer, benefiting directly from model upgrades; (2) the soft judgment of review verdicts (accept/polish/rewrite + scope of impact) is moved out of the Go code; (3) impact assessment of user Steer is handed to the model—which chapters should be rewritten for "supporting character motivation needs to be clearer," Coordinator can judge, while Host hard-coding cannot; (4) exception branches (writer outline feedback, editor discovering worldview leaks) are handled by the model itself, avoiding writing Go state machines for every branch. **Deleting Coordinator is equivalent to placing bets on "my Go code is getting stronger" instead of "the models are getting stronger"—that is not a good bet.**

### 6.2 Sub-agent Topology and Model Heterogeneity

```
Coordinator (1 agent run, MaxTurns=100_000)
    ↓ subagent()
architect_short/long  ·  writer  ·  editor
    ↓ Tool calls
Store (Collaboration medium; sub-agents do not communicate directly)
```

Sub-agent turns are counted independently (native to agentcore) and do not consume the 100,000 turn quota of the Coordinator. Sub-agents communicate through structured artifacts in the Store; the Coordinator only passes "task descriptions" and does not move content.

`bootstrap.ModelSet` supports role-level models: coordinator/architect/writer/editor are configured independently + provider failover. Running Sonnet instead of Opus for the Writer saves an order of magnitude of costs on a 200-chapter novel.

### 6.3 Three Collaboration Modes

Sub-agents do not communicate directly; all information flows through structured artifacts in the Store. Three modes cover all workflows of the system:

**Mode A · Serial Handover (Main branch)**: Coordinator → Architect planning → Writer chapters 1..N → Editor arc-end review → Writer rewrite. The most common mode; the Coordinator checks the current state via `novel_context` to decide who to call next.

**Mode B · Review Feedback (Closed loop)**: Writer detects outline drift in draft → `commit_chapter` return value carries `writer_feedback` field → Coordinator sees the feedback and decides whether to escalate to an Architect call to adjust the outline. The Writer does not call the Architect directly; feedback is sent back to the Coordinator via structured fields.

**Mode C · Skeleton Expansion (Rolling Planning)**: `commit_chapter` detects the next arc is still a skeleton → returns `arc_end_reached + next_skeleton_arc` → Flow Router dispatches instruction → Coordinator calls `architect_long` to expand detailed chapters of the next arc → Writer continues. The long-form "rolling planning" capability is achieved through this closed loop.

### 6.4 Code Constraints on Sub-agent Flow (No Prompt Crutches)

> Early writer flows relied on the constraint of "strictly proceed in the following order" in `writer.md`. The LLM frequently violated this—skipping plan to draft directly, saying more token-consuming sentences after committing, and writing the body text only in chat instead of saving to disk. **Prompt-based process constraints are unstable**; they depend entirely on the model's current "obedience," and model upgrades might even make it "creatively non-compliant."

Four layers of code constraints (acting simultaneously):

| Layer | Implementation | Function |
|---|---|---|
| `StopAfterTools` / `StopAfterToolResult` | `agents/build.go` SubAgentConfig | Successful key tools end the Worker run; terminal exits still consult StopGuard (see contract tests). Writer stops after `commit_chapter`; editor reviews/summaries and architect arc/volume finishing use `StopAfterToolResult` |
| `CheckpointDeltaGuard` | `agents/guard/subagent_guards.go` | Require a new checkpoint for the relevant step relative to the baseline before `end_turn`; three consecutive blocks escalate to termination. Editor guards are task-aware: a review alone does not satisfy a summary task |
| Inline tool `next_step` | Tool result fields | Facts carry next-step suggestions for the model |
| Tool ownership/precondition checks | `edit_chapter`, `commit_chapter`, etc. | Reject targeted edits to initial drafts, edits to completed chapters not queued for rework, and empty commits; `ConcurrencySafe=false` prevents concurrent races |

In the new architecture, `writer.md` only carries: writing quality guidelines, crash-resume cognitive model, and chapter contract interpretation. **It no longer performs process orchestration** — when the LLM skips steps, the code will save the day, not the prompt. The same four-layer constraints for the architect and editor are in their respective tools/Guards.

> Regarding Iron Rule 1: `next_step` is a factual statement inline to the tool ("I just saved the plan"), not process orchestration injected by the Host across calls. Cross-subagent scheduling at the Coordinator layer still strictly goes through Flow Router → FollowUp.

### 6.5 agentcore Dependencies

`../agentcore` is the project's own general-purpose Agent library (linked via `go.work`). All primitives used in the new architecture already exist: `Prompt` / `Inject` / `FollowUp` / `Subscribe` / `WithMaxTurns` / `WithStopGuard` / `WithToolGate` / `SubAgentConfig` / `WithContextManager`.

**Contract tests** (`internal/agents/agentcore_contract_test.go`, driven through `Runner.Run`) make framework assumptions executable: terminal exits consult StopGuard; Error/Aborted paths do not; Escalate chains support `errors.Is`; `Run` returns typed `ErrUnknownAgent`; tool-error progress is complete plain text. **All must pass before upgrading agentcore**. Comments can become stale; these tests have already caught an invalid assumption and avoided a workaround.

- Allowed in agentcore: new ContextManager strategies, new provider adaptations, new event types, general message injection modes.
- Forbidden in agentcore: business models like Progress/Checkpoint/Scope, business tools like `novel_context`/`commit_chapter`, and business rules like arc-end detection/review gates.

Decision rule: If agentcore is imported by a coding agent or a customer service agent in the future, the new capability must still make sense in that scenario to be allowed in. **Application-layer fallback patches (proxies, wrappers, monkey patches) are strictly prohibited** — modify agentcore directly if a capability is missing.

**Deliberately Unused Capabilities** (to prevent misuse):

- `Agent.TaskRuntime() / Tasks() / StopTask()` — agentcore's built-in background task manager (fire-and-forget background subagent). All sub-agent calls in the new architecture are foreground and synchronous, and **not used**.
- `Agent.FollowUp(msg)` — **the only legal user is `flow.Dispatcher`**, used to issue `[Host issued command]`. Other public methods of the Host are prohibited from calling this. User Steer goes through `Inject` (preserving immediate interruption capability), and Resume goes through `Prompt` to start a new run.
- `Agent.Steer(msg)` — the old steering interface; the new architecture uses `Inject` exclusively.
- `WithPermission*` — permission approval mechanism (manual approval for dangerous actions); the novel application has no dangerous actions, and is **not used**.

**Enabled Policy Hook**: `WithToolGate` —— the sole purpose is to hard-intercept `subagent` dispatch when `phase=complete` (`agents/build.go` `completePhaseGate`). If a user requests continuation/rewriting after completion, the Coordinator LLM might still dispatch sub-agents, whereas Writer writing out-of-bounds chapters will be rejected by `commit_chapter`, and `CheckpointDeltaGuard` will not release `end_turn` → infinite loop. The Flow Router returns `nil` at completion, which only blocks Host automatic dispatch, not LLM active dispatch, so the Gate adds a final state guard at the throat point. It is a narrow-use process fallback, **not an approval flow like `WithPermission*`**; do not mix the two.

---

## 7. Host Layer

### 7.1 Structure

```go
type Host struct {
    cfg               bootstrap.Config
    bundle            assets.Bundle
    store             *store.Store
    models            *bootstrap.ModelSet
    coordinator       *agentcore.Agent
    coordinatorCtxMgr *corecontext.ContextEngine  // linked with context window when switching models
    askUser           *tools.AskUserTool
    writerRestore     *ctxpack.WriterRestorePack

    observer     *observer
    router       *flow.Dispatcher  // Subscription + Route + FollowUp
    routerDetach func()
    usage        *UsageTracker
    usageCancel  context.CancelFunc
    budget       *BudgetSentinel   // Host policy component: executes user budget declarations (equivalent to aborting on behalf), subscribed before Dispatcher
    notifier     *notify.Notifier  // Observation layer: off-screen copies of run_end/repeat/budget alerts, never intervenes in control flow

    events, streamCh, done chan ...

    mu        sync.Mutex
    lifecycle lifecycle  // idle / running / paused / completed
    closeOnce sync.Once
}
```

### 7.2 Public APIs

**Lifecycle** (Coordinator's Run entry): `Start` / `StartPrepared` / `Resume` / `Continue` / `Steer` / `Abort` / `Close`

**Observation Channels**: `Events` / `Stream` / `Done` (sentinels inside streamCh for clearing stream)

**UI Aggregation**: `Snapshot()` —— TUI pulls all display data at once

**Configuration/Extensions**: Model Management (`SwitchModel`), external novel reverse-import (`ImportFrom`), co-creation chat (`CoCreateStream`), event replay (`ReplayQueue`), imitation profile building (`Simulate`/`ImportSimulationProfile`), and export (`Export`).

No business scheduling methods like `decideNext` or `retryActiveTask`. The Flow Router is a thin combination of a pure function + FollowUp, holding no implicit state such as "tasks currently retrying."

### 7.3 `waitDone` Behavior

```go
func (h *Host) waitDone() {
    h.coordinator.WaitForIdle()
    h.observer.finalize()

    if Phase == Complete { lifecycle=completed; send "creation completed" event }
    else if running        { lifecycle=idle;     send "Coordinator stopped (completed N chapters)" event }

    select { case h.done <- struct{}{}: default: }
}
```

Three actions: wait for idle → switch lifecycle → send terminal events + deliver done signal. **Prohibit `Inject` / `FollowUp` / `Prompt` inside the function body.** The entire Host enters the terminal state after the LLM completes a Run.

To move again, there are only two ways: the user actively triggers `Continue`/`Start`, or the process is restarted to run `Resume`.

> **Historical lesson**: We once patched this function to add an `idleResumeCount` for automatic Run restarts. In the only actual occurrence of a mimo marathon, it 100% failed to save the day, and instead masked the true cause in the agentcore layer ("thinking-only stop messages entering history"). **"Defensive restarts" at the Host layer are always misplaced fixes.** See `feedback_no_host_resilience.md` and §10 item 5 for details.

---

## 8. Startup and Recovery

### 8.1 Creation

```
User: "one-line requirement"
  → Host.Start
    → store.Progress.Init / store.Checkpoints.Reset
    → coordinator.Prompt(userPrompt) + flow.Dispatcher.Enable + Dispatch
    → Coordinator long loop: plan → write 1..N → review → done
```

### 8.2 Recovery (Restart after crash)

```
Process starts
  → Read Progress + latest Checkpoint + PendingCommit + PendingSteer
  → buildResumePrompt → short announcement (not step-level command)
  → coordinator.Prompt(resumePrompt) + Dispatcher.Enable + Dispatch
  → Coordinator continues according to Host commands
```

Resume starts a new Run using `Prompt` (resets turn counts, cleans context), not `FollowUp`. The specific next step is derived from the fact layer by the Flow Router after the first TurnEnd.

### 8.3 User Intervention

| Entry              | Prefix                  | Semantics                                       | Implementation                                                                          |
| ------------------ | ----------------------- | ----------------------------------------------- | --------------------------------------------------------------------------------------- |
| `Steer(text)`    | `[User intervention]` | Modify/query, requires Coordinator adjudication | Uses`Inject` during runtime; writes PendingSteer to `meta/run.json` during downtime |
| `Continue(text)` | `[User intervention]` | Continue writing, wake up after downtime        | Uses`FollowUp` during runtime; uses `Inject` during downtime to auto-resume the run |

Both entries are unified through the `interventionMsg` helper to add the `[User intervention]` prefix—it is the anchor for intervention classification in `coordinator.md`; previously sending bare text with Continue bypassed classification and was incorrectly dispatched to make the writer edit completed chapters (fixed).

`Inject` semantics: enqueue into current run queue during runtime; auto-resume run and inject when idle; queue up and wait for recovery when paused.

**Persistence of Long-term Intervention**: "Long-term requirements that only affect subsequent writing" (style/tendency types) identified during intervention classification are saved to `meta/user_directives.json` by the Coordinator calling `save_directive` (capped at 20 directives, de-duplication on add / indexing on remove). These are injected into `working_memory.user_directives` via `novel_context`—instantly visible to all sub-agents for every chapter, surviving compression and restarts, independent of the Coordinator's dialogue memory and dispatch transfers. Other classes of interventions naturally land in the store (length → compass/outline, settings → foundation, editing past chapters → PendingRewrites). Envelopes are used instead of system prompt modifications to protect the writer's cross-chapter system prefix cache.

### 8.4 Chapter Advance Control

`ChapterAdvanceGate` executes two user intents on different timescales:

| Intent | Source | Semantics |
|---|---|---|
| `AdvanceMode=review` + exact permit | `/review on`, `/next` | Persistent policy: approve each forward new chapter individually |
| `AdvanceHold` | Arbiter intervention | One-time intent: pause at a boundary, after rework drains, or after the target chapter is stably committed |

Permits bind to chapter numbers. Consumption requires the target in CompletedChapters, no PendingCommit, and a commit checkpoint, so a crash anywhere in the commit saga cannot reuse the permit for the next chapter. See [Chapter Advance Gate](chapter-advance-gate.md).

---

## 9. Directory Structure

```
internal/
  domain/         Pure data: Phase / FlowState / Progress / Checkpoint / Scope / Story / Plan /
                  Review / StateChange / Phase-Flow transition rules
  store/          File system persistence (tmp+rename + triple-action): progress / checkpoints / outline /
                  drafts / summaries / characters / world / signals / run_meta / runtime / session
  tools/          11 Agent tools: all write-type tools use atomic triple-actions + digest idempotency
                  + ConcurrencySafe=false + premise_structure (used internally by save_foundation) + ask_user
  agents/         build.go assemblies of Coordinator + three sub-agents; ctxpack/ Writer context compression strategy
  host/           host.go + resume.go + observer.go + events.go + usage.go + usage_replay.go
                  + stream_extract.go + cocreate.go
    flow/         router.go (pure function with 11 branches) + state.go + dispatcher.go + router_test.go
    reminder/     stop_guard.go (Coordinator) + subagent_guards.go (CheckpointDeltaGuard ×3)
    imp/          reverse-import of external novels: split → foundation → chapter-by-chapter analysis
    exp/          export of completed chapters: merge chapters → TXT / EPUB 3, driven by path suffix; pure read-only, does not depend on LLM
  entry/          tui (Bubble Tea) / headless / startup
  bootstrap/      config + ModelSet + provider failover + setup wizard
  models/         public model registry (OpenRouter, etc.) + price refresh (24h disk cache)
  errs/           error layering
  diag/           read-only diagnostic module subscribing to host event stream
  utils/          legacy of old architecture (minor parsing tools, new code should not depend on this)

assets/
  prompts/        coordinator (~55 lines) / architect-short|long / writer / editor / import-* / simulation-*
  references/     writing techniques + genre templates + long-form planning, etc.
  styles/         default / fantasy / romance / suspense

../agentcore     general-purpose Agent framework (sibling directory under go.work, allows adding general capabilities, no business logic)
../litellm       LLM gateway
```

### 9.1 Evolution Milestones

| Time       | Refactor                                                                                                                                             | Net Effect                                                 |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| 2026-04-10 | `internal/orchestrator/` (6342 lines) → `host/` + `agents/`                                                                                   | Runtime core -74%                                          |
| 2026-04-20 | Hybrid Coordinator: new`host/flow/`, slimmed down `reminder/`, `coordinator.md` from 88 lines → 45 lines                                      | Routing error rate approaches 0                            |
| 2026-05-02 | agentcore`WithMaxToolErrors(0)` + `isReasoningOnlyStopAssistant`; `StreamIdleTimeout=5min`; deleted `idleResumeCount` automatic resume patch | mimo / slow thinking stream successfully runs through      |
| 2026-06-05 | Rolling planning closed loop (`expand_arc`/`append_volume`) + `/import` reverse-engineered layered continuation + user volume intervention     | 200+ chapters successfully runs through for the first time |

Actual test runs: `hy3-preview` (free) wrote 12 chapters in 73 minutes; `mimo-v2.5-pro` wrote 10 chapters totaling 84,000 words (averaging 8,400 words per chapter); both completed in a single run. The long-form gpt-5.4 novel *Ordinary Bones* wrote 235 chapters totaling 1.27 million words (averaging 5,407 words per chapter), successfully running through the rolling planning closed loop.

---

## 10. Explicit Non-Goals (Strictly Prohibited)

Violations represent architectural deviations.

1. **Do not introduce Task / Job / WorkItem concepts.** The "current task" shown on the UI is an event stream projection, not a fact.
2. **Do not introduce Dispatcher / Scheduler / Ready Evaluator.** Decision rights reside in the Coordinator LLM and the tools layer.
3. **Do not write an "idle resume" mechanism like `idle_dispatch`.** Coordinator Run end = Host sends done.
4. **Do not bypass the Coordinator to call SubAgents in the Host.** The Flow Router issues `coordinator.FollowUp` commands so the Coordinator generates the `tool_call`. Resume starts a new Run via `Prompt`.
5. **Do not add automatic resume patches in the Host for LLM abnormal halts.** Run end = Host enters terminal state. The former `idleResumeCount` has been deleted (see details in §7.3, `feedback_no_host_resilience.md`).
6. **Do not infer task completion based on "tool exec end."** The sole evidence of completion is the checkpoint write.
7. **Do not build a four-layer model of WorkflowInstance / TaskInstance / Command + Apply.** The fact layer has only three types: Progress + Checkpoint + Artifact.
8. **Do not support parallel tasks.** A single active Coordinator Run, advancing one book serially. Use multiple processes for multiple novels.
9. **Do not perform LLM calls in the tools layer** (except for the Agent tool itself). Pure IO + validation + idempotency.
10. **Do not let the UI read the Store directly.** The UI can only subscribe to events or read the Host's `Snapshot()`.
11. **Do not use signal files for IPC.** The Host directly reads Progress + Checkpoint + layered outlines; deriving instructions from facts in `flow.Route` is a clean, vertical route.
12. **Do not write Flow state machines on the Host side.** Flow labels are only updated by tools; the Router only reads and does not write.
13. **Do not write hard-coded fallbacks for "LLM hallucinations."** Optimize prompts, improve tool return structures, and make `novel_context` present facts more clearly—rather than forcing Host process changes.
14. **Do not let diag / observation layers intervene in control flow.** Diagnostics are read-only, producing only Findings and desensitized exports; automatic repairing / resuming / process modifications are strictly prohibited (see §2.3 Observer Discipline).
15. **Budgets and alerts must not enter the Route/tools layer, and alerts must not enter the control flow.** `BudgetSentinel` is a Host policy component (executes user pre-signed Aborts, does not evaluate model behavior); `notify` is pure observation (does not retry, redirect, or halt). `flow.Route` remains a pure function, unaware of both.

---

## 11. Verification Strategy

### 11.1 Stability Scenarios

- **A Marathon Run**: 80~200 chapters run through in one go, resulting in Phase=complete. Provider failover and tools transient retries are allowed; Host resume or multiple Coordinator Runs are prohibited.
- **B Crash Recovery**: Kill process after drafting / before committing chapter N → Resume → continues from consistency_check, without rewriting drafts already saved to disk. `checkpoints.jsonl` contains no duplicate steps.
- **C Provider Jitter**: Simulate intermittent 503 errors → litellm failover; LLM main loop remains unaware.
- **D User Intervention**: Steer during runtime → Coordinator handles it in the next turn; Steer during downtime → next Resume prompt includes it.

### 11.2 Compliance (Can be written as linter / test)

- `internal/host/` is not allowed to import scheduling packages like `internal/scheduler`.
- The number of public Host lifecycle APIs remains stable; new public methods can only be "extension entry points" (co-creation / import / model management).
- `coordinator.Inject` / `FollowUp` / `Prompt` are prohibited inside the `waitDone` function body.
- Recovery-related code can only appear in `host/resume.go`.
- `flow.Route` must be a pure function: reading Store / any IO is prohibited.

### 11.3 Quality Iteration

Modifying `writer.md` immediately causes style changes; adding new editor review dimensions is backward-compatible (save_review accepts structured JSON). Adding a new reference guide md requires wiring in three places (`tools.References` field + `loadReferences` in `assets/load.go` + `writerReferences`/`architectReferences` injection in `novel_context.go`), rather than being loaded automatically by just dropping it in the directory—`References` is an explicit field mapping, facilitating role/chapter-level pruning.

**Whole-book Style Statistics (`internal/stylestat`)**: The review window within an arc is naturally blind to whole-book style freezes like "sentence pattern tics dozens of times per chapter, isomorphic chapter-end patterns, word-for-word repetition across chapters"—every single part looks normal when viewed in isolation. The chapter path in `novel_context` runs deterministic statistics (sentence pattern models, near-window high-frequency phrases, cross-chapter duplicate sentences, chapter-end patterns, title format mix-ups) on all completed chapters, and injects them into `episodic_memory.style_stats`: the editor adjudicates numerically on the aesthetic dimension, and the writer self-avoids based on this. **Statistics belong to code, adjudication belongs to LLM**—thresholds are not hard-coded, and whether a number constitutes a flaw is adjudicated by the model based on the genre. Its parallel product baseline `rules.Lint` (markdown leftovers / non-Chinese snippets) is always executed in `commit_chapter`, only returning facts.

### 11.4 质量迭代

改文风 → 改 `<书目录>/style/`（用户级）或 assets/voice.md（内置），文风评测集 A/B 验证；新增评审维度 → 改 editor.md（save_review 结构化接收）；新增参考资料 → 三处显式接线（`tools.References` + `loadReferences` + novel_context 注入映射）。

**全书级风格统计（`internal/stylestat`）**：Host 为每本书创建唯一 `StyleStatsIndex`，并显式注入 `novel_context` 与 `commit_chapter`。首次启动从全部已完成章节恢复索引，后续对新增/重写章节增量更新（句式模式/高频短语/跨章重复句/章末形态），相同书状态下复用快照并注入 `episodic_memory.style_stats`：editor 按数字裁定，writer 据此自避免。离线 eval 仍可直接调用纯函数 `Compute`。**统计归代码，裁定归 LLM**。

---

## 12. Summary

> **Let the LLM finish a novel in a single run. The Host only handles startup, recovery, routing, and observation, keeping decision-making as much as possible with the model.**

There is no workflow engine, no task queue, no dispatcher, and no scheduler. All we have is:

- A Coordinator with 100,000 turns.
- Three functional sub-agents (independent contexts and models).
- 11 atomic tools.
- A jsonl checkpoint file.
- A Host shell of ~860 lines.
- A pure Flow Router function of ~150 lines (11 branches + unit tests).

Every line of Host business code is a bet hedged against model upgrades. **Minimum Host, fattest Prompt (quality layer), and strongest tools** allow the architecture to automatically improve every year—Coordinator decisions get more accurate, Writer writes better, Editor reviews better, and Architect plans more precisely, all being benefits obtained directly by changing models without architecture awareness.

Conversely, hard-coding rules in the Host like "the last review said to rewrite chapters 3 and 5" or "stop the machine if no progress is made after 3 attempts" becomes **negative return** when the model upgrades: judgments that should have been made by the LLM become redundant, and protective logic turns into false alarms. **The worst part is that no one dares to delete it—deleting it means "trusting the model," and psychological baggage is harder to clean up than code.** The more such code is left behind, the higher the future refactoring costs.

**Extensibility comes from correct extension points**: changing style → modify prompt; new review dimensions → modify prompt; new genres → add reference guides; new sub-agent types → add a single line to `SubAgentConfig`; parallel multiple novels → multiple processes.

The sole discipline: **When someone wants to "make the Host smarter," ask "why not make the LLM smarter" first.** If this question cannot be answered with a reason of "Host must," do not add code to the Host.
