---
title: Introduction
description: What Fanout is, what it solves, and when to choose it.
---

Fanout is a single-binary observability platform you run yourself. It accepts OpenTelemetry traces, logs, and metrics, gives you a fast UI to explore them, an alert engine, and a chat investigator that can triage incidents end-to-end.

There is no SaaS, no cluster to operate, no separate database to provision. One process, one config, your machine.

## What you get

- **Drop-in OpenTelemetry.** Point any OTel SDK or collector at Fanout's OTLP endpoint. Traces, logs, and metrics arrive on the standard wire format — nothing custom to learn.
- **Sub-second exploration.** Service overviews, trace waterfalls, log search, and metric queries return in milliseconds — even when you scroll weeks back.
- **A chat investigator.** Ask *"why is checkout slow?"* and get a rooted answer. Pair it with Claude Code over MCP and triage from your editor.
- **Alerts that match the data.** Rules over the same signals the UI shows — error rates, latency percentiles, throughput deltas, anomaly scores. Webhooks deliver to wherever you already get pages.
- **Self-hosted by design.** Your data stays on your network. No per-host fees, no telemetry tax, no agents shipping data offsite.

## What it isn't

- **Not a SaaS.** You run the binary.
- **Not a cluster.** One process. Scale vertically. Most teams won't outgrow a single VM.
- **Not a collector replacement.** Point your existing OpenTelemetry Collector at Fanout, or send directly from an SDK — whichever you already do.

## When to choose Fanout

You'll feel at home if you want to:

- Consolidate traces, logs, and metrics in one place without standing up a stack.
- Keep the data on disks you control, for cost or compliance reasons.
- Move from dashboard-hunting to chat-driven investigation, with an AI loop that can reach the same data you can.

If you need a multi-tenant, multi-region SaaS with an account team, this isn't that.

## Next steps

- [Install](/docs/install/) — Docker or pre-built binary.
- [Getting started](/docs/getting-started/) — first boot to first trace in under five minutes.
