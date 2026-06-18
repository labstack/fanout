# Fair Throughput Benchmark — Design

**Date:** 2026-06-18
**Status:** Approved (design)
**Author:** v@labstack.com (with Claude)

## Problem

We need a defensible answer to "what throughput does fanout support?" — one that is
fair to the engine and meaningful to customers.

The existing `scripts/bench-hetzner.sh` produces a fast directional number
(~64K rows/s on a 4-core cpx32, build `cc3a3d9`) but it is a **30-second burst**
with two methodology flaws that make it unsuitable as a "supported throughput"
claim:

1. **The load generators share the box with fanout.** On a 4-core VM, 4 parallel
   generators competed with fanout for CPU (peak was 89% summed across *all*
   processes). Fanout never had its full cores, so the number is understated in
   that dimension — but the test also can't say what fanout does with dedicated
   cores.
2. **It's a burst, not sustained.** 30s and ~3 rollup cycles cannot surface the
   accumulation failure modes (unbounded file/snapshot growth, rollups falling
   behind, RSS creep) that only appear over minutes. It also runs an aggressive
   `FLUSH_SECONDS=5`/`ROLLUP_EVERY=15` rather than prod defaults, and never
   exercises the **read path under ingest load** — the real customer workload.

`scripts/soak.sh` covers sustained growth invariants but also co-locates the
driver and does not drive query load. `cmd/loadgen` already supports both
query-under-load (`-query-workers`/`-query-url`/`-query-rate`) and SLO pass/fail
gates (`-max-export-p95-ms`/`-max-query-p95-ms`); they are simply not wired into
a two-VM, prod-realistic harness.

## Goal

Produce **two numbers** from one harness run:

- **Ceiling** — the ingest rate at which the first SLO breaks. The failure point;
  context and a marketing/headroom number, not a promise.
- **Rated capacity** — the highest *sustained* ingest rate at which **every** SLO
  holds, confirmed by a longer soak. This is the customer-facing "supported
  throughput."

## SLO gate

A run/step PASSES only if all hold:

| Invariant | Threshold |
|---|---|
| Dropped rows | `fanout_rows_dropped_total` delta == 0 |
| Query latency under load | P95 < **1500 ms** (`-max-query-p95-ms 1500`) |
| Rollup freshness | rollup age < 4× `ROLLUP_EVERY` (i.e. < 240s) at every sample |
| File growth bounded | `fanout_lake_partitions` ≤ PART_CAP (compaction keeps up) |
| Memory | RSS stable, no kernel OOM |
| Errors | zero `level":"ERROR"` lines; zero send/query errors |
| Liveness | fanout process alive + `/healthz` reachable at the end |

Liveness is asserted first: an OOM-killed server leaves no log and makes
metric scrapes read `0`, which would otherwise look like a clean pass.

## Topology — the key fairness fix

Two Hetzner VMs, both always torn down on exit (trap), in the same location for
low driver→target latency:

- **Fanout-under-test:** `cpx32` (4 vCPU / 8 GB) — *same tier as the prior burst
  run*, so the fair number is directly comparable to the 64K figure. Only the
  methodology changes, not the hardware.
- **Load driver:** a separate `cpx41` (8 vCPU / 16 GB) — more cores than the
  target so the generator is never the bottleneck and fanout keeps all 4 of its
  cores. This removes the co-location flaw.

Ingest path: **direct gRPC** to the target's `:4317` (insecure). We are measuring
engine capacity, not the edge; TLS/Traefik overhead is explicitly out of scope
for the headline number.

## Configuration

Prod-realistic, not the aggressive burst settings:

- `FLUSH_SECONDS=15`, `ROLLUP_EVERY=60` (prod defaults).
- `DUCKLAKE_MAINTENANCE_EVERY_SECONDS` — short enough that a 15-min soak can
  verify compaction keeps `lake_partitions` bounded (e.g. 60s, as soak.sh uses),
  documented in the report as a deviation from the 1h prod default.
- `DUCKDB_MEMORY`/`DUCKDB_THREADS` left self-sizing (the cgroup-aware defaults
  that prod uses).
