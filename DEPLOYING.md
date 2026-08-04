# Deploying Fanout

`scripts/yeet.sh` deploys the marketing site, the demo, and a Fanout instance to
one host as a single Docker Compose project, with Traefik terminating TLS.

Everything deployment-specific is configuration. Nothing in this repository
names a particular host.

## Required configuration

Set these in your shell (or CI environment) before running the deploy:

| Variable | Purpose | Example |
| --- | --- | --- |
| `FANOUT_DEPLOY_HOST` | SSH target for the deploy | `root@deploy.example.com` |
| `FANOUT_APP_HOST` | Public hostname of the Fanout instance | `fanout.example.com` |
| `FANOUT_INGEST_HOST` | Public hostname for OTLP gRPC over TLS | `ingest.example.com` |
| `FANOUT_SITE_HOST` | Marketing site hostname (default `fanout.run`) | `example.com` |
| `FANOUT_DEMO_HOST` | Demo hostname (default `demo.fanout.run`) | `demo.example.com` |

`yeet.sh` derives `PUBLIC_URL`, `MCP_PUBLIC_URL`, and `INGEST_ENDPOINT` from
`FANOUT_APP_HOST` and `FANOUT_INGEST_HOST` and writes them to the host's root
`.env` alongside the resolved image tags.

## Required files

Each is gitignored; copy the committed template beside it and fill it in.

| File | Template | Holds |
| --- | --- | --- |
| `traefik/.env` | `traefik/.env.example` | Cloudflare DNS-01 token, ACME contact email |
| `traefik/dynamic.yml` | `traefik/dynamic.yml.example` | Traefik routers for your hostnames |
| `fanout/.env` | `fanout/.env.example` | Fanout instance settings and credentials |
| `demo/.env` | `demo/.env.example` | Demo instance settings |

`yeet.sh` refuses to deploy if any is missing or still contains template
placeholders.

## Uptime workflow

`.github/workflows/uptime.yml` probes the same hostnames. Define `SITE_HOST`,
`DEMO_HOST`, `APP_HOST`, and `INGEST_HOST` as repository variables (Settings →
Secrets and variables → Actions → Variables). The workflow fails if any is
unset rather than probing an empty URL and reporting success.

Deploy credentials `DEPLOY_SSH_KEY` and `DEPLOY_KNOWN_HOSTS` are repository
secrets, never files in this repository.
