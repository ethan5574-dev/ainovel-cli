# ainovel-cli

An end-to-end AI engine for writing long-form novels. A deterministic Engine dispatches Architect, Writer, and Editor workers from saved story facts; an Arbiter handles semantic decisions such as startup, steering, and failure recovery. From a one-line idea to a finished novel, writing can proceed autonomously, with optional chapter-by-chapter approval.

<p align="center">
  <img src="scripts/sample.gif" alt="ainovel-cli demo" width="800">
  <img src="scripts/novel.png" alt="ainovel-cli bg" width="800">
</p>

## Features

- **Deterministic engine + multiple agents** — Engine dispatches autonomous Architect / Writer / Editor agents using a fact-based decision table; the main loop incurs no LLM calls and can be tested exhaustively
- **Auditable semantic decisions** — Arbiter selects the planner, triages steering, and handles failures in single LLM calls, saving each decision for replay
- **Step-level crash recovery** — Successful tools write checkpoints for precise recovery at plan/draft/check/commit boundaries
- **Rolling volume/arc planning** — Long novels start with skeletons for two volumes and detailed chapters for the first arc. Architect expands later arcs/volumes as writing advances, consulting prior summaries and character state
- **Relevant chapter recommendations** — Foreshadowing, appearances, state changes, and relationships retrieve relevant history, complemented by next-chapter previews for continuity in 500+ chapter novels
- **Adaptive context** — Automatically selects full, sliding-window, or layered-summary context based on chapter count, supporting 500+ chapter novels
- **Seven-dimensional quality review** — Editor checks consistency, character behavior, pacing, continuity, foreshadowing, hooks, and aesthetics. Aesthetic review covers descriptive texture, narrative technique, distinct dialogue, diction, and emotional impact, with textual evidence for each
- **Real-time steering** — Enter revision feedback while writing without pausing; the system evaluates the impact and rewrites affected chapters
- **Optional chapter-by-chapter approval** — Automation remains the default. `/review on` enables fine control; each `/next` permits one new chapter, without rework or crash recovery consuming it incorrectly
- **TUI + Headless entry points** — Observe and steer interactively, or run continuously on a server, NAS, or CI system
- **Multiple LLM providers** — Switch between OpenRouter, Anthropic, Gemini, OpenAI, and others

## Architecture

Core design: **deterministic flow, autonomous creativity**. Engine/Route derive routine scheduling from persisted facts. Arbiter handles decisions requiring semantic interpretation. Host owns startup, recovery, user policies, and observation.

```
┌─────────────────────────────────────────────────┐
│                  Host (thin shell)                │
│             Startup / recovery / observe / inject │
└──────────────────────┬──────────────────────────┘
                       │ Runtime loop
┌──────────────────────▼──────────────────────────┐
│              Engine / Route                     │
│     Facts → Worker; Arbiter for semantic decisions │
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

- **Host** - Owns startup, crash recovery, chapter-advance policy, and events for TUI, headless mode, and the local Web UI.
- **Engine / Route** - Drives routine planning → writing → review → summary scheduling using deterministic facts.
- **Arbiter** - Makes structured, audited semantic decisions for startup, user steering, and failure/deadlock handling.
- **SubAgents** - Architect / Writer / Editor each have isolated context and collaborate through artifacts in the Store.
- **Tools** - Save artifacts and checkpoints, enforce preconditions, and return structured facts with next-step guidance where applicable.

### Agent Roles

| Agent | Responsibility | Tools |
|--------|------|------|
| **Arbiter** | Semantic decisions: startup planner selection, steering triage, failure/deadlock handling | None (one LLM call producing a structured decision) |
| **Architect** | Title, reader-facing synopsis, premise, outline, characters, and world rules | `novel_context` `save_book` `save_foundation` `expand_next_arc` `revise_outline` |
| **Writer** | Independently conceive, draft, self-check, and commit a chapter | `novel_context` `read_chapter` `plan_chapter` `draft_chapter` `check_consistency` `commit_chapter` |
| **Editor** | Read prose and review structure and aesthetics | `novel_context` `read_chapter` `save_review` `save_arc_summary` `save_volume_summary` |

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
6. `commit_chapter` — Commit the final draft and return fact fields (`arc_end_reached` / `next_chapter` etc.); Engine/Route determine the next step

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
curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/v1.2.3/scripts/install.sh | sh -s -- v1.2.3

# Or install via Go
go install github.com/voocel/ainovel-cli/cmd/ainovel-cli@latest

# View version / Update to latest version
ainovel-cli --version
ainovel-cli update

# First run, automatically enters setup wizard (Select Provider → Enter API Key → Base URL → Model Name)
ainovel-cli
```

