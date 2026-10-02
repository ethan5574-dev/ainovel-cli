# Refactoring Proposal: Hybrid Coordinator — Host Routing × LLM Adjudication

> Status: **Adopted and Landed** (2026-04-20)
> Research Date: 2026-04-20
> Corresponding Current Document: `docs/architecture.md` §2 / §3 / §7 / §8 / §13 has been updated synchronously
>
> **This document is the second draft.** See Appendix A for issues with the first radical draft (completely removing the Coordinator), which is retained to avoid repeating mistakes.
>
> Implementation Results:
> - `internal/host/flow/` newly created (router.go / state.go / dispatcher.go / router_test.go; all 15 branch unit tests passed)
> - `internal/host/reminder/` deleted `flow.go` / `queue_guard.go` / `book_complete.go`; retained StopGuard and sub-agent Guards
> - `assets/prompts/coordinator.md` compressed from 88 lines to ~45 lines (responsibility narrowed to executing Host instructions + adjudication + startup selection)
> - `internal/host/resume.go` significantly simplified, generating only a label and a short prompt; the specific next step is dispatched by the Router after the first TurnEnd
> - `internal/store/` added helper methods `HasArcReview` / `HasArcSummary` / `HasVolumeSummary` / `CheckConsistency`
> - Fixed the bug where the observer agent state failed to stay on "working"

---

## 1. Background

### 1.1 Project Positioning

```
agentcore       — General agent framework
litellm         — General LLM gateway
ainovel-cli     — Novel writing vertical agent (this project)
```

The decision space of a vertical agent is **closed**: the flowchart is fixed, branches are limited, and it is fact-driven. Applying the design philosophy of general agents ("betting on model capabilities") to vertical scenarios is overly pure.

### 1.2 User Goals (in order of priority)

1. **Stability** — Continue writing smoothly without interruption due to routing errors.
2. **Reaping LLM Upgrade Benefits** — The architecture does not fight against model capabilities.
3. **Fully Leveraging Multi-agent Capabilities** — Clear division of functional roles.

This proposal represents a **Pareto improvement** among the three goals (without sacrificing any goal to exchange for another).

---

## 2. Current Status Survey

### 2.1 Classification of Coordinator Decision Points

Extracting the decision points of `coordinator.md` one by one:

| # | Decision Point | Nature | Frequency |
|---|---|---|---|
| 1 | Select `architect_long` / `short` at startup | Adjudication (semantic understanding) | 1 time per book |
| 2 | Input expansion (auto-completion for <20 characters) | Adjudication (creativity) | 0-1 time per book |
| 3 | Plan completion loop | Routing (fact-driven) | 1-3 times |
| 4 | Next step after each chapter commit | **Routing** | **1-2 times per chapter** |
| 5 | Step-by-step execution of arc-end review | Routing | 3-5 times per arc |
| 6 | Verdict branching in review | Routing (already codified, see §2.3) | 1 time per arc |
| 7 | User intervention handling | Adjudication (must use LLM) | Arbitrary |
| 8 | Sub-agent error re-dispatch | Routing | Occasional |
| 9 | Output summary upon book completion | Routing | 1 time |

**Conclusion**: Among the 9 decision points, 6 are pure routing (table lookup), and 3 are genuine adjudications that require the LLM. **Routing happens far more frequently than adjudication** (1-2 times per chapter vs a few times per book).

### 2.2 The Reminder Channel is Already a Semi-Finished Product of Process Codification

Generators under `internal/host/reminder/` produce **action-specific instructions** based on facts each round:

- `flow.go` → `"Current flow=writing, next_chapter=37. Please call subagent(writer, \"write chapter 37\")..."`
- `queue_guard.go` → `"Current flow=rewriting, pending queue: [3, 5]. Please call writer to rewrite chapter-by-chapter immediately..."`
- `book_complete.go` → `"The book is complete. Please output the book summary..."`