- Data shape: `-services 50 -attr-cardinality 200 -error-rate 0.05
  -messaging-ratio 0.15`, logs + metrics on. This exercises both rollups
  (`service_rollup` + `edge_rollup`), GROUP BY cardinality, and attribute
  extraction.

## Procedure

1. **Provision** both VMs, install the toolchain on each, ship the current git
   HEAD (`git archive`), build `fanout` on the target and `loadgen` on the driver.
2. **Boot fanout** on the target with prod-default config and `PUBLIC_READ=true`
   (tokenless ingest). Wait for `/healthz`.
3. **Ramp** — for each rate step in `10000 25000 50000 100000 200000` traces/s,
   the driver runs `loadgen` for ~3 min with the SLO gate armed and query load on:
   ```
   loadgen -endpoint <target>:4317 -rate <step> -duration 3m \
     -services 50 -attr-cardinality 200 -error-rate 0.05 -messaging-ratio 0.15 \
     -metrics-url http://<target>:7520/-/metrics \
     -query-url http://<target>:7520 -query-workers 4 -query-rate 20 \
     -max-query-p95-ms 1500 -report step-<step>.json
   ```
   The harness also samples rollup age / partitions / RSS between steps (the
   signals loadgen's own report does not gate on). The **ceiling** is the first
   step that fails the gate; the **last passing step** is recorded.
4. **Certify** — a **15-min sustained soak** at ~80% of the last-passing rate,
   query-under-load on, full SLO gate. Passing this is the **rated capacity**.
5. **Report** — print a summary table (per-step pass/fail, ceiling, rated
   capacity, headroom ratio, and the achieved server-side rows/s vs target
   traces/s) and keep the per-step JSON reports. Tear down both VMs.

## Components

- **`scripts/bench-fair.sh`** — the new harness. Orchestrates provisioning,
  shipping, the ramp loop, the soak, result aggregation, and teardown. Reuses
  existing `loadgen` flags; **no Go changes**. Modeled on `bench-hetzner.sh`
  (provision/trap/ship/build) + `soak.sh` (between-sample growth invariants),
  extended to two VMs and rate-stepping.
- **`just stress fair`** — a new subcommand in the `justfile` stress dispatcher
  routing to `scripts/bench-fair.sh`.
- **`cmd/loadgen`** — unchanged; consumed as-is.

### bench-fair.sh internal structure

- `provision <name> <type>` — `hcloud server create`, return IP; register for
  teardown.
- `setup_toolchain <ip>` — apt + Go (matches bench-hetzner.sh).
- `ship_and_build <ip> <target|driver>` — `git archive HEAD` → scp → build only
  what that VM needs (`fanout` on target, `loadgen` on driver).
- `run_step <rate> <dur> <slo-args...>` — driver fires loadgen; returns
  pass/fail + the JSON report; harness samples target growth invariants.
- `summarize` — aggregate steps → ceiling, last-pass, rated capacity, table.
- `cleanup` (trap EXIT INT TERM) — delete BOTH VMs; warn loudly on delete failure.

## Out of scope (YAGNI)

- TLS/Traefik edge path (separate, later, if we want an end-to-end customer
  number).
- Multi-hardware scaling curve (cpx32 → ccx33 → ccx43). The approved run is
  cpx32-only for comparability; the harness should not hardcode cpx32 so a
  future scaling run is a flag change, but we do not build the curve now.
- Changes to `cmd/loadgen`.
- Persisting results to a dashboard or CI gate (the JSON reports are enough for
  now).

## Risks

- **Driver is the bottleneck.** Mitigated by sizing the driver larger (cpx41 8c
  vs cpx32 4c) and by reporting *achieved* server-side rows/s — if achieved rate
  plateaus well below target while fanout CPU is not saturated, the driver is the
  limit and the step is inconclusive (flagged, not silently passed).
- **Ramp step too coarse.** 2× steps may straddle the ceiling widely. Acceptable
  for a first fair number; a bisection refinement between the last-pass and
  first-fail step is a possible follow-up, not built now.
- **Cost / orphaned VMs.** Two VMs for ~1 hour is pennies; the teardown trap
  deletes both on any exit, and the harness prints the server names so a manual
  `hcloud server delete` is trivial if a delete ever fails.
