# ainovel-cli

An end-to-end AI engine for writing long-form novels. The Coordinator drives the Architect, Writer, and Editor subagents within a single prompt loop to complete an entire book, while the Host only handles startup, recovery, and observation. From a single-line idea to a finished novel, no manual intervention is required.

<p align="center">
  <img src="scripts/sample.gif" alt="ainovel-cli demo" width="800">
  <img src="scripts/novel.png" alt="ainovel-cli bg" width="800">
</p>

## Features

- **Multi-agent collaboration** - The Coordinator orchestrates Architect / Writer / Editor in a long-running loop and makes the creative decisions.
- **LLM-driven long loop** - One prompt can write an entire book, with the Host staying out of scheduling. Simpler is more stable.
- **Step-level recovery** - Every successful tool call writes a checkpoint, enabling recovery at the plan / draft / check / commit step level after a crash.
- **Two-layer rolling planning** - Long novels are not planned all at once. Initially we only plan the skeleton of the first 2 volumes/arcs plus the detailed chapters of the first arc; later arcs and volumes are expanded by the Architect when needed, using prior summaries and character state so the distant plan stays grounded.
- **Smart related-chapter suggestions** - Each chapter can automatically recommend related historical chapters based on foreshadowing, character appearances, state changes, and relationships, keeping 500+ chapter stories coherent.
- **Adaptive context strategy** - Automatically switches between full, sliding-window, and layered summaries based on total chapter count.
- **Seven-dimensional quality review** - The Editor reviews consistency, character behavior, pacing, narrative coherence, foreshadowing, hooks, and aesthetic quality, with the aesthetic dimension split into descriptive quality, narrative technique, dialogue distinction, diction, and emotional impact.
- **Real-time user intervention** - You can inject edits directly while writing without pausing; the system evaluates scope and rewrites affected chapters automatically.
- **Unified TUI entry point** - Watch progress live in the interactive UI, or start from a single prompt.
- **Multi-LLM support** - Switch freely among OpenRouter / Anthropic / Gemini / OpenAI and more.

## Architecture

Core design: **LLM-driven, Host-served**. The Coordinator independently decides the creative flow for the whole book within a single run; the Host only handles startup, recovery, and event observation.

```
┌─────────────────────────────────────────────────┐
│                  Host (thin shell)                │
│             Startup / recovery / observe / inject │
└──────────────────────┬──────────────────────────┘
                       │ One prompt
┌──────────────────────▼──────────────────────────┐
│            Coordinator (LLM long loop)             │
│    Read novel_context → call subagent → continue  │
└────┬──────────┬──────────┬──────────────────────┘
     │          │          │
 ┌───▼────┐ ┌───▼───┐ ┌────▼────┐
 │Architect│ │Writer │ │ Editor  │
 └───┬────┘ └───┬───┘ └────┬────┘
     └──────────┼──────────┘
                │ Tool calls (IO + checkpoint)
┌───────────────▼─────────────────────────────────┐
│                   Store                         │
│  Progress / Checkpoint / Outline / Drafts / ... │
└─────────────────────────────────────────────────┘
```

- **Host** - Starts the Coordinator, handles crash recovery, and projects events into the TUI. It makes no scheduling decisions.
- **Coordinator** - The sole decision-maker, driving the full planning → writing → review → summary flow in one run.
- **SubAgents** - Architect / Writer / Editor each have isolated context and collaborate through artifacts in the Store.
- **Tools** - Atomic IO plus checkpoint writes; they return facts in JSON and never carry instructions.

### Agent Roles

| Agent | Responsibility | Tools |
|--------|------|------|
| **Coordinator** | Orchestrates the whole system and handles review decisions and user intervention | `subagent` `novel_context` |
| **Architect** | Creates the premise, outline, character sheets, and world rules | `novel_context` `save_foundation` |
| **Writer** | Independently plans, writes, self-checks, and submits a chapter | `novel_context` `read_chapter` `plan_chapter` `draft_chapter` `check_consistency` `commit_chapter` |
| **Editor** | Reads the source text and reviews both structure and style | `novel_context` `read_chapter` `save_review` `save_arc_summary` `save_volume_summary` |

### Writing Process

```
User Requirements → Architect plans skeleton + first arc chapters → Writer writes chapter-by-chapter → Editor reviews at arc level
                                                                          ↑                                │
                                                                          ├── Rewrite/Polish ◄─────────────┘
                                                                          │
                                                                   Architect expands next arc/volume
                                                                   (Refers to prior summaries + character snapshot)
```

