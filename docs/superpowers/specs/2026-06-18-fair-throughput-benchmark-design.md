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

Both numbers are reported in **achieved server-side rows/s** (the
`fanout_ingest_rows_total` delta over wall-clock), not in the generator's target
traces/s — see "Units" below.

## Units — traces/s vs rows/s (do not conflate)

`loadgen`'s `-rate` is in **traces/s**, but one trace tick emits multiple
ingested rows: ~2 spans always + a messaging producer/consumer pair for a
`-messaging-ratio` fraction (≈ +0.3 spans at 0.15), **plus** one log and one
metric per tick. So at the default data shape, **≈ 4.3 rows per trace**
(`2 + 0.15·2 spans + 1 log + 1 metric`). The headline metric is **rows/s**, so:

- All targets/ceilings are stated and compared in **achieved rows/s**, derived
  from the `fanout_ingest_rows_total` delta — never from the loadgen target rate.
- A step is **saturated** when achieved rows/s falls below ~95% of its target
  (target_traces/s × rows-per-trace) — i.e. the server can't keep up — *or* when
  an SLO breaks, whichever comes first.

The prior burst run is the anchor: it *achieved* ~64K rows/s (≈ 15K traces/s)
while *targeting* 200K traces/s — proof that target rate is meaningless under
saturation and only achieved rows/s is comparable.

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

- **Fanout-under-test:** `cpx32` (4 vCPU / 8 GB) — *same hardware tier as the
  prior burst run*. Note this is a fundamentally **fairer measurement**, not a
  clean single-variable comparison: topology, flush config (15s/60s vs 5s/15s),
  duration (15 min vs 30s), and added query load all differ. Expect the number
  to move for several reasons at once; do not frame the delta as "the 64K figure
  was wrong by X."
- **Load driver:** a separate `cpx41` (8 vCPU / 16 GB) — more cores than the
  target so the generator is never the bottleneck and fanout keeps all 4 of its
  cores. This removes the co-location flaw.

### Networking

Both VMs join a **Hetzner private network** (`hcloud network create`, same
location, sub-ms intra-DC latency) and the driver targets fanout's **private
IP**. This keeps WAN RTT out of the export-p95 and query-p95 SLO measurements —
the gate must measure fanout, not the internet. The harness opens nothing on the
public interface for the test traffic; `:4317` (ingest) and `:7520`
(metrics + query) are reached over the private network only. (Hetzner has no
cloud firewall by default, but binding the test path to the private IP is both
faster and cleaner.)

Ingest path: **direct gRPC** to the target's `:4317` (insecure). We are measuring
engine capacity, not the edge; TLS/Traefik overhead is explicitly out of scope
for the headline number.

### Driver concurrency

