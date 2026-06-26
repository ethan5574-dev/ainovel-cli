# Assets Content Map

Before adding "a piece of text / a reference guide / a rule" to the system, search this table to determine its ownership and check the wiring method.

| Directory | What to store | Consumer | Wiring Method |
|---|---|---|---|
| `prompts/` | Permanent role system prompts (`coordinator` / `writer` / `editor` / `architect` × 2) and one-off task prompts (`import` × 2 / `simulation` × 2) | `agents/build.go` assembly; `imp` / `sim` runner | `load.go` Prompts fields. Note: `simulation_guidance` is injected during load by `load.go` and is not visible in md files. |
| `references/` | Genre-independent writing knowledge materials. Do not enter system prompts; instead, injected into `reference_pack` by `novel_context` cropped by role / chapter. | writer / editor / architect | **Three-way wiring**: add fields in `tools.References` + read via `load.go` `loadReferences` + inject in `novel_context.go` `writerReferences` / `architectReferences`. Dropping it in the folder will not load it automatically. |
| `references/genres/<style>/` | Genre-specific knowledge (`style-references` / `arc-templates`) | Same as above, loaded when `style != default` | `load.go` `loadReferences` |
| `rules/` | Default mechanical rules (word count / forbidden words / fatigue words), checked by code during commit. | Rules loader three-level merge: built-in → `~/.ainovel/rules/` → project `./.ainovel/rules/` | `rules/default.md`; user-level format is shown in `rules.md.example` at root. Only store fixed-length strings; patterns with variables should be left to editor semantic judgment. |
| `styles/<style>.md` | Genre writing style instructions | Appended to **writer's** system prompt (`agents/build.go`) | Suffix of the filename is the value of `config.style`. Together with `references/genres/<style>/`, they represent two carriers of the same genre concept: the former is style instructions, the latter is knowledge materials. |

## New Content Ownership Judgment (Five Questions)

1. Must this process be **guaranteed**? → Do not write prompts; write code constraints (`StopAfterTools` / Tool guards / Flow Router).
2. Is this adjudication criteria (when to dispatch who)? → `prompts/coordinator.md`.
3. Is this a specific role's aesthetic / execution standard? → `prompts/<role>.md`.
4. Is this a mechanically enumerable rule (forbidden words / word counts / thresholds)? → `rules/` (code-enforced, zero LLM cost).
5. Is this writing knowledge material? → `references/` (remember the three-way wiring).

## Consistency Assurance

Envelope paths (`working_memory.*`, etc.) referenced by prompts and `commit_chapter` parameter documents in `writer.md` are mechanically checked by `prompts_consistency_test.go` — these drifts do not throw errors but quietly make the model less capable, exposed by failing tests.

Process descriptions in prompts are the "user manual," while flow truth resides in the code layer; when they diverge, code takes precedence and prompts are revised.
