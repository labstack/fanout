# Fanout

Single-binary OpenTelemetry ingest, storage, and query. Self-hosted.

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

Open <http://localhost:7520>, complete setup, copy the generated ingest token, and point any OTLP collector or SDK at `:4317` with header `x-fanout-ingest-token: fo_<token>`.

Full setup walkthrough: [fanout.run/docs#first-boot](https://fanout.run/docs/#first-boot).

## Repo layout

```
cmd/fanout/      Go binary
internal/        Go packages (ingest, lake, query, api, mcp, …)
internal/ui/     Embedded admin UI (Go glue)
web/             React admin UI source (Vite)
site/            Marketing + public docs (Astro)
docs/            Internal plans, specs, design notes
```

## Develop

```sh
just install   # Go tools, bun deps (web + site), pre-commit
just up        # Run server + web + site together
just check     # Format, vet, lint, type-check, build
just test      # go test ./...
```

With the shared local Caddy setup in `../docker`:

- Site/docs: `https://fanout.test`
- App: `https://demo.fanout.test`

Caddy owns `/api` and `/mcp` in local development, so the frontend dev flow expects that proxy to be running.

## License

See [LICENSE](LICENSE).
