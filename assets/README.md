# Assets Content Map

Before adding "a piece of text / a reference guide / a rule" to the system, search this table to determine its ownership and check the wiring method.

| Directory | What to store | Consumer | Wiring Method |
|---|---|---|---|
| `prompts/` | Worker system prompt（writer / editor / architect×2）、Arbiter 裁定 prompt 与一次性任务 prompt（import / simulation / revision） | `agents/build.go`、`internal/arbiter`、imp / sim / revision runner | `load.go` Prompts 字段。注意：simulation_guidance 由 `load.go` 加载时注入，md 文件里看不到 |
| `references/` | 题材无关的写作知识材料。不进 system prompt，由 novel_context 按角色 / 章节裁剪后注入 `reference_pack` | writer / editor / architect | **三处接线**：`tools.References` 加字段 + `load.go` loadReferences 读取 + `novel_context.go` writerReferences / architectReferences 注入。放进目录不会自动加载 |
| `references/genres/<style>/` | 题材专属知识（style-references / arc-templates） | 同上，`style != default` 时加载 | `load.go` loadReferences |
| `rules/` | 已废弃的旧内置规则目录；机械基线已迁到代码，用户规则来自 `~/.ainovel/rules/*.md` / `./.ainovel/rules/*.md` 的自然语言快照 | `userrules.Service` 归一化为 `meta/user_rules.json`；`novel_context` 注入；`commit_chapter` 检查 | 内置基线见 `internal/rules/snapshot.go` 的 `SystemDefaults()`；用户 `.md` 零格式、零 YAML，按自然语言归一化 |
| `styles/<style>.md` | 题材写作风格指令 | 拼进 **writer** 的 system prompt（`agents/build.go`） | 文件名即 `config.style` 取值。与 `references/genres/<style>/` 是同一题材概念的两种载体：前者是风格指令，后者是知识材料 |

## New Content Ownership Judgment (Five Questions)

1. Must this process be **guaranteed**? → Do not write prompts; write code constraints (`StopAfterTools` / Tool guards / Flow Router).
2. Is this adjudication criteria (when to dispatch who)? → `prompts/coordinator.md`.
3. Is this a specific role's aesthetic / execution standard? → `prompts/<role>.md`.
4. Is this a mechanically enumerable rule (forbidden words / word counts / thresholds)? → `rules/` (code-enforced, zero LLM cost).
5. Is this writing knowledge material? → `references/` (remember the three-way wiring).

## Consistency Assurance

Envelope paths (`working_memory.*`, etc.) referenced by prompts and `commit_chapter` parameter documents in `writer.md` are mechanically checked by `prompts_consistency_test.go` — these drifts do not throw errors but quietly make the model less capable, exposed by failing tests.

Process descriptions in prompts are the "user manual," while flow truth resides in the code layer; when they diverge, code takes precedence and prompts are revised.