**Double dispatch currently exists in the architecture**:
```
Policy layer: coordinator.md defines "if A, then B"
  ↓
Reminder layer: concrete rules based on facts each round → generates "please do B now"
  ↓
LLM layer: reads reminder to generate tool_call (basically repeating the reminder)
  ↓
SubAgent execution
```

**The LLM is actually just "executing" the instructions given to it by the Reminder.** This middle step both consumes tokens and introduces uncertainty (the LLM might not fully comply with the reminder, such as the observed mid-routing errors).

### 2.3 The Tool Layer Already Carries a Large Number of Judgments

- `save_review.evaluateScorecardGate()`: Scorecard gate, automatically upgrading "accept" to "polish/rewrite".
- `save_review.ContractStatus` check: `contract=missed` automatically upgrades to "rewrite".
- `commit_chapter.CheckArcBoundary()`: Instantly computes `arc_end / needs_expansion / needs_new_volume`.
- `commit_chapter.applyCompletion()`: Instantly determines `book_complete`.
- `CommitResult` returns 17 fact fields.

**Conclusion**: The tool layer has already codified most "judgments," and the decisions the Coordinator makes upon receiving these facts are basically if-else logic.

### 2.4 Actual Cost of Current Status

Coordinator LLM turns per chapter:
- **1-2 turns per chapter** (reading system prompt ~3000 tokens + reminder ~200 tokens + history + CommitResult ~500 tokens → generating tool_call ~50 tokens).
- About **200-400 turns** of Coordinator LLM calls for a 200-chapter novel.
- Among them, **~90% are pure routing** (LLM repeating the reminder), and **~10% are adjudications**.

**About 3500-7000 tokens are spent on Coordinator decisions per chapter, 95% of which is redundant** (the Reminder has already computed the answer).

---

## 3. Design Proposal: Hybrid Coordinator

### 3.1 Core Concept

**Move process decisions from the LLM to the Host, but retain the Coordinator as an adjudication node and command execution channel.**

```
┌──────────────────────────────────────────────────────────┐
│                   Entry (TUI / headless)                 │
└────────────────────────────────┬─────────────────────────┘
                                 │ Start / Resume / Steer
┌────────────────────────────────▼─────────────────────────┐
│                            Host                          │
│                                                          │
│   ┌──────────────────────────────────────────────────┐   │
│   │  Flow Router (Newly added core)                  │   │
│   │  ───────────                                     │   │
│   │  Subscribes Coordinator events: triggered on     │   │
│   │  subagent tool return                            │   │
│   │  Pure function: route(Progress, Checkpoint,      │   │
│   │      Boundary) → NextInstruction                 │   │
│   │  With command → coordinator.FollowUp(command)    │   │
│   │  No command (adjudication) → let LLM decide      │   │
│   └──────────────────────────────────────────────────┘   │
│                                                          │
│   Retained: Lifecycle API / Observer / Usage Tracker     │
│   Retained: resume.go (simplified, core logic unchanged) │
└────────────────────────────────┬─────────────────────────┘
                                 │
┌────────────────────────────────▼─────────────────────────┐
│                    Coordinator Agent (LLM)               │
│                                                          │
│   Responsibilities narrowed to two types:                │
│   1. Receive Host FollowUp commands → generate           │
│      corresponding tool_call                             │
│   2. Adjudicate user Steer on arrival (evaluation/query) │
│                                                          │
│   coordinator.md: 88 lines → ~25 lines                   │
│   MaxTurns: 1000 retained (responding to user steer      │
│      + executing Host commands)                          │
└────────────────────────────────┬─────────────────────────┘
                                 │
                                 ▼
          ┌──────────────────────┼───────────────────────┐
          ▼                      ▼                       ▼
     ┌────────┐             ┌────────┐             ┌────────┐
     │Architect│             │ Writer │             │ Editor │
     └────────┘             └────────┘             └────────┘
```

### 3.2 Redistribution of Responsibilities

