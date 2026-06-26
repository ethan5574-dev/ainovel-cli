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
| Progress | `meta/progress.json` | `phase` / `flow` / `completed_chapters` | `phase` advances monotonically; `flow` is in the legal set | `phase` regresses / `flow` is stuck in a state |
| Compass | `meta/compass.json` | Gap between `last_updated` and latest chapter | gap < 15 chapters | gap > 15 chapters (CompassDrift hit) |
| Cast Ledger | `meta/cast_ledger.json` | Item count / brief_role fill rate / name consistency | See §4 | See §4 |
| Foreshadow | `meta/foreshadow.json` | Longest stagnated chapters of `status="planted"` | < total_chapters/3 | > total_chapters/3 (StaleForeshadow hit) |
| Outline | `meta/layered_outline.json` | Remaining unwritten chapters in the current volume | Expanded 1-2 chapters in advance | Current chapter is reached but next has no outline (OutlineExhausted) |
| Characters | `meta/characters.json` | Whether core/important characters are in recent N chapter summaries | All can be found | Absent (GhostCharacter hit) |
| Checkpoint | `meta/checkpoints.jsonl` | Whether the last line's `step` corresponds to progress | Consistent | Inconsistent (crash recovery failed to self-heal) |
| Coordinator Session | `meta/sessions/coordinator.jsonl` | tool_call pattern of recent 5-10 turns | Single turn fast progression | Same tool called empty multiple times (infinite loop) |

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

## 4. Cast Ledger Observability

**Feature Landed**: 2026-05-08 (commit `feat: cast ledger automatically tracks minor characters`)

### What to inspect

```bash
cat output/{novel}/meta/cast_ledger.json | jq 'length'                     # Total entries
cat output/{novel}/meta/cast_ledger.json | jq '[.[] | select(.brief_role == "" or .brief_role == null)] | length'  # Missing brief_role count
cat output/{novel}/meta/cast_ledger.json | jq '[.[] | select(.appearance_count >= 3)] | length'   # High frequency appearances (≥3 times) count
cat output/{novel}/meta/cast_ledger.json | jq 'sort_by(-.appearance_count) | .[:10]'  # Top 10 most frequent characters
```

### Health checks

| Dimension | Healthy | Abnormal | Action |
|---|---|---|---|
| **Entries vs Completed Chapters** | ledger entries ≈ completed_chapters × 0.3-0.6 | > chapters × 0.8 (passing/trivial characters incorrectly registered) | Check if the `cast_intros` section in `writer.md` is clear enough |
| **brief_role fill rate** | missing < 30% | missing > 50% | Writer missed filling severely—insufficient prompt guidance |
| **Name similarity** | No suspected multiple names for same person | "Li X", "Old Li", "X Shopkeeper" co-existing | LLM name drift—add constraint "use consistent names" in prompt or add user steer merge tool |
| **Frequent appearance characters** | Few entries with `appearance_count >= 5` | Large number of entries appearing high-frequency across arcs | Consider promoting to core character profiles (Phase 3 promotion path) |
| **Recall consumed** | When Writer writes about an old character, the characters field in commit_chapter contains ledger's existing name | Writer reinvents the same name (e.g. "Old Zhou A" and "Old Zhou B" appear) | recent_cast recall not consumed—check "minor character continuity" section in `writer.md` |

### Data Flow Verification (End-to-End)

After running 5 chapters:
1. `cat meta/cast_ledger.json` should not be empty (unless only core characters are used in every chapter).
2. If Writer introduces "Old Zhou" in Chapter 1:
   - There should be an `Old Zhou` entry in `cast_ledger`, with `appearance_count=1`.
3. If Old Zhou is written again in Chapter 5:
   - `Old Zhou.appearance_count=2`, `last_seen_chapter=5`.
4. In the `novel_context` return value of Chapter 5 in `meta/sessions/agents/writer-*.jsonl`, Old Zhou should be visible under `episodic_memory.recent_cast`.
5. If seen in the previous step but not consumed by Writer (the written Old Zhou does not match Chapter 1) — this is a prompt issue.

### Currently No Automated Diagnosis (but snapshot loaded)

`diag.Snapshot.CastLedger` is already loaded in `Load()` and can be consumed by rules directly—but no rules have been written yet. Verification still relies on manual checking via the `jq` command above.

Future diagnostic rules (candidates):
- `CastBriefRoleMissing`: Alarm on missing rate > 50%
- `CastBloat`: Alarm on entries > chapters × 0.8
- `CastPromotionCandidate`: Suggest promotion when appearance_count ≥ 5 and spans multiple arcs

Do not set thresholds now—wait for long-form data to see the real distribution first. Rule code itself only requires 30-50 lines.

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

- [ ] `CompassDrift` hit and persists for 2 arcs without clearing
- [ ] `cast_ledger` entries count > completed chapters × 0.8
- [ ] `cast_ledger` brief_role fill rate < 30%
- [ ] Suspected multiple names for the same character ("Old Li" / "Shopkeeper Li" co-existing)
- [ ] Writer does not read existing old characters from `recent_cast` when writing new chapters (reinvention)
- [ ] Coordinator session shows consecutive ≥ 5 empty `novel_context` calls
- [ ] `meta/checkpoints.jsonl` lacks the corresponding `commit_chapter` step after any chapter commit

The first 4 items are health indicators of the new mechanisms; the last 3 items are the stability of existing mechanisms.

---

## 7. Document Maintenance Standards

**When adding a new fact-layer artifact (creating a new `meta/*.json` / `meta/*.jsonl`), synchronize:**

1. Add a quick lookup row in §2 of this document.
2. If the artifact needs special observation (not a simple "exists/does not exist" check), add a thematic section §X.
3. If automated diagnostics are desired, load it in `internal/diag/snapshot.go::Load` and add rules in `internal/diag/rules_*.go`.

**Do not:**
- Do not copy all rules from `internal/diag/` to this document (that is a rule reference, not an observation manual).
- Do not write diagnostic rules for every mechanism—thresholds chosen arbitrarily might be wrong; observe first and supplement later.
