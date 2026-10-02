# Observability Guide

When running a long novel, how do we know the mechanisms are actually working?

This document is not a copy of the diag rules. It is written for **real execution**: when you reach chapter N, which file should you open, which field should you inspect, and how do you tell whether things are healthy or not?

---

## 1. General Troubleshooting Flow

```
1. /diag                       # Automatic diagnostics; view the Findings section
2. cd output/{novel}/meta/     # Navigate and directly inspect/cat key artifacts
3. cat meta/sessions/coordinator.jsonl | tail  # View the most recent turns of LLM behavior
```

Facts that `/diag` does not cover, including the "pending diagnostics" listed in this document, need to be checked manually in steps 2-3.

### Reporting issues: redacted diagnostics export

Each `/diag` run also writes `output/{novel}/meta/diag-export.md` - a **redacted** diagnostic report with the novel text, prompts, and thinking removed, leaving only the behavioral skeleton: tool names, error strings, repeat counts, phase/flow, stuck steps, and log error categories. For dead loops or interruption issues, attach this file to a GitHub issue so maintainers can diagnose it without the user's `output/` data.

---

## 2. Key Artifact Cheat Sheet

Ordered by the most common troubleshooting path when something goes wrong:

| Artifact | Path | What to inspect | Healthy | Unhealthy |
|---|---|---|---|---|
| Progress | `meta/progress.json` | `phase` / `flow` / `completed_chapters` | Phase moves forward; flow stays valid | Phase regression / stuck flow |
| Compass | `meta/compass.json` | Gap between `last_updated` and latest chapter | Gap < 15 chapters | Gap > 15 chapters (CompassDrift) |
| Supporting cast | `meta/chapter_records/*.json` | `facts.characters` / `facts.cast_intros` | Named supporting characters are reused consistently | Repeated renaming |
| Foreshadowing | `meta/foreshadow.json` | Longest inactivity for `status="planted"` | < chapter count / 3 | > chapter count / 3 (StaleForeshadow) |
| Outline | `meta/layered_outline.json` | Remaining unwritten chapters in current volume | Expanded 1–2 chapters ahead | Next chapter lacks an outline (OutlineExhausted) |
| Character profiles | `meta/characters.json` | Core/important characters in recent N summaries | All present | Missing (GhostCharacter) |
| Checkpoints | `meta/checkpoints.jsonl` | Latest `step` matches progress | Consistent | Inconsistent (recovery did not heal) |
| Decision audit | `meta/decisions.jsonl` | Recent facts and decisions | Accurate triage and appropriate actions | Repeated failures for similar interventions |

---

## 3. Compass Observability

**Fix time**: 2026-05-08 (commit `fix: update_compass tool automatically fills last_updated`)

### What to inspect

```bash
cat output/{novel}/meta/compass.json
```

Field semantics:
- `ending_direction`: Ultimate ending direction (should align with `premise.md` "ending direction" section).
- `open_threads`: Active long lines (added/removed at volume boundaries by architect).
- `estimated_scale`: Estimated scale (e.g., "4-6 volumes", updated at volume boundaries).
- `last_updated`: **Automatically filled by the tool** as the maximum completed chapter number when updated (no longer relies on LLM self-filling).

### Health checks

| Signal | Judgment |
|---|---|
| `last_updated` in `[latest-15, latest]` range | Healthy |
| `last_updated` lags behind latest by > 15 chapters | Architect failed to update at arc/volume boundaries—check architect-long.md prompt |
| `last_updated == 0` | **Dirty data before this fix**, next update_compass will self-heal |
| `ending_direction` mismatch with premise.md "ending direction" | Architect modified user intent behind the scenes—record it, decide whether to freeze the field (design issue, see todo.md) |

### How to verify the fix

Compare before and after running long-form:
- **Before fix**: After running 30+ chapters, `compass.last_updated` is highly likely to be `0` or some early chapter number.
- **After fix**: Every time the architect calls `update_compass`, `last_updated` is overwritten by the tool layer with the current latest.

---

## 4. Observing the Supporting Cast

The supporting-cast view is projected from accepted records of completed chapters, without a separate `cast_ledger.json`. `facts.characters` determines appearances; `facts.cast_intros` supplies the earliest non-empty introduction. Core characters and aliases in `characters.json` are excluded automatically.

After writing five chapters, inspect the latest Writer session:

```bash
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

If chapter 1 introduces Old Zhou and chapter 5 reuses him, the later `novel_context` result should contain his name and first introduction in `episodic_memory.recent_cast`. If the data is present but the prose renames or repositions him, Writer failed to consume context. If the view is missing, inspect the chapter record's `facts.characters` and `facts.cast_intros`.

---

## 5. Is the Writer Working as Expected?

When running long novels, we care most about **whether the Writer is acting on the prompt**. The most direct observation is the session log:

```bash
ls output/{novel}/meta/sessions/agents/    # One jsonl per sub-agent
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

Look for specific behaviors:

| Expected Behavior | Manifested in jsonl |
|---|---|
| Writer reads recent_cast | `episodic_memory.recent_cast` field in the return value of `novel_context` is non-empty |
| Writer fills cast_intros in commit_chapter | `cast_intros` array parameter in tool_call is non-empty (only in chapters introducing new characters) |
| Writer uses related chapter recommendation | Number of `read_chapter` calls > 1 (default is 1; more indicates checking back) |
| Writer does not violate tool sequence | tool_call sequence strictly follows `novel_context → read_chapter → plan_chapter → draft_chapter → check_consistency → commit_chapter` |

If the jsonl shows Writer repeatedly calling `novel_context` empty or calling other tools after `commit_chapter` — the prompt did not lock down.

---

## 6. Long Run Red Lines

When running a 100+ chapter novel, if any of the following items are hit, stop and investigate:

- [ ] CompassDrift persists for two arcs
- [ ] A character appears under suspected duplicate names (e.g. Old Li / Shopkeeper Li)
- [ ] Writer reinvents existing characters instead of using recent_cast
- [ ] Worker session contains at least five consecutive empty novel_context calls
- [ ] A chapter commit lacks its corresponding `commit_chapter` checkpoint
---

## 7. Document Maintenance Standards

**When adding a new fact-layer artifact (creating a new `meta/*.json` / `meta/*.jsonl`), synchronize:**

1. Add a quick lookup row in §2 of this document.
2. If the artifact needs special observation (not a simple "exists/does not exist" check), add a thematic section §X.
3. If automated diagnostics are desired, load it in `internal/diag/snapshot.go::Load` and add rules in `internal/diag/rules_*.go`.

**Do not:**
- Do not copy all rules from `internal/diag/` to this document (that is a rule reference, not an observation manual).
- Do not write diagnostic rules for every mechanism—thresholds chosen arbitrarily might be wrong; observe first and supplement later.
