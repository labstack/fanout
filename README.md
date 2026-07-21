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

## Quick start

```sh
docker run -d --name fanout \
  -p 7520:7520 -p 4317:4317 \
  -v $PWD/data:/var/lib/fanout/data \
  ghcr.io/labstack/fanout:latest
```

Open <https://demo.fanout.test>, complete setup, and copy the ingest token. Point any OTLP collector or SDK at
`demo.fanout.test:4317` with header `x-fanout-ingest-token: fo_<token>`.

Full setup walkthrough: [fanout.run/docs#first-boot](https://fanout.run/docs/#first-boot).

## Repo layout

```
cmd/fanout/      Single Go server/runtime binary
internal/        Ingest, query, AG-UI, MCP, auth, and embedded web packages
ui/host/         Static React AG-UI client (compiled and embedded at build time)
ui/apps/         Portable React MCP Apps embedded by the MCP server
site/            Marketing + public docs (Astro)
docs/            Internal plans, specs, design notes
```

## Develop

```sh
just install   # Go tools, bun deps (web + site), pre-commit
just up        # Run server + browser build watcher + site
just check     # Format, vet, lint, type-check, build
just test      # go test ./...
```

With the shared local Caddy setup in `../docker`:

- App: `https://demo.fanout.test`
- Site/docs: `https://fanout.test`

Caddy owns `/api` and `/mcp` in local development, so the frontend dev flow expects that proxy to be running.

## License

See [LICENSE](LICENSE).
