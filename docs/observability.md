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
| 进度 | `meta/progress.json` | `phase` / `flow` / `completed_chapters` | phase 单调前进，flow 在合法集合内 | phase 倒退 / flow 卡在某状态 |
| 指南针 | `meta/compass.json` | `last_updated` 与最新章节差距 | gap < 15 章 | gap > 15 章（CompassDrift 命中） |
| 配角视图 | `meta/chapter_records/*.json` | `facts.characters` / `facts.cast_intros` | 有名字的次要角色稳定复用 | 同一角色反复换名 |
| 伏笔台账 | `meta/foreshadow.json` | `status="planted"` 的最长停滞章数 | < 章数/3 | > 章数/3（StaleForeshadow 命中） |
| 大纲 | `meta/layered_outline.json` | 当前卷剩余未写章数 | 提前 1-2 章已展开 | 写到当前章但下一章无 outline（OutlineExhausted） |
| 角色档案 | `meta/characters.json` | 是否能在最近 N 章摘要里找到 core/important 角色 | 都能找到 | 缺席（GhostCharacter 命中） |
| 检查点 | `meta/checkpoints.jsonl` | 最近一行的 `step` 是否对应 progress | 一致 | 不一致（崩溃恢复未自愈） |
| 裁定审计 | `meta/decisions.jsonl` | 最近若干条裁定的 facts/decision | 分诊准确、动作合理 | 同类干预反复裁定失败 |

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

## 4. 配角视图观测

配角视图由已完成章节的接纳记录实时投影，不再维护独立的 `cast_ledger.json`。`facts.characters` 决定角色在哪些章节出现，`facts.cast_intros` 提供最早的非空简介；`characters.json` 中的核心角色与别名会自动排除。

跑 5 章后，查看最近的 Writer 会话：

```bash
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

如果第 1 章引入“老周”、第 5 章再次使用，后一次 `novel_context` 返回的 `episodic_memory.recent_cast` 应包含老周及其首次简介。看到了但正文仍换名或定位漂移，属于 Writer 没有消费上下文；视图缺失则检查对应 `meta/chapter_records/*.json` 的 `facts.characters` 与 `facts.cast_intros`。

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

- [ ] CompassDrift 命中且持续 2 个弧未消除
- [ ] 同一角色出现疑似多名（"老李" / "李掌柜" 共存）
- [ ] Writer 写新章时不读 recent_cast 中已有的旧角色（重复发明）
- [ ] Worker session 中出现连续 ≥ 5 次空调 novel_context
- [ ] 任意章节 commit 后 `meta/checkpoints.jsonl` 没有对应 `commit_chapter` step
---

## 7. Document Maintenance Standards

**When adding a new fact-layer artifact (creating a new `meta/*.json` / `meta/*.jsonl`), synchronize:**

1. Add a quick lookup row in §2 of this document.
2. If the artifact needs special observation (not a simple "exists/does not exist" check), add a thematic section §X.
3. If automated diagnostics are desired, load it in `internal/diag/snapshot.go::Load` and add rules in `internal/diag/rules_*.go`.

**Do not:**
- Do not copy all rules from `internal/diag/` to this document (that is a rule reference, not an observation manual).
- Do not write diagnostic rules for every mechanism—thresholds chosen arbitrarily might be wrong; observe first and supplement later.
