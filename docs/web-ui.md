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

The repository includes an `ainovel-web` compose service:

```bash
docker compose up ainovel-web
```

It publishes port `5173`, mounts `./config` as the configuration directory, and mounts `./workspace` for generated books.

The web server is intended for local use. It defaults to loopback, rejects cross-origin mutations, and sends restrictive browser security headers. Keep the default binding unless you explicitly need access from another device; binding to `0.0.0.0` does not add user authentication.