| Layer | What it does | What it does not do |
|---|---|---|
| **Host / Flow Router** | Reads facts → pure function routing → FollowUp commands | Calls SubAgent directly (still goes through Coordinator) |
| **Coordinator** | Executes Host commands + adjudicates user intervention + selects planner at startup | Decides "what to do next" on its own |
| **SubAgent (A/W/E)** | Respective duties | Unchanged |
| **Tool Layer** | Atomic persistence + returns facts | Unchanged |

**Key Invariants**:
- ✅ The Coordinator remains a continuous agent run, retaining whole-book "continuous awareness".
- ✅ User Steer still goes through `coordinator.Inject()`, preserving the immediate interruption capability.
- ✅ `SubAgentTool` is still called by the LLM (following the agentcore native path); event stream / ContextManager / model switching remain unchanged.
- ✅ Zero modifications to agentcore.

### 3.3 Concrete Logic of Flow Router

```go
// internal/host/flow/router.go

type NextInstruction struct {
    Agent  string   // architect_long / architect_short / writer / editor
    Task   string   // description of the task for the sub-agent
    Reason string   // optional reason for the Coordinator (convenient for debugging)
}

type RouterState struct {
    Progress        *domain.Progress
    LatestCheckpoint *domain.Checkpoint
    // Arc boundary in layered mode (computed when the previous chapter is completed)
    LastCompleted   int
    ArcBoundary     *store.ArcBoundary
    HasArcReview    bool
    HasArcSummary   bool
    // Missing items in basic settings
    FoundationMissing []string
}

// Route returns the next step command. Returning nil means letting the Coordinator adjudicate (adjudication scenario).
func Route(s RouterState) *NextInstruction {
    p := s.Progress

    // 0. Terminal state: let LLM output summary, no routing
    if p.Phase == domain.PhaseComplete {
        return nil
    }

    // 1. Planning phase: adjudication (selecting planner) is handled by LLM, no routing
    if p.Phase != domain.PhaseWriting {
        return nil
    }

    // 2. Writing phase
    // 2a. Rewrite/Polish queue prioritized
    if len(p.PendingRewrites) > 0 {
        ch := p.PendingRewrites[0]
        verb := "rewrite"
        if p.Flow == domain.FlowPolishing {
            verb = "polish"
        }
        return &NextInstruction{
            Agent:  "writer",
            Task:   fmt.Sprintf("%s chapter %d", verb, ch),
            Reason: fmt.Sprintf("PendingRewrites queue has %d chapters remaining", len(p.PendingRewrites)),
        }
    }

    // 2b. Reviewing: no routing, let the Coordinator branch based on save_review verdict
    if p.Flow == domain.FlowReviewing {
        return nil
    }

    // 2c. Arc-end post-processing in layered mode
    if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
        b := s.ArcBoundary
        if !s.HasArcReview {
            return &NextInstruction{
                Agent:  "editor",
                Task:   fmt.Sprintf("perform arc-level review for volume %d arc %d", b.Volume, b.Arc),
                Reason: "arc-end review incomplete",
            }
        }
        if !s.HasArcSummary {
            return &NextInstruction{
                Agent:  "editor",
                Task:   fmt.Sprintf("generate arc summary for volume %d arc %d", b.Volume, b.Arc),
                Reason: "arc summary incomplete",
            }
        }
        if b.NeedsExpansion {
            return &NextInstruction{
                Agent:  "architect_long",
                Task:   fmt.Sprintf("展开第 %d 卷第 %d 弧（expand_next_arc）", b.NextVolume, b.NextArc),
                Reason: "下一弧骨架待展开",
            }
        }
        if b.NeedsNewVolume {
            return &NextInstruction{
                Agent:  "architect_long",
                Task:   "evaluate and execute save_foundation(type=append_volume) or mark_final",
                Reason: "volume end requires deciding whether to append new volume",
            }
        }
    }

    // 2d. Normal continuation
    next := p.NextChapter()
    return &NextInstruction{
        Agent:  "writer",
        Task:   fmt.Sprintf("write chapter %d", next),
        Reason: "continuation",
    }
}
```

