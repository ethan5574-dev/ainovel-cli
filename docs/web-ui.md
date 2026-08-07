# Local Web UI

The web studio is embedded in the Go binary. It uses the existing `Host` and engine, so the browser and TUI share the same persistence, checkpoints, agent loop, and model configuration.

## Run locally

Complete first-run provider setup once:

```bash
go run ./cmd/ainovel-cli
```

Then start the browser UI:

```bash
go run ./cmd/ainovel-cli web --port 5173
```

Open <http://127.0.0.1:5173>.

The default address is loopback-only. To bind another interface explicitly:

```bash
go run ./cmd/ainovel-cli web --addr 0.0.0.0:5173
```

The current local UI supports starting a novel, resuming a session, sending interventions, pausing, chapter-review mode, approving the next chapter, reopening a completed book, live event/model output, and `ask_user` questions.

## Docker

The repository includes an `ainovel-web` compose service:

```bash
docker compose up ainovel-web
```

It publishes port `5173`, mounts `./config` as the configuration directory, and mounts `./workspace` for generated books.

The web server is intended for local use. Keep the default loopback binding unless you explicitly need access from another device and understand that the UI has no authentication layer yet.
