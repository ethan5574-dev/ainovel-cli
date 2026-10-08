# Local Web UI

The web studio is embedded in the Go binary. It uses the existing `Host` and engine, so the browser and TUI share the same persistence, checkpoints, agent loop, and model configuration.

## Run locally

Start the browser UI:

```bash
go run ./cmd/ainovel-cli web --port 5173
```

Open <http://127.0.0.1:5173>.

On a fresh machine, the browser opens a first-run provider setup wizard. No TUI setup is required.

The default address is loopback-only. To bind another interface explicitly:

```bash
go run ./cmd/ainovel-cli web --addr 0.0.0.0:5173
```

The local studio supports:

- Starting, pausing, resuming, steering, reviewing, and reopening a novel.
- Guided co-creation for a new book and stage planning for a book in progress.
- A live manuscript browser with outline, chapter status, summaries, and chapter content.
- TXT and EPUB downloads, including chapter-range export.
- TXT manuscript import with chapter detection, story-memory reconstruction, confirmation, and optional continuation.
- Provider/model/API-key editing, connection testing, and live default-model switching.
- Live agent status, runtime activity, and model output. Upstream removed `ask_user`;
  use co-create or send a direction to guide the engine instead.

## Docker

After cloning the repository, start the `ainovel-web` Compose service:

```bash
docker compose up --build -d ainovel-web
```

Open <http://127.0.0.1:5173>. On a fresh installation, configure the provider and API key in the browser setup wizard.

Compose builds the local `ainovel-cli:local` image. The Dockerfile builds the React frontend with Node, then embeds the generated assets in the Go binary. You do not need Node or Go installed on the host, and stale host bundles are excluded from the build context.

The service binds only to localhost, mounts `./config` for persistent configuration and `./workspace` for generated books, and checks `/api/health`. The optional platform/Postgres services are not required.

Choose another local port if `5173` is occupied:

```bash
AINOVEL_WEB_PORT=8080 docker compose up --build -d ainovel-web
```

Then open <http://127.0.0.1:8080>. Set `AINOVEL_WEB_PORT` in an untracked `.env` file to keep this choice across Compose commands.

To inspect or stop the service:

```bash
docker compose logs -f ainovel-web
docker compose stop ainovel-web
```

The same image also supports a direct Docker run:

```bash
docker build -t ainovel-cli:local .
docker run --rm --init -p 127.0.0.1:5173:5173 \
  -v "$PWD/config:/root/.ainovel" \
  -v "$PWD/workspace:/workspace" \
  ainovel-cli:local web --addr 0.0.0.0:5173
```

The container listens on `0.0.0.0` internally so Docker can forward traffic; the host port mapping remains localhost-only. Do not replace it with a public binding unless you provide appropriate access control. Configuration may contain API keys; do not commit `config/` files.

The web server is intended for local use. It defaults to loopback, rejects cross-origin mutations, and sends restrictive browser security headers. Keep the default binding unless you explicitly need access from another device; binding to `0.0.0.0` does not add user authentication.

## Source development

To edit the Go backend and React frontend without rebuilding the Docker image, use two terminals from the repository root. Point `AINOVEL_HOME` at the same `config/` directory that Compose mounts into `/root/.ainovel`:

Terminal 1 (Go API/backend):

```bash
export AINOVEL_HOME="$PWD/config"
go run ./cmd/ainovel-cli web --addr 127.0.0.1:5173
```

Terminal 2 (Vite frontend with hot reload):

```bash
export AINOVEL_HOME="/absolute/path/to/ainovel-cli/config"
cd internal/web/webui-src
npm ci
npm run dev
```

Open <http://127.0.0.1:5174>. Frontend edits apply immediately; restart the `go run` process after Go changes. To use the TUI with the same config, run `AINOVEL_HOME="$PWD/config" go run ./cmd/ainovel-cli` from the repository root. `AINOVEL_HOME` defaults to `~/.ainovel` when unset. A project-level `./.ainovel/config.json` remains a higher-priority override.
