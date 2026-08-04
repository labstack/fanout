# Fanout

Single-binary, agent-native OpenTelemetry investigation. Fanout owns ingest,
storage, the AG-UI runtime, typed MCP tools, SQLite conversation history, and
an embedded browser client. Rich results are portable MCP Apps, while named
dashboard layouts and widget configuration persist in SQLite and can be created
from the browser assistant or any OAuth-connected MCP client.

Five typed domains cover system health, service topology, performance,
trace inspection, and logs. Their React visualizations are compiled at build
time and embedded with the host in the same Go executable; Node or Bun is not
required at runtime.

- **Docs:** [fanout.run/docs](https://fanout.run/docs/)
- **Demo:** [demo.fanout.run](https://demo.fanout.run)
- **Releases:** [github.com/labstack/fanout/releases](https://github.com/labstack/fanout/releases)
- **Architecture:** [`docs/architecture.md`](docs/architecture.md)
- **Product contract:** [`openspec/specs/`](openspec/specs/)

## Quick start

Fanout refuses to start without an authentication-code secret, SMTP credentials
(email-code login), and an LLM API key (chat investigator). Replace the `<...>`
placeholders with your values:

```sh
docker run -d --name fanout \
  -p 7520:7520 -p 4317:4317 \
  -v $PWD/data:/var/lib/fanout/data \
  -e AUTH_CODE_SECRET=$(openssl rand -hex 32) \
  -e SMTP_HOST=<smtp-host> \
  -e SMTP_USER=<smtp-user> \
  -e SMTP_PASS=<smtp-password> \
  -e SMTP_FROM='"Fanout" <fanout@example.com>' \
  -e AI_API_KEY=<anthropic-or-openai-key> \
  ghcr.io/labstack/fanout:latest
```

Open <http://localhost:7520>, complete setup, and copy the one-time ingest
token. Point any OTLP/gRPC collector or SDK at `localhost:4317` (or your host's
address) with header `x-fanout-ingest-token: fo_<token>`.

Remote MCP needs a canonical public HTTPS `MCP_PUBLIC_URL`; the local command
disables it until that URL exists.

Full setup walkthrough: [fanout.run/docs#first-boot](https://fanout.run/docs/#first-boot).

## Repo layout

```
cmd/fanout/      Single Go server/runtime binary
internal/        Ingest, query, AG-UI, MCP, auth, and embedded web packages
ui/host/         Static React AG-UI client (compiled and embedded at build time)
ui/apps/         Portable React MCP Apps embedded by the MCP server
site/            Marketing + public docs (Astro)
openspec/specs/  Canonical shipped behavior by capability
openspec/changes Proposed work and archived decisions
docs/            Documentation index and operator runbooks
```

## Develop

```sh
just install   # Go/OpenSpec tools, UI + site deps, Lefthook
just up        # Run server + browser build watcher + site
just check     # Format, vet, lint, type-check, build
just test      # go test ./...
just docs-check # Strictly validate OpenSpec artifacts
```

With the shared local Caddy setup in `../docker`:

- App: `https://demo.fanout.test`
- Site/docs: `https://fanout.test`

Caddy owns `/api` and `/mcp` in local development, so the frontend dev flow expects that proxy to be running.

## License

See [LICENSE](LICENSE).