`loadgen`'s send loop is **synchronous per worker** (each worker blocks on the
gRPC `Export` round-trip), so per-worker throughput is RTT-bound and a single
low-worker process cannot push the higher steps. The driver must therefore run
with high concurrency: a single `loadgen` invocation with **`-workers 48`** (12×
the target's 4 cores), scaling workers up if a step's achieved rate plateaus
below target while the *target's* CPU is not yet saturated (the
driver-bottleneck signal). The driver is sized larger (8 cores) precisely so
this concurrency is cheap on its side.

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
3. **Ramp** — steps are chosen to **bracket the known ~15K traces/s (~64K rows/s)
   region** on cpx32, not to overshoot it: `6000 9000 12000 16000 20000 28000`
   traces/s (≈ 26K → 120K rows/s target). For each step the driver runs `loadgen`
   for ~3 min with the SLO gate armed and query load on:
   ```
   loadgen -endpoint <target-private-ip>:4317 -rate <step> -duration 3m -workers 48 \
     -services 50 -attr-cardinality 200 -error-rate 0.05 -messaging-ratio 0.15 \
     -metrics-url http://<target-private-ip>:7520/-/metrics \
     -query-url http://<target-private-ip>:7520 -query-workers 4 -query-rate 20 \
     -max-query-p95-ms 1500 -report step-<step>.json
   ```
   For each step the harness records **achieved rows/s** (the
   `fanout_ingest_rows_total` delta ÷ step duration) and samples rollup age /
   partitions / RSS between steps (signals loadgen's own report does not gate on).
   A step **passes** iff every SLO holds AND achieved rows/s ≥ 95% of target
   (below that with the target's CPU unsaturated ⇒ driver bottleneck ⇒ step is
   **inconclusive**, bump `-workers` and retry, do not silently pass). The
   **ceiling** is the achieved rows/s of the first failing (not inconclusive)
   step; the **last passing step**'s achieved rows/s is carried forward.
   If even the first step (6000) already fails, restart the ramp lower; if the
   top step (28000) still passes, extend upward — the harness must not cap the
   ceiling silently.
4. **Certify** — a **15-min sustained soak** at **~80% of the last-passing step's
   achieved rows/s** (converted back to a traces/s target via the ÷4.3 factor),
   query-under-load on, full SLO gate. Passing this is the **rated capacity**
   (reported as achieved rows/s over the soak).
5. **Report** — print a summary table: per-step target traces/s, achieved rows/s,
   pass/fail/inconclusive, plus the ceiling, rated capacity, and headroom ratio
   (ceiling ÷ rated). Keep the per-step JSON reports. Tear down both VMs and the
   private network.

## Components

- **`scripts/bench-throughput.sh`** — the new harness. Orchestrates provisioning,
  shipping, the ramp loop, the soak, result aggregation, and teardown. Reuses
  existing `loadgen` flags; **no Go changes**. Modeled on `bench-hetzner.sh`
  (provision/trap/ship/build) + `soak.sh` (between-sample growth invariants),
  extended to two VMs and rate-stepping.
- **`just stress throughput`** — a new subcommand in the `justfile` stress dispatcher
  routing to `scripts/bench-throughput.sh`.
- **`cmd/loadgen`** — unchanged; consumed as-is.

### bench-throughput.sh internal structure

- `make_network` — `hcloud network create` (+ subnet) in the run location;
  register for teardown.
- `provision <name> <type>` — `hcloud server create --network <net>`, return the
  **private IP** (and public IP for our SSH/control plane); register for teardown.
- `setup_toolchain <ip>` — apt + Go (matches bench-hetzner.sh), over the public IP.
- `ship_and_build <ip> <target|driver>` — `git archive HEAD` → scp → build only
  what that VM needs (`fanout` on target, `loadgen` on driver).
- `run_step <target_traces> <dur> <slo-args...>` — driver fires loadgen at the
  target's **private IP**; returns pass/fail/inconclusive + the JSON report +
  achieved rows/s; harness samples target growth invariants between steps.
- `summarize` — aggregate steps → ceiling, last-pass, rated capacity (all in
  achieved rows/s), headroom ratio, table.
- `cleanup` (trap EXIT INT TERM) — delete BOTH VMs **and the private network**
  (network delete after servers detach); warn loudly on any delete failure and
  print the resource names for manual cleanup.

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
  vs cpx32 4c), driving with `-workers 48`, and reporting *achieved* server-side
  rows/s — if achieved rate plateaus below 95% of target while fanout CPU is not
  saturated, the step is **inconclusive** (bump workers and retry; never silently
  passed).
- **Ramp step too coarse.** The ~1.4–1.5× steps around the known ceiling are
  tighter than the old 2× steps, but may still straddle it. Acceptable for a
  first fair number; a bisection refinement between last-pass and first-fail is a
  possible follow-up, not built now.
- **Orphaned cloud resources.** Two VMs + one network for ~1 hour is pennies; the
  teardown trap deletes all three on any exit (servers first, then network), and
  the harness prints every resource name so manual `hcloud ... delete` is trivial
  if a delete ever fails.

## Results (2026-06-19)

First clean end-to-end run. Two VMs `cpx32` ×2 (4 vCPU / 8 GB each), driver +
target on a private network, direct gRPC. **Code under test: `f28cad2`** (the
DuckLake expire-grace race fix). NOTE: the run banner printed HEAD `1e8cea7`
because the summary re-reads `git HEAD` at the end rather than capturing the
SHA shipped at launch — a harness bug to fix; `git archive` shipped `f28cad2`.

| Step (tr/s) | Achieved rows/s | Verdict | Why |
|---|---|---|---|
| 6000  | 25,403 | **pass** | all SLOs held |
| 9000  | 34,403 | inconclusive | achieved < 95% of target — same-size driver caps generation |
| 12000 | 30,769 | fail | **query p95 2000ms > 1500ms** (drops=0, query errors=0) |
| soak 4726 (15 min) | 17,465 | **fail** | **query p95 5000ms > 1500ms** (drops=0) |

- **Ceiling ≈ 30,769 rows/s** — the rate at which query-P95-under-load first
  breaches the 1.5s SLO. Not a crash, not drops (drops=0 everywhere), not the
  race (0 "Cannot open file" all run — the `f44f7e6` fix held).
- **Rated capacity: not certified.** The soak FAILED at a modest ~17.5k rows/s
  on query p95 = 5s, so the SLO-bound sustainable rate is *below* 17.5k.