The Writer completes each chapter in a fixed sequence (the writing content is fully autonomous, and the tool calling sequence is strict):

1. `novel_context` — Load context (previous summaries, foreshadowing, character state, style rules, related chapter recommendations)
2. `read_chapter` — Read previous chapters to regain tone and pacing
3. `plan_chapter` — Conceive the chapter goal, conflict, and emotional arc
4. `draft_chapter` — Write the chapter's draft text
5. `check_consistency` — Check consistency against state data (must run after drafting)
6. `commit_chapter` — Commit the final draft and return fact fields (`arc_end_reached` / `next_chapter` etc.); the next step is driven by the Reminder

### State Transition Rules

The system internally splits the execution state into two layers:

- **Phase** — Major phase, indicating whether the novel is currently in the setup, outlining, writing, or completed phase.
- **Flow** — Current active flow, indicating whether the system is currently writing, reviewing, rewriting, polishing, or processing user steering.

#### Phase

`Phase` transitions follow a "forward-only, no-regression" rule:

```text
init -> premise -> outline -> writing -> complete
  \-------> outline ------^
  \--------------> writing
```

Meanings:

- `init` — Task created, stable configuration not yet formed.
- `premise` — Story premise saved.
- `outline` — Outline saved, ready to enter formal writing.
- `writing` — Chapter writing phase entered.
- `complete` — Book process finished.

Rules:

- Self-transitions are allowed, e.g., `writing -> writing`.
- Forward transitions are allowed, e.g., `outline -> writing`.
- Backward transitions are not allowed, e.g., `writing -> premise`, `complete -> writing`.

#### Flow

`Flow` only describes active workflows during the writing phase, allowing transitions between several workflows:

```text
writing   -> reviewing / rewriting / polishing / steering / writing
reviewing -> writing / rewriting / polishing / steering / reviewing
rewriting -> writing / steering / rewriting
polishing -> writing / steering / polishing
steering  -> writing / reviewing / rewriting / polishing / steering
```

Meanings:

- `writing` — Advancing the next chapter normally.
- `reviewing` — Editor is reviewing.
- `rewriting` — Processing chapters that must be rewritten.
- `polishing` — Processing chapters that only need polishing.
- `steering` — Evaluating and processing user steering.

Rules:

- Allows `writing -> reviewing`, e.g., triggered after chapter submission.
- Allows `reviewing -> rewriting/polishing/writing`, determined by review results.
- Allows `steering -> writing/reviewing/rewriting/polishing`, determined by the steering impact scope.
- Does not allow abnormal jumps, e.g., `rewriting -> reviewing`.

These rules are now uniformly enforced by lightweight checks in the codebase, preventing state regression or transitions to invalid process branches.

### Long-form Rolling Planning

Traditional solutions plan all chapters at once, which makes the outline hollow and the pacing feel rushed when reaching 300+ chapters. This system uses a **Compass + Rolling Horizon planning** approach, simulating the actual creation process of web novel authors:

```
Initial Planning              At Arc End                    At Volume End
┌────────────────────────┐    ┌────────────────────────┐    ┌────────────────────────┐
│ Endgame (Compass)      │    │ Editor Arc Review      │    │ Editor Vol Review      │
│ Start 2 Vols, more     │    │ Arc Summary + Snapshot │    │ Vol Summary            │
│ detailed 1st arc       │ →  │ Architect expands arc  │ →  │ Architect creates next │
│ Characters + Worldview │    │ Writer writes chapter  │    │ volume, updates compass│
└────────────────────────┘    └────────────────────────┘    └────────────────────────┘
```

- **Compass** — Endgame direction + active plotlines + size estimation, updated by the Architect at each volume boundary; the story direction can evolve as writing progresses.
- **On-demand Generation** — Once the current volume is finished, the Architect autonomously creates the next volume based on what has been written. The initial plan generates 2 volumes as a start, and subsequent volumes are generated on demand.
- **Skeleton Arc** — Contains only a goal + estimated chapter count, expanded into detailed chapters only when reached.
- **Progressive Refinement** — Each expansion refers to prior summaries, character snapshots, and style rules; the further we write, the more precise it becomes.
- **Common Pacing Templates** — Growth & Breakthrough Arc / Competition & Confrontation Arc / Exploration & Discovery Arc / Conflict & Feud Arc / Daily Transition Arc; each arc type has a reference density and genre mapping.

### Long-form Context Management