> Windows or manual installation: download the appropriate platform archive from [Releases](https://github.com/voocel/ainovel-cli/releases/latest).
> The installer downloads the SHA256 manifest from the same GitHub Release and verifies it before extracting and installing the binary.

### Headless Mode

`--headless` runs without TUI, suitable for servers, NAS, CI, or background tasks. It has no first-run configuration wizard: run `ainovel-cli` interactively once, or create `~/.ainovel/config.json` manually.

```bash
# Start a new task with a one-line requirement
ainovel-cli --headless --prompt "Write an Eastern fantasy novel about a protagonist from a remote border town"

# Read requirements from a file
ainovel-cli --headless --prompt-file prompt.txt

# Resume an unfinished task in the same directory
ainovel-cli --headless
```

`--prompt` and `--prompt-file` are available only in Headless mode and are mutually exclusive. Model streaming goes to stdout, runtime events to stderr, and complete logs to the book directory's `logs/headless.log`.

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

For the local Web UI, build and start the cloned source:

```bash
docker compose up --build -d ainovel-web
```

Open <http://127.0.0.1:5173> and complete provider setup in the browser. Docker builds the frontend and embeds it in the Go binary; Node and Go are not required on the host. Configuration and novels persist in `./config` and `./workspace`.

To use another local port, run `AINOVEL_WEB_PORT=8080 docker compose up --build -d ainovel-web`, then open <http://127.0.0.1:8080>. The host port is localhost-only by default because the Web UI has no authentication. See [Local Web UI](docs/web-ui.md) for logs, stopping the service, and direct Docker commands.

After entering the TUI, two initial interaction modes are supported at startup:

- `Quick Start`: begin writing from a one-line idea
- `Co-create`: clarify requirements through conversation, with the **creative instruction draft updated live on the right**. AI suggests 1–3 directions per turn; combine them with number keys, edit and send, then press `Ctrl+S` to start writing

Both modes will eventually converge into the same set of writing instructions, which are then passed to the same writing engine.

For an existing detailed world setting or outline, create a new book from a file on the welcome page:

```text
/start ./outline.md
```

`/start` sends the entire file to Architect as initial requirements for foundation and dynamic planning, not as completed chapters. Use `/import` to continue an existing novel.

### Managing Multiple Novels

Each novel is bound to the directory where the command is launched, and output artifacts are stored in `{cwd}/output/novel/`. Starting in a different directory = starting a new novel; returning (`cd`) and starting again = automatically resuming from the last checkpoint. Configuration in `$AINOVEL_HOME/config.json` is shared globally and does not need to be copied. If unset, `AINOVEL_HOME` defaults to `~/.ainovel`.

### Configuration File

A configuration file `$AINOVEL_HOME/config.json` is automatically generated on the first run (`~/.ainovel/config.json` by default). You can edit this file directly to adjust settings later. If you delete the configuration file, running the command will trigger the setup wizard again.

Set `AINOVEL_HOME` to make native TUI/CLI/Web runs use the same persistent directory as Docker. In this repository, Docker Compose mounts `./config` at `/root/.ainovel`; for local development, point `AINOVEL_HOME` at that same host directory:

```bash
export AINOVEL_HOME="$PWD/config"
go run ./cmd/ainovel-cli                 # TUI
go run ./cmd/ainovel-cli web             # Go Web backend
```

For Web UI frontend development, run the backend above in one terminal and Vite in another:

```bash
export AINOVEL_HOME="/absolute/path/to/ainovel-cli/config"
cd internal/web/webui-src
npm ci                                  # once, or after dependency changes
npm run dev
```

Open <http://127.0.0.1:5174>. The project override `./.ainovel/config.json`, when present in the current working directory, still takes precedence over the shared home config.

You can also create the configuration file manually, referencing `$AINOVEL_HOME/config.example.jsonc` (automatically generated during setup; defaults to `~/.ainovel/config.example.jsonc`).

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

#### Configuration Lookup Order (Later Sources Override Earlier Ones)

1. `$AINOVEL_HOME/config.json` (defaults to `~/.ainovel/config.json`) — Global configuration
2. `./.ainovel/config.json` — Optional project override
3. `--config path/to/config.json` — Explicit command-line configuration

> Project `.ainovel/` mirrors global `~/.ainovel/`, rooted in the current project instead of your home directory. Use `./.ainovel/config.json` and `./.ainovel/rules/*.md` (see the custom-rules section below). This directory contains credentials and is ignored by Git by default.

Override rules:

- Later scalar fields override earlier ones, e.g. `provider`, `model`, and `style`
- `providers` and `roles` merge by key; fields within matching entries override individually
- Omitted fields inherit prior values: a project-level `base_url` alone preserves the global `api_key`
- Empty strings cannot explicitly clear inherited values; edit the higher-priority configuration directly when necessary

> ⚠️ `provider` (including `roles.*.provider`) refers to a **key in `providers`**, not a protocol name. Switching to an account absent from global `providers` requires its credentials (`api_key` / `base_url`) in the project configuration, or startup reports missing credentials.

`providers.<name>.models` optionally lists models selectable in the TUI `/model` panel. If omitted, the system uses models already referenced for that provider in configuration.

`providers.<name>.extra` configures the HTTP client (e.g. `user_agent`, `headers`, `anthropic_beta`). `providers.<name>.extra_body` extends the request body; do not confuse them.

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

## Accepting Manual Revisions

You may edit completed chapters in `output/novel/chapters/*.md` directly. SHA-256 hashes of accepted prose detect changes independently of modification times:

```text
/sync --check   # List changed chapters without model calls
/sync           # Accept edits and rebuild summaries, timeline, foreshadowing, relationships, state, and style memory
```

Unaccepted edits block resume, further input, and `/next` until `/sync`, preventing stale facts from driving new chapters. `/sync` never rewrites your prose: the model re-extracts chapter facts and reusable style preferences, while code handles versions, projections, and recovery deterministically. Affected reviews, arc/volume summaries, and snapshots are invalidated and rebuilt by Editor. Plot changes go to Architect before continuation to revise future plans or explicitly confirm that existing plans remain valid.

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
/import --guide=Interludes are standalone chapters     # Guidance may contain spaces; place it last
```

All options (the first three persist across crash recovery):

```text
/import ~/my-novel.txt --yes           # Unattended: accept segmentation and run the full pipeline
/import ~/my-novel.txt --story=closed  # Resolve uncertain story status as closed (or use open)
/import ~/my-novel.txt --continue      # Continue writing after import without an acceptance hold
/import                                # Resume an interrupted import without arguments
```

Prerequisites and recovery:

- Import only into an **empty book** with no completed chapters; merging another novel into an existing work is unsupported. Sources may be `txt`/`md`, encoded in UTF-8 or GB18030 (detected automatically; unreliable decoding produces an explicit error).
- Stage outputs live in `meta/import/` and are bound to input fingerprints. After interruption or failure, `/import` processes only missing work without repeating completed model calls. The welcome screen reports incomplete-import progress (e.g. 210/300 chapters analyzed); the engine blocks continuation until recovery finishes. Failed raw model responses remain in `meta/import/failures/` for diagnosis.
- If synthesized story status is `uncertain`, the pipeline pauses. Rerun with `--story=open|closed` to resolve it.
- Publishing sets a one-time acceptance hold by default. `--continue` skips it, although review mode still requires `/next`.
- Configure separate model tiers for the three import semantic functions under `roles` (see the role-based model configuration section).

> Source prose is saved verbatim as completed chapters, so import is for continuing the same book. To borrow only settings for a new work, start a new book normally and describe the desired style and setting.

## Export

Enter `/export` in TUI to combine completed chapters into an output file, defaulting to TXT at `{novel directory}/{book title}.txt`. Export is read-only and available during writing without affecting the engine.

The **output path extension** selects the format (`.txt` / `.epub`):

```text
/export                            # Default TXT: {novel directory}/{book title}.txt
/export ~/light.txt                # .txt selects TXT
/export ~/light.epub               # .epub selects EPUB (Apple Books / WeChat Read / Kindle converters)
/export from=10 to=30 --overwrite  # Chapter range and overwrite
/export from=10 ~/x.epub --overwrite
```

- **TXT** — Book title, volume separators, then prose (layered long-form books include volume separators). Internal premise material and arc separators are excluded: premise is an author/engine blueprint, and arcs are too fine-grained for readers. The exporter generates chapter headings and removes duplicate writer-supplied headings from prose.
- **EPUB** — An EPUB 3 container with title/synopsis metadata, title page, table of contents, and chapter XHTML. Content-derived identifiers let readers recognize a re-export as an update. No cover image is included.

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

Configurable roles: `architect` / `writer` / `editor` / `import_segment` / `import_analyze` / `import_synthesize`. Arbiter uses the top-level default model; `coordinator` is no longer a supported role.

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

Writing standards and anti-AI-tone guidance can be overridden **without source changes or recompilation**. Precedence: `<output directory>/style/` (travels with the book across machines) over `~/.ainovel/style/` (global):

```
style/
├── voice.md                          # Append guidance after built-in standards, with higher priority
├── anti-ai-tone.md                   # Append anti-AI-tone guidance in the same way
├── styles/
│   └── xianxia.md                    # Custom style; filename selects config.style
│                                     # A built-in name such as fantasy.md replaces that preset
└── genres/
    └── xianxia/
        └── style-references.md       # Genre references, replaced as a whole
```

In short: **append guidance (voice / anti-ai-tone), replace presets (styles / genres)**. Precedence is an instruction to the model; mechanically enforced forbidden words and word-count limits belong in the rules directory above. Restart to apply changes; checkpoints preserve step-level progress. Execution-protocol prompts are not overrideable because tool guards enforce collaboration invariants. See `docs/voice-layer.md`.

## Output Structure

Creative data (chapters, outlines, characters, progress, etc.) lives in the output directory. Running again resumes prior progress automatically. Deleting that directory discards the work and starts over.

```
output/{novel_name}/
├── book.md             # Readable title and synopsis projection
├── chapters/           # Accepted prose (Markdown)
│   ├── 01.md
│   └── ...
├── summaries/          # Chapter summaries (JSON)
├── drafts/             # Chapter drafts
├── reviews/            # Review reports
├── timeline.jsonl      # Append-only timeline facts
├── timeline.md         # Readable timeline projection
├── premise.md          # Story premise
├── outline.json        # Flat outline (expanded chapters only)
├── layered_outline.json # Layered outline (long-form mode)
├── characters.json     # Character profiles
├── world_rules.json    # World rules
├── meta/
│   ├── book.json       # Sole source of truth for book metadata
│   ├── compass.json   # Ending-direction compass (long-form mode)
│   ├── progress.json   # Runtime progress
│   ├── foreshadow.json # Foreshadowing ledger
│   ├── state_changes.jsonl # Append-only character-state changes
│   ├── style_rules.json# Writing rules distilled at arc boundaries
│   ├── snapshots/      # Character snapshots (long-form mode)
│   └── checkpoints.jsonl # Step checkpoints appended after successful tools
```

## Crash Recovery

A long novel may take hours or days, with crashes, disconnections, or Ctrl+C along the way. **Rerun in the same directory to resume automatically**, without manual recovery steps.

### Recovery Scenarios

| Interruption | Recovery Behavior |
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
3. Restarts Engine, whose routing decisions and Worker recovery use those persisted facts.

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
2. Sends it to Arbiter for semantic triage.
3. Applies the decision at a safe boundary: modify settings, review/rewrite existing chapters, adjust future planning, or pause as requested.

### Steering Examples

| Steering Instruction | Potential System Response |
|---|---|
| "Change the protagonist to female" | Modifies character settings, evaluates if written chapters need rewrites |
| "Bring the romance forward to Chapter 4" | Adjusts the outline, potentially rewrites Chapter 4 and subsequent ones |
| "Add a villain character" | Updates character profiles and world rules, introducing them in subsequent chapters |
| "The pacing is too slow, speed it up" | Adjusts outline density for future chapters |
| "Write through chapter 20" | Continues until chapter 20 is stably committed, then pauses |

## Design Philosophy

> **Shift complexity from code to the model.** Less code means fewer points of failure. Leave the decision-making to the roles best suited for it.

### Facts in Code, Creative Judgment in Models

- **Deterministic scheduling** — Engine/Route select routine work without spending an LLM call on every dispatch.
- **Semantic adjudication** — Arbiter handles startup, steering, and failure decisions, each with a persisted audit record.
- **Autonomous Workers** — Models own creative planning, prose, and review within guarded tool contracts.
- **Physical safeguards** — Tool ownership checks, checkpoint guards, and ChapterAdvanceGate enforce safe execution independently of prompt compliance.
- **Simple state** — Persisted story facts drive the loop; no general workflow-instance or task-instance machinery is needed.
- **Model upgrades improve creativity** — Stronger models can improve Worker output without replacing deterministic scheduling.

### Fully Automated Closed Loop

Single-line input, complete novel output:

```
"Write a mystery novel" → Build Worldview → Design Characters → Plan Outline
                      → Write Chapter-by-Chapter → Quality Review → Auto-Rewrite
                      → Arc-level Summary → Character Snapshots → Complete Book
```

- **Fact-driven scheduling** — Engine/Route select the next Worker; Arbiter handles semantic exceptions.
- **Writer autonomous creation** — Completes the full plan → draft → check → commit closed loop independently for each chapter.
- **Editor autonomous review** — Analyzes structural issues across chapters and outputs verdicts and impact scopes.
- **Architect autonomous building** — Derives the full setup from a single-line request, expanding subsequent planning at arc/volume boundaries.
- **Automated foreshadowing management** — Planting, developing, and resolving foreshadowing is tracked entirely by the agents.
- **Automated pacing control** — Tracks narrative lines and hook history to avoid repetitive chapter structures.

### Decoupled Facts and Instructions

Tools persist authoritative facts; Engine derives subsequent work from those facts:

- `commit_chapter` / `save_review` return structured facts (`final_verdict` / `pending_rewrites` / `arc_end_reached` / `next_chapter`), without carrying any `[System]` strings.
- The pure router in `internal/flow/` reads an explicit State assembled from Progress, Outline, reviews, and summaries. Rework precedes new writing, and arc/volume finishing precedes expansion.
- Host's ChapterAdvanceGate enforces user permits and one-time holds separately from creative routing. Worker guards require task-specific checkpoints before successful exit.

Scheduling and safety rules are covered by deterministic and contract tests, independent of model-generated prose.

## Technology Stack

- **Go 1.25** — Main language
- **[agentcore](https://github.com/voocel/agentcore)** — Minimal agent kernel (tool-calling + streaming)
- **[litellm](https://github.com/voocel/litellm)** — Unified LLM interface adapter
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** — Terminal TUI framework

## License

MIT

This project actively participates in and endorses the [linux.do community](https://linux.do/).