**Function Characteristics**:
- Pure function (input: RouterState, output: NextInstruction).
- Unit testable (assert routing results given states).
- **Returning nil is legal** — indicates "this is an adjudication scenario, please let the LLM decide."

### 3.4 Trigger Timing

Host subscribes to `agentcore.EventToolExecEnd` events:

```go
coordinator.Subscribe(func(ev agentcore.Event) {
    if ev.Type == agentcore.EventToolExecEnd && ev.Tool == "subagent" && !ev.IsError {
        // SubAgent just returned → read latest state → route
        h.flowRouter.Dispatch()
    }
})
```

```go
func (r *FlowRouter) Dispatch() {
    state := r.loadState()
    instruction := Route(state)
    if instruction == nil {
        return // adjudication scenario, let LLM decide
    }
    msg := formatInstruction(instruction)
    _ = r.coordinator.FollowUp(agentcore.UserMsg(msg))
}

func formatInstruction(i *NextInstruction) string {
    return fmt.Sprintf(
        "[Host issued command] Next step: call subagent(%s, %q)\n"+
        "Reason: %s\n"+
        "This is an explicit instruction from the process layer. Please execute it immediately without calling novel_context first and without outputting reasoning.",
        i.Agent, i.Task, i.Reason,
    )
}
```

### 3.5 Reactivity and Concurrency

**User Steer Path** (unchanged):
```
Steer → coordinator.Inject(UserMsg("[User intervention] xxx"))
```

- Running: message inserted into current run queue
- Idle: resume run
- Paused: queued up

**Concurrency of Routing Commands + Steer**:
- Both enter the Coordinator's message queue, processed in native agentcore order.
- If Host just sent `FollowUp("[Host command] write chapter 37")`, and user Steer `"stop, adjust style"` arrives immediately after:
  - Does the Coordinator process Host command first? Or user Steer?
  - **The semantic of `Inject` is enqueuing at the head of the current queue**, so user Steer is processed first.
  - This is expected behavior: user intervention has higher priority than Host routine scheduling.

**Avoiding Host Command conflicts with Steer**:
- The Flow Router **briefly pauses** for a few turns upon receiving the "Steer injected" signal (letting the Coordinator finish processing Steer before routing).
- Perceives Steer processing results by subscribing to `agentcore.EventMessageEnd` + checking Progress state changes.

### 3.6 Simplified coordinator.md Example

Cut down from 88 lines to about 25 lines:

```markdown
You are the general coordinator of novel creation.

## Your Working Mode

**Mainline**: The Host will issue a `[Host issued command]` message after each sub-agent returns, telling you which sub-agent to call next and what to do. Generate the corresponding tool_call immediately upon receiving the command; do not call novel_context for reasoning first, and do not repeat.

**Adjudication**: You need to judge independently in the following scenarios (the Host will not issue commands; you must act actively):

### Startup: Select Planner

- Default → `architect_long`
- Only when the user explicitly requests short/single-volume/sketches and limits the length to 25 chapters → `architect_short`

If user input is < 20 characters, supplement the differentiation direction, target readers, and at least one unconventional story hook in the task description before dispatching.

### User Steer

Format: `[User intervention] xxx`

- **Query** (asking about status/settings): Output the text answer directly without calling tools; the Host will continue dispatching.
- **Modification** (requesting setup change / rewrite / style adjustment): Evaluate the impact scope:
  - Involves setup changes → call architect_* to do `save_foundation(type=...)`
  - Involves already written chapters → let the tool automatically write target chapters into `PendingRewrites` (can be done by explaining rewrite intentions when calling writer again)
  - Only affects subsequent style → describe requirements briefly, and append to the task description of the writer next time you receive the Host command.

## Tools

- `subagent(agent, task)`: Call sub-agents.
- `novel_context`: Only use when required by user queries; do not call first after Host commands arrive.

## Sub-agents

- `architect_long` / `architect_short` / `writer` / `editor`

## Prohibited

- Calling novel_context first before acting upon Host commands.
- Deciding the next step on your own without user Steer and without Host commands.
```