A 500+ chapter novel uses a 3-level summary + 4-level compression pipeline + smart recommendation system:

```
Volume → Volume Summary
└── Arc → Arc Summary + Character Snapshot + Style Rules
    └── Chapter → Chapter Summary (sliding window of the latest 3 chapters)
```

- **Layered Summaries** — Chapter summaries for short range, arc summaries for medium range, and volume summaries for long range. Layered compression ensures no information loss.
- **Related Chapter Recommendation** — During writing, the system checks historical chapters across four dimensions (foreshadowing, character appearance, state change, relations) and recommends chapters to the Writer for reading as needed.
- **Next Chapter Preview** — Loads the outline of the next chapter, helping the Writer design end-of-chapter hooks and coordinate foreshadowing.
- **Arc Boundary Detection** — Automatically detects the end of an arc/volume, triggering reviews, summary generation, and next arc/volume expansion.

#### Context Compression Pipeline

When the dialogue history exceeds the model's context window, it is compressed step-by-step from lowest to highest cost:

```
ToolResultMicrocompact → LightTrim → StoreSummaryCompact → FullSummary
   Clean old tools       Truncate text   Zero-LLM compression   LLM summary fallback
```

- **StoreSummaryCompact** — Exclusive to the Writer; directly replaces old messages using existing chapter summaries, character snapshots, and foreshadowing ledgers in the store, incurring zero LLM overhead.
- **Narrative-focused FullSummary** — Writer uses a summary prompt oriented towards narrative continuity, explicitly asking to preserve character states, foreshadowing clues, pending review fixes, and style anchors.
- **Post-compression Recovery Pack** — Automatically injects the current chapter plan, outline, and character snapshots after a FullSummary, preventing the Writer from having 'amnesia' after compression.
- **Circuit Breaker** — Automatically skips compression and raises an explicit alert after consecutive failures, using a half-open mode to retry in the next turn.
- **CJK Token Estimation** — Estimates Chinese tokens as `runes × 1.5`, avoiding delayed compression triggers caused by `bytes/4` underestimation.
- **TUI Health Indicator** — Context usage is displayed in real-time with color gradients: Green (<70%), Yellow (70-85%), and Red (>85%).

## Quick Start

```bash
# One-click installation (macOS / Linux, no Go required)
curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/main/scripts/install.sh | sh

# Install a specific version
curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/main/scripts/install.sh | sh -s -- v1.2.3

# Or install via Go
go install github.com/voocel/ainovel-cli/cmd/ainovel-cli@latest

# View version / Update to latest version
ainovel-cli --version
ainovel-cli update

# First run, automatically enters setup wizard (Select Provider → Enter API Key → Base URL → Model Name)
ainovel-cli
```

