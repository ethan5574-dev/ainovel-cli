# Assets Content Map

Before adding "a piece of text / a reference guide / a rule" to the system, search this table to determine its ownership and check the wiring method.

| Directory | What to store | Consumer | Wiring Method |
|---|---|---|---|
| `prompts/` | Worker system prompts (writer / editor / two architects), Arbiter decision prompts, and one-shot task prompts (import / simulation / revision) | `agents/build.go`, `internal/arbiter`, import / simulation / revision runners | `load.go` Prompts fields. Note: simulation_guidance is injected by `load.go`, so it is not visible in the Markdown source |
| `references/` | Genre-independent writing knowledge. Not part of the system prompt; `novel_context` selects it by role/chapter and injects `reference_pack` | writer / editor / architect | **Three wiring points**: add a `tools.References` field, read it in `loadReferences` in `load.go`, and inject it through writerReferences / architectReferences in `novel_context.go`. Adding a file alone does not load it |
| `references/genres/<style>/` | Genre-specific knowledge (style-references / arc-templates) | Same consumers; loaded when `style != default` | `loadReferences` in `load.go` |
| `rules/` | Deprecated built-in rule directory; mechanical defaults now live in code. User rules are natural-language snapshots from `~/.ainovel/rules/*.md` / `./.ainovel/rules/*.md` | `userrules.Service` normalizes them into `meta/user_rules.json`; `novel_context` injects them; `commit_chapter` checks them | Built-in defaults: `SystemDefaults()` in `internal/rules/snapshot.go`. User Markdown requires no special format or YAML |
| `styles/<style>.md` | Genre-specific writing instructions | Included in the **writer** system prompt (`agents/build.go`) | The filename supplies `config.style`. These files and `references/genres/<style>/` express the same genre through instructions and knowledge respectively |

## New Content Ownership Judgment (Five Questions)

1. Must this process be **guaranteed**? → Do not write prompts; write code constraints (`StopAfterTools` / Tool guards / Flow Router).
2. Is this semantic adjudication criteria? → `prompts/arbiter-*.md`; routine dispatch rules belong in `internal/flow/`. The legacy `coordinator.md` is not the current runtime control prompt.
3. Is this a specific role's aesthetic / execution standard? → `prompts/<role>.md`.
4. Is this a mechanically enumerable rule (forbidden words / word counts / thresholds)? → Code defaults in `internal/rules/`, or user preferences in `.ainovel/rules/*.md` (the old assets `rules/` directory is deprecated).
5. Is this writing knowledge material? → `references/` (remember the three-way wiring).

## Consistency Assurance

Envelope paths (`working_memory.*`, etc.) referenced by prompts and `commit_chapter` parameter documents in `writer.md` are mechanically checked by `prompts_consistency_test.go` — these drifts do not throw errors but quietly make the model less capable, exposed by failing tests.

Process descriptions in prompts are the "user manual," while flow truth resides in the code layer; when they diverge, code takes precedence and prompts are revised.
