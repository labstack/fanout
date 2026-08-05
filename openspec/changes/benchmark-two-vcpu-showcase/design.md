# Design

## Context

`cmd/bench` generates OTLP traffic, optionally issues queries, and reports
export and query latency percentiles. It accepts `-metrics-url` to capture
server-side deltas from `/-/metrics`, and `-fanout-version` to stamp the build
under test into the report. It has never been run against a fixed machine and
the output has never been recorded, so there is no baseline to compare against.

The binary under test is the image CI publishes, `ghcr.io/labstack/fanout`. The
package is private while the repository is private, so the machine under test
authenticates to GHCR to pull it.

Fanout refuses to start without SMTP credentials, an `AUTH_CODE_SECRET`, and
`AI_API_KEY`. The benchmark supplies placeholders: no mail is sent and the
agent is never invoked, so the values only have to satisfy startup validation.

## Goals / Non-Goals

**Goals**

- One reproducible number for sustained ingest on a machine anyone can rent.
- Latency distributions, not averages. An average hides exactly the tail a
  reader cares about.
- Enough recorded method that a skeptical reader can re-run it.

**Non-Goals**

- Comparison against other observability systems. A fair comparison needs their
  tuning done by someone who wants them to win, which this is not.
- Finding the absolute ceiling. This measures a sustained rate at a fixed size,
  not the maximum before collapse.
- Measuring the agent or MCP paths. Those are model-latency-bound and would say
  more about the provider than about Fanout.

## Decisions

**Two machines, not one.** The load generator is CPU-hungry. Running it beside
the server on the two cores being measured would spend part of the machine
generating the load it is measuring, understating the result and making it
incomparable to anything. The driver goes on a separate `cpx22` in the same
datacenter.

**Dedicated vCPU for the system under test.** Hetzner's shared-core types are
cheaper, but a shared core makes run-to-run variance a property of the
neighbours rather than of Fanout. `ccx13` provides two dedicated cores, so a
re-run should land in the same place. This is what makes the number defensible.

**The driver is cross-compiled, not built on the machine.** `cmd/bench` has no
cgo dependency and builds static for `linux/amd64` with `CGO_ENABLED=0`, so the
driver needs no toolchain — only the binary. The server keeps its cgo build,
because DuckDB requires it.

**The historical run did not measure ingest authentication.** The instance used
a tokenless mode that has since been removed. The report states this because
the published number omits a credential check that every current deployment,
including demos, performs.

**The report records the digest, not the tag.** Tags move. A number is only
reproducible against the exact image that produced it.

## Risks / Trade-offs

- **A single run is a data point, not a distribution.** Repeating the run
  quantifies variance; the report states what was actually done rather than
  implying more.
- **Placeholder credentials mean untested paths.** Auth and the agent are not
  exercised, so this measures ingest and query only. The report says so.
- **Hetzner is one vendor.** The numbers describe this hardware. They are not a
  claim about equivalent core counts elsewhere.
- **Published benchmarks age.** The report records the version and date so a
  future reader can tell whether it still describes the current code.

## Migration Plan

None. No runtime component changes, so there is nothing to roll out or roll
back. The provisioned machines are destroyed once results are collected, and
the repository retains no dependency on them.