> Windows or manual installation: Go to [Releases](https://github.com/voocel/ainovel-cli/releases/latest) to download the package for your platform.

### Docker

Docker images are suitable for running headless long tasks on servers/NAS, or you can use `-it` to enter the TUI. We recommend mounting the configuration and workspace directories to the host:

```bash
mkdir -p config workspace

# TUI
docker run --rm -it \
  -v "$PWD/config:/root/.ainovel" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/voocel/ainovel-cli:latest

# Headless
docker run --rm \
  -v "$PWD/config:/root/.ainovel" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/voocel/ainovel-cli:latest \
  --headless --prompt "Write a long Eastern Fantasy novel where the protagonist starts from a remote border town"
```

Or use Compose:

```bash
docker compose run --rm ainovel
docker compose run --rm ainovel --headless --prompt "Write a short mystery novel"
```

After entering the TUI, two initial interaction modes are supported at startup:

- `Quick Start`: Start writing immediately with a single prompt.
- `Co-creation`: Clarify requirements through multi-turn dialogue with AI. The **draft writing instructions are synchronized in real-time on the right side**. The AI proactively suggests 1-3 guides in each turn, which can be quickly filled by pressing number keys. Press `Ctrl+S` to start formal writing.

Both modes will eventually converge into the same set of writing instructions, which are then passed to the same writing engine.

### Managing Multiple Novels

Each novel is bound to the directory where the command is launched, and output artifacts are stored in `{cwd}/output/novel/`. Starting in a different directory = starting a new novel; returning (`cd`) and starting again = automatically resuming from the last checkpoint. Configuration in `~/.ainovel/config.json` is shared globally and does not need to be copied.

### Configuration File

A configuration file `~/.ainovel/config.json` is automatically generated on the first run. You can edit this file directly to adjust settings later. If you delete the configuration file, running the command will trigger the setup wizard again.

You can also create the configuration file manually, referencing `~/.ainovel/config.example.jsonc` (automatically generated during setup).

```jsonc
{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-xxx",
      "base_url": "https://openrouter.ai/api/v1",
      "models": ["google/gemini-2.5-flash", "google/gemini-2.5-pro"],
      "extra": {
        "user_agent": "my-client/1.0",
        "headers": { "X-Custom-Client": "my-client" }
      }
    }
  },
  "style": "default"
}
```

#### 配置文件查找顺序（后者覆盖前者）

1. `~/.ainovel/config.json` — 全局配置
2. `./.ainovel/config.json` — 项目级覆盖（可选）
3. `--config path/to/config.json` — 命令行指定

> 项目级 `.ainovel/` 是全局 `~/.ainovel/` 的镜像：同样的结构、只是根目录从家目录换成当前项目。配置放 `./.ainovel/config.json`，写作规则放 `./.ainovel/rules/*.md`（详见下文「去 AI 味与自定义规则」）。该目录含密钥，已默认加入 `.gitignore`。

覆盖规则说明：

- 标量字段按后者覆盖前者，例如 `provider`、`model`、`style`
- `providers` 和 `roles` 按 key 合并，同名项内部按字段覆盖
- 未填写的字段会继承上层配置，例如项目级配置只写 `base_url` 时会保留全局配置中的 `api_key`
- 当前不支持用空字符串显式清空上层已有值；如需清空，请直接编辑更高优先级的配置文件

> ⚠️ `provider`（以及 `roles.*.provider`）的值是 `providers` 里的 **key 名**——一根指针，不是协议名。项目级若把 `provider` 切到一个全局 `providers` 里不存在的账号，必须在项目级同时补上该账号的凭证（`api_key` / `base_url`），否则启动会报“未配置凭证”。

`providers.<name>.models` 为可选字段，用于声明该 provider 下允许在 TUI `/model` 面板中切换的模型列表；如果未配置，系统会回退为当前配置文件里已经出现过的该 provider 模型。

`providers.<name>.extra` 为 provider 级配置，会传给底层 HTTP 客户端，适合配置 `user_agent`、`headers`、`anthropic_beta` 等代理识别字段；`providers.<name>.extra_body` 才是请求体扩展参数，两者不要混用。

## Diagnostics Report

Type `/diag` in the TUI to run diagnostic analysis on the output artifacts of the current novel, producing actionable findings and improvement suggestions.

Diagnostics cover four dimensions:

- **Process** — Rewrite loops/stalls, unconsumed steering instructions, abnormal Phase/Flow states, and skipped chapter numbers.
- **Quality** — Persistent low review scores across dimensions, contract compliance rate, rewrite rate, and anomalous chapter word counts.
- **Planning** — Stagnant foreshadowing, outdated compass, exhausted outline, and missing summaries.
- **Context** — Missing characters, timeline gaps, and stagnant relationship data.

Each finding includes: problem description, data evidence, and improvement suggestions (pointing to specific prompts/flows/configs).

`/diag` also writes a **desensitized** `meta/diag-export.md` file (stripping the actual novel text while preserving tool calls, error strings, retry counts, and other behavioral skeletons). If you encounter infinite loops or interruption issues, paste it into a GitHub issue to help maintainers debug without needing your local data.

## Imitation Profiling

Put the reference articles into the `simulate/` folder of the current launch directory, then enter `/simulate` in the TUI. The system recursively reads `.txt`, `.md`, and `.markdown` files, analyzes the corpus using the architect model, and writes to:

```text
output/novel/meta/simulation_profile.json
```

Running `/simulate` again will skip unchanged files based on `relative_path + sha256`. If there are no additions or modifications, it will prompt "Profile is already up-to-date" and will not call the LLM. If there is an existing profile and new or modified articles appear in `simulate/`, the system will synthesize them on top of the original profile.

You can also import previously generated profiles to avoid re-analyzing the same set of articles:

```text
/simulate
/importsim ./profile.json
```

`/importsim` only accepts the `simulation_profile.v1` JSON format generated by this tool, merging by corpus fingerprint and skipping duplicate sources. Only import profile files from trusted sources; the imported content serves as a contextual reference for subsequent Agents. The profile is injected in a compact format into the `novel_context`, readable by Coordinator, Architect, Writer, and Editor. Agents only learn structure, pacing, hooks, and reader-attraction techniques, without copying the original phrasing or specific settings.

## Import

Type `/import <file_path>` in the TUI to reverse-import an existing novel: it splits the text by chapter, uses the LLM to reverse-engineer the premise, characters, worldview, hierarchical outline, and compass, and writes each chapter to disk. The original text forms the first volume of a serializable project. Once imported, it **automatically transitions to continuation writing**—the Coordinator reviews and summarizes the end of Volume 1, appends a new volume, and continues writing from the next chapter.

```
/import ~/my_novel.txt             # Import from scratch and reverse-engineer foundation
/import ~/my_novel.txt from=50     # Continue importing from chapter 50 (skips foundation reverse-engineering)
```

**Chapter Splitting Rules**: Automatically recognizes the following title formats (at the start of a line, optionally with `#`/`##` Markdown prefixes, enclosed in brackets like `【】`/`〖〗`, full-width spaces, and compatible with GBK/BOM encoding):

- Chinese numbering: `第一章` (Chapter 1), `第3回` (Round 3), `第十话` (Episode 10), `第二卷` (Volume 2), `第五节` (Section 5), `第二幕` (Act 2), independent `卷一` (Volume 1); numbers can be in uppercase (`第壹章`), and can include subtitles (`第三章：决战`)
- Chinese special units: `序章` (Prologue), `楔子` (Wedge/Intro), `引子` (Introduction), `前言` (Foreword), `尾声` (Epilogue), `终章` (Final Chapter), `后记` (Afterword), `番外` (Side Story), `外传` (Spin-off)
- English: `Chapter 1`, `Chapter II`, `Prologue`, `Epilogue`, with optional subtitles (e.g., `Chapter 1: The Beginning`)

If prompted with **"No chapters recognized"**, verify that the file is indeed a formatted novel text (with chapter titles occupying their own line at the beginning of the line).

> Import is a deterministic replay and does not go through the Coordinator; the original text is saved verbatim as completed chapters, making it ideal for "continuing the same book". If you only want to borrow the setup for a new creation, start a new book in the normal way and describe the style and settings in the requirements.

## Export

Enter `/export` in the TUI to merge and export completed chapters. The default format is TXT, written to `{novelDir}/{NovelName}.txt`. Export is a read-only operation; you can export the current draft at any time during writing without affecting the Coordinator's run.

The format is determined by the **output file extension** (`.txt` / `.epub`):

```text
/export                            # Default TXT, {novelDir}/{NovelName}.txt
/export ~/my_novel.txt             # Extension .txt → TXT
/export ~/my_novel.epub            # Extension .epub → EPUB (readable by Apple Books, WeChat Read, Kindle converter, etc.)
/export from=10 to=30 --overwrite  # Chapter range + overwrite
/export from=10 ~/x.epub --overwrite
```

- **TXT** — `《Book Title》` → Volume dividers → Chapter text (hierarchical mode automatically adds volume dividers). Two kinds of internal data **will not be exported**: the premise (creative blueprint containing target readers/writing taboos, meant only for the author and the engine) and arc dividers (since arcs are too granular for the reader's view). The exporter uniformly generates "Chapter N Title" and strips duplicate headers (e.g. `# Chapter N...` or `# Chapter Title`) produced by the writer in the body text.
- **EPUB** — EPUB 3 standard container, including cover page, TOC, and XHTML files split by chapter. The identifier is stably derived from the content (re-exporting the same book will be recognized as an update by reader apps). Cover image is not included by default.

Unfinished chapters within the range will be skipped and listed in the results, which is not treated as an error.

#### Assigning Different Models to Roles

Assign different models to different agents using the `roles` field. Roles not configured will fallback to the default model:

```jsonc
{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": {
    "openrouter": { "api_key": "sk-or-v1-xxx", "base_url": "https://openrouter.ai/api/v1" },
    "anthropic": { "api_key": "sk-ant-xxx" }
  },
  "roles": {
    "writer": { "provider": "anthropic", "model": "claude-sonnet-4" },
    "architect": { "provider": "openrouter", "model": "google/gemini-2.5-pro" }
  }
}
```

Configurable roles: `coordinator` / `architect` / `writer` / `editor`

#### Custom Proxy

Select any Provider and fill in the proxy address, or use Custom Proxy and specify the API protocol type. The `api_key` for custom proxies is optional; if your proxy does not require authentication, it can be omitted:

```jsonc
{
  "provider": "my-proxy",
  "model": "gpt-4o",
  "providers": {
    "my-proxy": {
      "type": "openai",
      "base_url": "https://proxy.example.com/v1",
      "extra": {
        "user_agent": "my-client/1.0",
        "headers": { "X-Custom-Client": "my-client" }
      }
    }
  }
}
```

Supported Providers: `openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` / `ollama` / `bedrock`, and any custom proxy.

If the proxy uses the Anthropic protocol and requires client identification fields, `type` should be set to `anthropic`, `anthropic_beta` should be placed at the top level of `extra`, and HTTP headers like Stainless should be placed under `extra.headers`:

```jsonc
{
  "provider": "claude-proxy",
  "model": "claude-sonnet-4-6",
  "providers": {
    "claude-proxy": {
      "type": "anthropic",
      "api_key": "sk-xxx",
      "base_url": "https://proxy.example.com",
      "extra": {
        "user_agent": "claude-code/2.1.183",
        "anthropic_beta": "claude-code-20250219",
        "headers": {
          "X-Stainless-Lang": "js",
          "X-Stainless-Package-Version": "0.94.0",
          "X-Stainless-Runtime": "node"
        }
      }
    }
  }
}
```

About `api_key`:

- Managed services like `openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` typically require an `api_key`.
- `ollama` and `bedrock` allow omitting the `api_key`.
- Custom proxies with an explicitly specified `type` allow omitting the `api_key`.

For example, local `ollama` configuration:

```jsonc
{
  "provider": "ollama",
  "model": "qwen3:latest",
  "providers": {
    "ollama": {
      "base_url": "http://localhost:11434/v1"
    }
  }
}
```

### Writing Styles

Switch writing styles using the `style` field in the configuration file:

- `default` — General style
- `suspense` — Mystery/Suspense
- `fantasy` — Fantasy/Xianxia
- `romance` — Romance

### AI-Tone Removal & Custom Rules

A built-in AI-tone removal baseline is provided (under `assets/`, default out-of-the-box): a mechanical blacklist in `rules/default.md` (clichés / fatigue words, deterministically checked during commit) + semantic criteria in `references/anti-ai-tone.md` (injected into writer / editor for avoidance## Crash Recovery

Writing a long-form novel can take hours or even days. Intermittent crashes, network disconnections, or `Ctrl+C` are common. The system **automatically resumes when run again in the same directory**, without requiring manual action.

### Recovery Scenarios

| Interruption Scenario | Recovery Behavior |
|---|---|
| Planning Phase (building worldview/outline) | Checks saved settings and automatically completes missing items |
| Chapter Writing In-Progress (draft not yet committed) | Resumes from that chapter, reading the existing draft to continue |
| Review In-Progress | Re-triggers the Editor review |
| Rewrite/Polish Queue Not Cleared | Continues processing the chapters waiting to be rewritten/polished |
| Arc/Volume Expansion Interrupted (review done but next arc not expanded) | Automatically detects the skeleton arc/volume and triggers the Architect to expand it |
| User Steering Incomplete | Re-injects the last steering instruction |
| Normal Writing Interrupted | Continues from the next chapter |

### How It Works

All creative outputs are persisted in the `output/` directory. Each tool execution, upon success, writes a checkpoint to `meta/checkpoints.jsonl`. Upon restart:

1. Reads `progress.json` + the latest checkpoint + pending signals.
2. Generates a recovery instruction down to the exact step level (e.g., "Chapter 7 draft is written, please proceed to check_consistency").
3. Fires a single `Prompt` to start the Coordinator, entering the long loop to continue writing.

> File writes use atomic `temp + fsync + rename` operations, ensuring existing data remains undamaged even during sudden power losses.

## Real-time Steering (Steer)

During writing, you can inject steering feedback at any time via the input box **without pausing or restarting**.

### TUI Mode

Once writing starts, the bottom input box automatically switches to steering mode:

```
❯ Bring the romance plotline forward to Chapter 4, increase interactions between the male and female leads
```

After inputting and pressing Enter, the system automatically:
1. Records the steering instruction to `run.json` (for crash recovery).
2. Injects it into the running Coordinator.
3. The Coordinator evaluates the impact scope, deciding whether to modify settings, rewrite existing chapters, or adjust future chapters.

### Steering Examples

| Steering Instruction | Potential System Response |
|---|---|
| "Change the protagonist to female" | Modifies character settings, evaluates if written chapters need rewrites |
| "Bring the romance forward to Chapter 4" | Adjusts the outline, potentially rewrites Chapter 4 and subsequent ones |
| "Add a villain character" | Updates character profiles and world rules, introducing them in subsequent chapters |
| "The pacing is too slow, speed it up" | Adjusts outline density for future chapters |

## Design Philosophy

> **Shift complexity from code to the model.** Less code means fewer points of failure. Leave the decision-making to the roles best suited for it.

### LLM-Driven, Simpler is More Stable

- **LLM-controlled decision-making** — All process decisions are autonomously determined by the Coordinator; the Host does not intervene. When tools fail, they return structured errors, and the LLM decides whether to retry or adjust its strategy.
- **Fact-only tools** — Atomic IO + checkpoint writes; tools return structured facts in JSON (e.g., `final_verdict` / `pending_rewrites` / `arc_end_reached`), never carrying instruction strings.
- **Reminder-driven turns** — The Host reads the fact layer before each LLM call, running a pure generator function to inject a `<system-reminder>`. Instructions do not enter persistent history and are re-evaluated from facts each turn.
- **StopGuard physical gatekeeper** — When `Phase ≠ Complete`, the Coordinator is physically blocked from executing `end_turn`; consecutive blocked attempts will escalate to an execution termination.
- **No complex orchestration** — No task queues, schedulers, or policy engines. The Coordinator's run is the sole control loop.
- **Performance scales with model strength** — The architecture locks decision-making within prompt and tool semantics; model upgrades immediately yield benefits without modifying a single line of Host code.

### Fully Automated Closed Loop

Single-line input, complete novel output:

```
"Write a mystery novel" → Build Worldview → Design Characters → Plan Outline
                      → Write Chapter-by-Chapter → Quality Review → Auto-Rewrite
                      → Arc-level Summary → Character Snapshots → Complete Book
```

- **Coordinator autonomous scheduling** — Decides the next step in a long loop by reading the fact layer + Reminder, without Host intervention.
- **Writer autonomous creation** — Completes the full plan → draft → check → commit closed loop independently for each chapter.
- **Editor autonomous review** — Analyzes structural issues across chapters and outputs verdicts and impact scopes.
- **Architect autonomous building** — Derives the full setup from a single-line request, expanding subsequent planning at arc/volume boundaries.
- **Automated foreshadowing management** — Planting, developing, and resolving foreshadowing is tracked entirely by the agents.
- **Automated pacing control** — Tracks narrative lines and hook history to avoid repetitive chapter structures.

### Decoupled Facts and Instructions

Tools return only facts; instructions are recomputed each turn by the Reminder from the fact layer:

- `commit_chapter` / `save_review` return structured facts (`final_verdict` / `pending_rewrites` / `arc_end_reached` / `next_chapter`), without carrying any `[System]` strings.
- Pure generator functions under `internal/host/reminder/` read `Progress` + `Outline`, generating a `<system-reminder>` pre-turn: `flow` (what to do now / end-of-arc braking), `queue_guard` (new chapters blocked if rewrite queues are not cleared), and `book_complete` (ends turn only when the book is finished). Physical safety is handled by `StopGuard` rejecting `end_turn` when `phase ≠ Complete`.
- Reminder lasts only one turn, does not enter the chat history, and is excluded from compression. Rules have unit tests to catch regressions.

This prevents instructions from being swallowed by chained calls or drifting in tool outputs. Fixing a bug is as simple as adding a generator rule and a test.

## Technology Stack

- **Go 1.25** — Main language
- **[agentcore](https://github.com/voocel/agentcore)** — Minimal agent kernel (tool-calling + streaming)
- **[litellm](https://github.com/voocel/litellm)** — Unified LLM interface adapter
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** — Terminal TUI framework

## License

MIT

This project actively participates in and endorses the [linux.do community](https://linux.do/).�磨队列未清空 | 继续处理待重写的章节 |
| 弧/卷展开中断（评审完但下一弧未展开） | 自动检测骨架弧/卷，触发 Architect 展开 |
| 用户干预未完成 | 重新注入上次的干预指令 |
| 正常写作中断 | 从下一章继续 |

### 工作原理

所有创作产物持久化在 `output/` 目录。每个工具执行成功后写入 checkpoint (`meta/checkpoints.jsonl`)。重启时：

1. 读取 `progress.json` + 最近 checkpoint + 待处理信号
2. 精确到 step 级生成恢复指令（如"第 7 章 draft 已落盘，请继续 check_consistency"）
3. 一次 `Prompt` 启动 Coordinator，进入长循环继续创作

> 文件写入使用 temp + fsync + rename 原子操作，即使在写入过程中断电也不会损坏已有数据。

## 实时干预（Steer）

创作过程中可以随时通过输入框注入修改意见，**不需要暂停或重启**。

### TUI 模式

创作启动后，底部输入框自动切换为干预模式：

```
❯ 把感情线提前到第4章，增加男女主的对手戏
```

输入后按 Enter，系统自动：
1. 记录干预指令到 `run.json`（崩溃恢复用）
2. 注入到正在运行的 Coordinator
3. Coordinator 评估影响范围，决定是修改设定、重写已有章节，还是在后续章节调整

### 干预示例

| 干预指令 | 系统可能的响应 |
|---|---|
| "主角改成女性" | 修改角色设定，评估已写章节是否需要重写 |
| "把感情线提前到第4章" | 调整大纲，可能重写第4章及后续 |
| "加入一个反派角色" | 更新角色档案和世界规则，在后续章节引入 |
| "节奏太慢了，加快推进" | 调整后续章节的大纲密度 |

## 设计理念

> **把复杂度从代码搬到模型里。** 代码越少，能坏的地方越少。决策权交给更擅长做决策的角色。

### LLM 驱动，越简单越稳定

- **决策权归 LLM** — 流程决策全部由 Coordinator 自主判断，Host 不介入。工具失败时返回结构化错误，由 LLM 自行决定重试或调整策略
- **工具只返事实** — 原子 IO + checkpoint 写入，返回值是 JSON 事实字段（`final_verdict` / `pending_rewrites` / `arc_end_reached`），不夹带任何指令字符串
- **Reminder 驱动每轮** — Host 在每轮 LLM 调用前读事实层，运行纯函数 generator 生成 `<system-reminder>` 注入，指令不进持久历史、每轮从事实重算
- **StopGuard 物理守门** — `Phase ≠ Complete` 时 Coordinator 物理上不可 `end_turn`，连续阻拦超限才升级终止
- **拒绝复杂编排** — 没有 task queue、没有 scheduler、没有 policy engine。Coordinator 的一次 Run 就是唯一的控制流
- **模型越强收益越大** — 架构把决策权留在 prompt 和工具语义里，模型升级后直接吃到收益，Host 一行不用改

### 全自动闭环

一句话输入，完整小说输出：

```
“写一部悬疑小说” → 构建世界观 → 设计角色 → 规划大纲
                → 逐章写作 → 质量评审 → 自动重写
                → 弧级摘要 → 角色快照 → 完整成书
```

- **Coordinator 自主调度** — 在一次长循环里读事实层 + Reminder 决定下一步，无需 Host 干预
- **Writer 自主创作** — 每章独立完成 plan → draft → check → commit 的完整闭环
- **Editor 自主评审** — 跨章节分析结构问题，输出裁定及影响范围
- **Architect 自主构建** — 从一句话需求推导出完整设定，弧/卷边界时自主展开后续规划
- **自动伏笔管理** — 埋设、推进、回收全程由 Agent 自行追踪
- **自动节奏调控** — 追踪叙事线和钩子类型历史，避免连续章节结构雷同

### 事实与指令解耦

工具只返事实，指令由 Reminder 每轮从事实层重算：

- `commit_chapter` / `save_review` 返回结构化事实（`final_verdict` / `pending_rewrites` / `arc_end_reached` / `next_chapter`），不夹带任何 `[系统]` 字符串
- `internal/host/reminder/` 下的纯函数 generator 读 `Progress` + `Outline`，每轮 pre-turn 生成 `<system-reminder>`：`flow`（当前该做什么 / 弧末刹车）/ `queue_guard`（队列未清禁止新章）/ `book_complete`（全书完成才放行）。物理兜底由 `StopGuard` 在 `phase≠Complete` 时拒绝 `end_turn` 承担
- Reminder 只存活一轮，不进历史、不参与压缩；规则有单元测试，退化可被回归捕获

这样指令不会被链式调用吞掉，也不会在工具产物里漂移。改 bug 只需加一个 generator + 一个测试。

## 技术栈

- **Go 1.25** — 主语言
- **[agentcore](https://github.com/voocel/agentcore)** — 极简 Agent 内核（tool-calling + streaming）
- **[litellm](https://github.com/voocel/litellm)** — 统一 LLM 接口适配
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** — 终端 TUI 框架

## License

MIT

本项目积极参与并认可 [linux.do 社区](https://linux.do/)。