### 3.7 Significant Slimming of the Reminder Channel

**Deleted**:
- `flow.go` (The Host FollowUp already issues specific instructions; Reminder's routing alerts lose value)
- `queue_guard.go` (Queuing is guaranteed by Host Router)
- `book_complete.go` (Host issues summary command via FollowUp when Phase=Complete)

**Retained**:
- `subagent_guards.go` (Writer/Architect/Editor's StopGuard, ensuring sub-agents do not end empty-handed)
- Newly added lightweight `foundation_reminder.go`: informs Coordinator of missing items during planning (this is **information needed for adjudication** rather than routing commands)

**StopGuard Retained**:
- Coordinator's StopGuard is retained (intercepts `end_turn` as backup when `Phase != Complete`)
- Adds reminder injection when "Host command received but corresponding sub-agent not called in this round".

### 3.8 Minor Simplification of resume.go

Currently, `buildResumePrompt` generates natural language instructions precise to the step based on checkpoints (121 lines).

New Architecture:
- Reads Progress first on Resume; Flow Router computes `NextInstruction`.
- Coordinator receives a **very short** resume prompt, then waits for the Host's FollowUp command.

```
[Resume] The book "xxx" has completed N chapters, entering the XX phase.
Please wait for the Host's next command, or process user interventions that might have been left during downtime.
```

Almost all branch logic sinks to the Flow Router (the Router naturally routes based on state; Resume does not need a special path).

---

## 4. Evaluation of Goal Attainment

### 4.1 Stability

| Risk | Current | New Architecture |
|---|---|---|
| Coordinator selects wrong architect | Occurred (mid-routing error) | Adjudication at startup remains, but prompt goes from three tiers to binary (completed), significantly reducing error rate |
| Coordinator violates "only say write chapter N" | Occurred | Host issues fixed-format commands, no longer requiring LLM to generate task descriptions |
| Coordinator misses queue_drained check | Occurred | Host Router forces sequential progression |
| Coordinator forgets to call editor after arc-end commit | Possible | Host Router dispatches directly upon detecting IsArcEnd && !HasArcReview |
| Crash recovery branch omission | Known gap | Flow Router's state machine naturally covers all branches |
| StopGuard consecutive 5 blocks upgrades to fatal | Exists | Once Host commands are clear, it is hard for LLM to block consecutively (unless prompt completely fails) |

### 4.2 LLM Upgrade Benefits

| Dimension | Retention |
|---|---|
| Writer model upgrade → Writing quality | 100% |
| Editor model upgrade → Review accuracy | 100% |
| Architect model upgrade → Refined planning | 100% |
| **Coordinator model upgrade → Better adjudication** | **100%** (adjudication scenarios retained) |
| ~~Coordinator model upgrade → Better routing~~ | Waived (routing dimension waived) |

**Important Retention**: Adjudication scenarios such as user intervention evaluation, planner selection, and verdict boundary judgments are still handled by the LLM, benefiting directly from model upgrades.

### 4.3 Multi-agent Capabilities

- SubAgent count, duties, and assembly methods **remain completely unchanged**.
- Model heterogeneity (coordinator/architect/writer/editor independent configurations) **remains completely unchanged**.
- Coordinator remains a continuous run, preserving the "whole-book perspective."
- Collaboration media (artifacts in the Store) remain unchanged.

### 4.4 Reactivity

- Immediate interruption capability of user Steer via `coordinator.Inject` is **completely retained**.
- Host Router dispatches commands when SubAgent returns, sharing the same message channel with user Steer.
- The priority of `Inject` is higher than `FollowUp` (`Inject` is queue-jumping), so Steer will not be crowded out by Host commands.

### 4.5 Token Cost

Current per chapter: Coordinator ~3500-7000 tokens × 1-2 turns = 3500-14000 tokens

New architecture per chapter:
- Coordinator prompt compressed from ~3000 tokens to ~800 tokens.
- Still requires 1 turn per chapter (Coordinator reading FollowUp command + generating tool_call).
- Totals ~1000-1500 tokens.

**Saves 60-80%** (not as high as the radical 100% savings, but preserves reactivity and whole-book perspective).

---

## 5. Impact on docs/architecture.md

### 5.1 §2 Core Principles Adjustment

**Principle 1** (LLM driving main loop) → adjusted to:
```
LLM-driven creation and adjudication, Host-driven process routing.

- Creation and adjudication (decisions requiring semantic understanding, quality judgment, or intent recognition) remain with the LLM.
- Process routing (reading facts → table lookup → issuing commands) is handled by Host code.
- The Host does not call SubAgents bypassing the Coordinator; instead, it issues explicit commands via FollowUp, retaining the Coordinator as a command execution channel and adjudication node.
```

**Principle 2** (betting on model capabilities, not hard-coding) → adjusted to:
```
Bet on the model in creation and adjudication dimensions (Writer/Editor/Architect/Coordinator adjudication capabilities); represent process routing in code (vertical agents have a closed decision space; LLMs have no advantage in table lookup tasks).
```

### 5.2 §13 Prohibited List Adjustment

- §13.13 "No deterministic control plane where Host reads signals → injects next step commands" →
  **Corrected wording**: "Do not use signal files for IPC (directly read Progress + Checkpoint instead); the Host reading facts and issuing explicit sub-agent call commands via `coordinator.FollowUp` is a reasonable vertical route."
- §13.14 "No hard-coded Flow transitions in state machines" →
  **Corrected wording**: "Flow labels are still only updated by tools (do not write state machines like 'if A, then SetFlow(B)' in the Host), but the Flow Router can decide who to call next based on Flow and other facts."

### 5.3 §7 Agent Assembly Adjustment

- Retain Coordinator assembly.
- `coordinator.md` compressed from 88 lines to ~25 lines.
- Reminder channel reduced (deleted flow/queue_guard/book_complete, retained foundation/subagent_guards).
- Added `internal/host/flow/` package.

---

## 6. Known Weaknesses (Honestly Listed)

### 6.1 Long-term Evolution of Flow Router

- As new scenarios are added (new flow states, new arc-end post-processing), the Router's switch-case will grow longer.
- Strict constraint required: **only process routing, no business logic**; write unit tests for decision rules.
- Warning of v0.0.1 `handleSubAgentDone` is always valid; but this proposal avoids sliding into a god object via "pure functions + unit tests + calling only pure facts."

### 6.2 Complexity of User Intervention

- Current design leaves Steer entirely to Coordinator LLM adjudication.
- But some Steer spans multiple categories (e.g. "clear up character A's past chapters + add subplots for him later").
- Relies on LLM's capability to dissect; prompt needs to give clear guidelines.
- **This part directly benefits from model upgrades** (compared to hard-coding InterventionAgent's enum classification, LLM flexible adjudication matches real scenarios better).

### 6.3 Pre-requisite Dependency of Fact Layer Consistency

- Router makes decisions based on Progress + Checkpoint; the fact layer must be reliable.
- Current `withWriteLock` is well encapsulated; commit_chapter's triple-actions complete atomically.
- But if inconsistency occurs in the fact layer (e.g. Progress says chapter 3 is completed but it is missing under chapters/), the Router will make wrong decisions.
- Suggestion: Add an **integrity/consistency check for the fact layer** at startup (e.g. warn if Progress.CompletedChapters does not match the chapters/ directory).

### 6.4 Coordinator Still Retains Possibility of LLM Routing

- Even if instructions are explicit, the LLM might "creatively" fail to execute them (e.g. generating a block of reasoning text before calling tools).
- StopGuard fallback: injects reminder if Host command is received but sub-agent is not called in this round.
- This is a backup, not a prohibition—occasional "extra thinking steps" from strong models are not a bad thing.

### 6.5 Higher Requirements for Test Coverage

- Flow Router is a pure function and must have complete unit tests (covering all Phase × Flow × Boundary combinations).
- Integration tests: simulate the complete chain of "commit → router → FollowUp → coordinator response → subagent".
- Crash recovery test: kill process and then resume, asserting the Router derives the correct next step.

---

## 7. Implementation Roadmap

### Phase 1: Fact Layer Strengthening (approx. 0.5 day)

- Implement consistency checks in §6.3: scan once during startup/Resume, generating warnings.
- Ensure `store.HasArcReview(vol, arc)` and `HasArcSummary(vol, arc)` APIs are available (add if missing).

### Phase 2: Introducing Flow Router Skeleton (approx. 1 day)

- Create `internal/host/flow/` package:
  - `route.go` — pure function `Route(state) → *NextInstruction`
  - `dispatcher.go` — subscribes to events + issues FollowUp commands
  - `route_test.go` — unit tests covering all branches
- Control activation via config switch `flow_driven: true/false`.
- Default to false, running comparisons first.

### Phase 3: Activation and Verification (approx. 1 day)

- Turn on `flow_driven: true`.
- Run a 30-50 chapter novel, comparing metrics:
  - Coordinator LLM call counts
  - Routing errors (should be 0)
  - Reactivity (steer interruption working normally)
- Fix bugs, adjust Router rules.

### Phase 4: coordinator.md Simplification + Reminder Slimming (approx. 0.5 day)

- Modify coordinator.md according to §3.6.
- Delete `reminder/flow.go / queue_guard.go / book_complete.go`.
- Retain necessary foundation reminders.
- Update sub-agent StopGuards if needed (usually not required).

### Phase 5: resume.go Simplification (approx. 0.5 day)

- Delete most branches of `buildResumePrompt`.
- Replace with a short, generic "[Resume] Please wait for Host instructions" message.
- After Resume, the Router naturally derives the next action to continue.

### Phase 6: Architecture Document Update (approx. 0.5 day)

- Modify `docs/architecture.md` §2 / §13 / §7 according to §5.
- Change this proposal document status to "Adopted" and archive it to `docs/history/`.

### Phase 7: Observation Period (2-4 weeks)

- Continuously run 2-3 long novels (100+ chapters each).
- Record all routing errors (if any), reactivity issues, and unexpected Coordinator behavior.
- Fine-tune Router rules and coordinator.md based on observations.

**Total of about 4 days of implementation + observation period.**

---

## 8. Comparison Table

| Dimension | Current Architecture | Hybrid (This Proposal) | Radical Proposal (Appendix A) |
|---|---|---|---|
| Stability | Mid (LLM occasionally routes wrong) | **High** | High |
| Reactivity | High | **High** | **Low** (Host directly calling SubAgent cannot be interrupted) |
| LLM Benefit | 100% | **100%** | 85% (routing dimension waived) |
| Token Savings | 0 | ~70% | ~95% |
| Whole-book View | Yes | **Yes** | None (each SubAgent runs independently) |
| Implementation Cost | - | Mid (approx. 4 days) | High (approx. 1 week + modify agentcore) |
| Document Update | - | Minor (§2/§13 fine-tuning) | Major (§2 principles rewritten) |
| Modifies agentcore | - | No | Possible (calling SubAgent directly) |
| Rollback Difficulty | - | Low (config switch) | High |

---

## 9. Decision Points

1. **Adopt this proposal (Hybrid Coordinator)?** [x] Adopt · [ ] Adopt with modifications · [ ] Reject
2. Should Phase 3 land and be validated as an independent PR first? [x]
3. Should `docs/architecture.md` §2 / §13 adjustments be handled together in this run? [x]
4. Observation period length: [x] 2 weeks · [ ] 4 weeks · [ ] Longer

---

## Appendix A: Evaluated Radical Proposal (Completely Removing Coordinator)

> First draft proposal. Demoted to reference due to reactivity regression, technical feasibility concerns, and loss of Coordinator's whole-book perspective.

The core of the radical proposal: Host directly calls `SubAgentTool.Execute`, bypassing Coordinator LLM.

**Identified Issues**:

1. **Reactivity Regression**: `SubAgentTool.Execute` is a blocking synchronous call; user Steer must wait for the current SubAgent to return before being processed. Current architecture's `Inject` can interrupt immediately.
2. **Technical Feasibility Concerns**:
   - Host directly calling SubAgentTool violates agentcore usage conventions.
   - Event stream (Event of `Subscribe`) might not bubble up to observer correctly.
   - SubAgent's `ContextManagerFactory` / `OnMessage` callback paths are unknown.
   - Requires modifying agentcore or majorly rewriting observer.
3. **Loss of Coordinator Whole-book Perspective**: Each SubAgent runs independently without a "continuous LLM watcher." In long runs, issues like style drift and character division lack a layer of invisible guard.
4. **InterventionAgent Over-simplification**: Radical proposal uses an enum (query/modify_setting/rewrite_chapters/adjust_style/noop) to classify user intent; real Steer might span multiple categories, and a forced schema will misclassify.
5. **Heavy Architecture Document Rewrite**: §2 core principles overturned; 30% of document reasoning affected.
6. **FlowDriver Grows into God Object**: A loop stuffing all routing logic, modified whenever a scenario is added, isomorphic to v0.0.1 `handleSubAgentDone`.

The Hybrid proposal sidesteps the first 4 issues, reduces the 5th to fine-tuning, and manages the 6th via "pure functions + unit tests."

---

## Appendix B: Allocation of Decision Points

| Decision Point | Current Location | New Architecture Location | Type |
|---|---|---|---|
| Select Planner | coordinator.md L26-29 | Coordinator LLM Adjudication (at startup) | Adjudication |
| Input Expansion | coordinator.md L31 | Coordinator LLM Adjudication (at startup) | Adjudication |
| Plan Completion Loop | coordinator.md L36-38 | Host Router Phase=Premise/Outline branches (returns nil to let LLM decide or explicit FollowUp architect) | Mixed |
| Next Step per Chapter | coordinator.md L46-51 + reminder/flow | **Host Router 2d branch** (FollowUp writer) | Routing |
| Arc-end Review | coordinator.md L78-82 | **Host Router 2c branch** (FollowUp editor/architect) | Routing |
| verdict Branching | coordinator.md L59-61 + save_review tool | Tool layer already codified; Router only reads Flow | Routing (Completed) |
| User Intervention | coordinator.md L67-70 | Coordinator LLM Adjudication (upon receiving Inject message) | Adjudication |
| Planner Error Re-dispatch | coordinator.md L40 | Host Router detects FoundationMissing unchanged, retry count | Routing |
| Summary on Book Completion | coordinator.md L63-65 + reminder/book_complete | Host Router detects Phase=Complete → FollowUp "output summary" | Routing |

---

## Appendix C: Reference Source Code Locations

- `assets/prompts/coordinator.md` — to be simplified
- `internal/host/reminder/flow.go` / `queue_guard.go` / `book_complete.go` — to be deleted
- `internal/host/reminder/subagent_guards.go` — retained
- `internal/host/reminder/stop_guard.go` — retained + adds "must execute Host command" check
- `internal/host/resume.go` — significantly simplified
- `internal/host/observer.go` — new subscription to EventToolExecEnd triggers Router
- `internal/host/flow/` — newly added package
- `internal/tools/commit_chapter.go` L220-280 — CommitResult 17 fields completed
- `internal/tools/save_review.go` L76-116 — verdict upgrades and Flow transitions codified
- `internal/store/outline.go` `CheckArcBoundary` — arc boundary facts API