### Root cause of the query-latency limit (the real finding)

Ingest is not the bottleneck — at 17.5k rows/s ingest was trivial (export p95
8ms, `ingest_queue_depth=0`, drops=0, 15.8M rows accepted). The limiter is the
**rollup**: `avg rollup ≈ 14–20s under load`. Rollups run every
`ROLLUP_EVERY=60s` (prod default), hold `writeMu`, and contend for the
4-connection DuckDB pool (`DUCKDB_MAX_CONNS=4`); concurrent `Overview` queries
queue behind them, so query p95 spikes to 5s on every rollup cycle. Ingest also
visibly stalls (~20s, no drops) while maintenance holds `writeMu`.

**Implication:** raising the rated capacity is a *query/rollup-contention*
problem, not an ingest-throughput problem. Candidate work: make rollups
incremental (not full-window recompute), shorten or drop the `writeMu` critical
section for rollups, run rollups/queries on separate DuckDB connections, or cap
rollup cost. This is the lever — not ingest.

### vs the ~64k burst anchor

Not comparable: the 64k figure was ingest-only (no query gate), aggressive
5s/15s flush, four loadgen processes sharing cores with fanout. This fair run
shows ingest is comfortably strong (~30k rows/s on 4 shared cores while *also*
serving queries at prod 15s/60s cadence), but surfaces that query-latency-under-
load — gated by rollup cost — is the true product limit.

### Follow-ups surfaced
- **Perf (high value):** rollup cost (~20s) gates query-latency-under-load.
- **Harness:** summary banner re-reads `git HEAD` at end → mislabels the shipped
  SHA; capture `git rev-parse HEAD` once at launch instead.
- **Ceiling needs a bigger driver:** same-size cpx32 driver caps generation
  (~8k tr/s); the true ingest ceiling is above 30.7k. Use multiple loadgen
  processes or a larger driver to push past it.
- **Hardening `1e8cea7` (cleanup grace + query-timeout clamp) not yet
  live-validated** — wouldn't change this result (limiter is rollup cost).

## Results — after fixes (2026-06-19, HEAD 7472ff3)

Iterated fixes, each validated by re-running the benchmark:

| Fix | Commit | Effect |
|---|---|---|
| DuckLake snapshot-expiry grace | f44f7e6 | stops most expiry-side file races |
| Bound rollup agg scans to affected bucket range | cc16454 | rollup 14–20s → **2.6–3.2s at small data**; query p95 5s → 233ms early |
| Read-retry on transient "No such file" | 7472ff3 | race fully absorbed — **0 file-not-found**, ramp now reaches the soak |
| Harness: forensics capture, banner SHA, lg_fail, ssh key | several | runs are diagnosable + reproducible |

**Stability: resolved.** The earlier "crash" (connection refused, negative
counter) does NOT recur. Final run: fanout alive serving 200s to the end, no
panic/fatal, no OOM (1.6 GiB / 7.6 GiB), 0 query errors.

**Remaining limit (architectural): within-day scan growth.** rollup + Overview
raw-span error queries filter recent minutes but can't prune WITHIN a day —
`merge_adjacent_files` rewrites the day's parquet into wide-`start_time`-range
files, so DuckLake's file/row-group zonemaps can't skip to the recent window.
So both grow with accumulated daily volume:

| step (early→late, day=19 filling) | avg rollup | query p95 |
|---|---|---|
| 6000 (small data) | 3.2s | 233ms ✅ |
| 9000 | 6.4s | 1000ms |
| 12000 | 14.4s | 5000ms ❌ |
| soak 4670 (low load, large day) | **18.7s** | 5000ms ❌ |

The 18.7s rollup at LOW load proves it's dataset-size-driven, not rate-driven.
A ~18s rollup every 60s on 4 cores contends with concurrent Overview queries →
p95 5s. In prod this means query latency degrades through a UTC day and resets
at midnight.

**Ceiling ~27k rows/s, rated capacity still uncertified** — gated by query p95
under within-day contention, not ingest (ingest fine: drops=0, export p95 ms).

### Candidate fixes for the within-day limit (architectural — needs decision)
- **Finer partitioning** (hour, or day+hour) so recent-window scans prune to 1–2
  partitions instead of the whole day. Highest leverage; storage-layout change.
- **Keep recent data in small, time-local files** (cap merge target size / don't
  merge the newest hour) so zonemaps stay tight — balances against the file-count
  OOM that motivated merge.
- **Sort/cluster parquet by start_time** within merged files so row-group
  zonemaps prune even inside a day.
- Move Overview recent/top-errors off raw-span scans (needs status_message in a
  rollup/index).
