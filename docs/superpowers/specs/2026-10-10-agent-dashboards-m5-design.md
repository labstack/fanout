# Milestone 5: correct metrics and the system page

Status: draft for review · 2026-10-10 · branch `feat/agent-dashboards-m5`

This extends `2026-10-04-agent-dashboards-design.md`. Milestones 1 to 4 are
merged. Its success criteria S1 to S13 stay in force; this milestone adds S14
to S16, S20 and S22. Performance work (a scale benchmark, per-minute summaries
and metric query speed) is deferred to a later milestone.

## Goal

Fanout shows what is happening in a whole system, not only in its traced
services, and its numbers are right at a realistic scale.

A system is everything whose behaviour decides whether the software works:
the services, what they depend on, where they run, how people reach them, and
what changes. Everything arrives over OTLP through the OpenTelemetry
Collector, using its standard receivers (`hostmetrics`, `docker_stats`,
`httpcheck`) and semantic conventions. Fanout adds no per-source integrations.
It recognises the standard names and presents them.

The front page answers, in this order:

1. Is it reachable?
2. Is anything failing or slow?
3. Is anything running out: disk, memory, CPU?
4. What changed?

Then the user drills into traces and logs, or asks the agent.

## Why now

Two facts from milestone 4 decide the scope.

1. **Metric measures are wrong for counters and histograms.** `rate()` counts
   data points per second, so for a counter it reports how often the Collector
   exports, not traffic. A histogram's `p95` is taken over each data point's
   stored value, not its buckets, so it is not the 95th percentile of the
   observations. The front page's CPU, network and disk panels are counters,
   so it cannot be built on today's measures.
2. **Ingest discards metric meaning.** It stores a cumulative sum as `sum`
   whether it is monotonic or not, so `process.memory.usage` (an up-down
   counter) and `system.network.io` (a counter) are indistinguishable. The
   demo data holds 213 sum metrics of both kinds. Exponential histograms are
   converted with one bound per count instead of one fewer, so every metric
   histogram query silently drops them.
## Success criteria

| ID | Criterion | Target | Evidence |
| --- | --- | --- | --- |
| S14 | Counter rate | `rate()` on a counter equals the per-second increase across resets, export-interval changes, delta temporality and many series | engine-executed golden tests; demo-data cross-check against an independent window query |
| S15 | Histogram percentiles | `pNN`/`quantile` on a metric histogram equals linear interpolation over the summed bucket increases | engine-executed golden tests with known distributions |
| S16 | Metric fidelity | every OTLP metric kind keeps its kind, temporality and monotonicity; exponential histograms keep zero and negative counts | ingest round-trip tests |
| S20 | The system page shows real trouble | through one Collector, the page shows a filesystem above 90%, a failing external dependency, an unreachable endpoint, a check that stopped reporting and a version change within two refreshes | Playwright on a fixture stack; one local run with a real Collector |
| S22 | The agent uses metrics correctly | on host-metric prompts, counters use `rate()`, up-down counters and gauges use `last`/`avg`, histograms use percentiles; S1–S5 still hold | eval harness (public prompts; the sealed holdout is untouched) |

Paid model spend for the milestone stays within $5, all of it in the final
verification.

## What exists today

- `metric_type` is `gauge`, `sum`, `sum_delta`, `histogram`,
  `exp_histogram` or `summary`. Start time and monotonicity are dropped at
  ingest. Histogram panels take a `temporality` option because the data does
  not say.
- Metric histogram panels (`heatmap`, `histogram`) already group by series
  identity and take last-minus-first per bound per period, clamped at zero.
  A counter reset contributes 0 for that period.
- The read caches are the trace caches only (`readCacheVersion` 4). The
  analytical `service_rollup` and `edge_rollup` tables refresh behind a
  watermark. `service_rollup` holds per-minute, per-service span counts, error
  rate, p50 and p95 (not mergeable across minutes), and log and metric counts.
  Health, overview, dependencies, latency, the alert engine and SQL panels
  read it.
- `/` redirects to chat or dashboards. Nothing presents host, container or
  reachability data, although the demo data holds `hostmetrics` and
  `docker_stats` output for one host and 24 containers.

## Approaches considered

**Metric meaning: new columns, or new type names in the existing column.**
Renaming values inside `metric_type` keeps the physical schema, but old format
3 files would carry `sum` with a different meaning, which is silent
misreading. New columns change the physical schema, so the batch format moves
to 4 and format 3 files are refused, which is explicit. The product is
pre-release and the deployed instances already need a wipe, so the cost is
local data only. **Chosen: format 4 with new columns.**

**The system page: a new page, or the existing default dashboard.** Every
user already gets a default dashboard, "System overview", on first visit, and
`/dashboards` opens it. A separate generated page would duplicate it. **Chosen:
the default dashboard gains the system sections, and `/` opens it.**

## Phase A: metric fidelity and series-aware measures

### Storage (batch format 4)

The metrics Parquet schema gains:

| Column | Type | Meaning |
| --- | --- | --- |
| `temporality` | dict string | `cumulative`, `delta`, or empty for gauges and summaries |
| `monotonic` | boolean | true for monotonic sums; false otherwise |
| `start_time_unix_nano` | int64 | the data point's start time; 0 when absent |
| `series_id` | uint64 | a hash of the series identity, computed once at ingest |

`metric_type` becomes the OTLP data kind only: `gauge`, `sum`, `histogram`,
`exponential_histogram`, `summary`. `sum_delta` is removed. Format 4 is the
only accepted format; format 3 files are refused like any unsupported format,
with no reader or conversion.

Exponential histograms convert to explicit bounds correctly: the negative
buckets keep their own negative bounds below the zero bucket, and counts have
exactly one more entry than bounds. The conversion keeps every observation's
count.

A sum or histogram point with unspecified temporality is invalid OTLP. The
export is rejected with `InvalidArgument`, as ingest already does for other
invalid input.

### The metrics view

The view's `type` column becomes the semantic kind: `gauge`, `counter`
(monotonic sum), `updown` (non-monotonic sum), `histogram` (both explicit and
exponential) or `summary`. It adds `temporality` and `start_time`.
`get_telemetry_schema` lists each metric's kind and temporality, so the agent
chooses measures from facts.

### Series and increases

A series is one combination of namespace, service, name, unit, scope name,
scope version, attributes and resource, as metric histogram panels already
use. Ingest hashes that identity into `series_id` from a canonical encoding
(sorted keys, typed values), so queries partition by one integer instead of
comparing attribute maps on every row. Metric measures work in two stages, the way PromQL's
`sum by (x) (rate(m[1m]))` does: first per series and bucket, then across the
series in each group.

The increase of a cumulative series between consecutive points is
`current - previous`, or `current` when the value decreased or the start time
changed (a reset). Each increase belongs to the bucket of its later point. The
query reads one lookback period before the window, 10 minutes, so the first
bucket has its previous point. A delta series' increase is its value. A series
first seen inside the window contributes from its second point. Histograms
apply the same rule per bound, with a reset detected on the total count.

### Measures on metrics

| Measure | gauge, updown | counter | histogram |
| --- | --- | --- | --- |
| `count()` | data points | data points | data points |
| `rate()` | not defined | Σ series (increase ÷ elapsed) | Σ series (count increase ÷ elapsed) |
| `sum(value)` | Σ series bucket average | Σ series increase | Σ sum increase |
| `avg(value)` | mean of series bucket averages | not defined | Σ sum increase ÷ Σ count increase |
| `min`/`max(value)` | over data points | over data points | not defined |
| `last(value)` | Σ series value at bucket end | Σ series value at bucket end | not defined |
| `pNN`/`quantile(value, q)` | over data points | not defined | linear interpolation over Σ bound increases |

Each delta between consecutive points of a cumulative series covers the
interval (previous time, time] at the rate increase ÷ elapsed. A series' rate
in a bucket is the time-weighted mean of that rate over the part of the bucket
its deltas cover, and its increase in a bucket is each delta's increase in
proportion to the overlap. Every series therefore contributes to every bucket
its reporting interval spans: a counter exported every 60 s reads the same
per-second rate in 10 s, 1 m and 1 h buckets, and a total across machines
that report every minute, every five minutes, or at drifting times does not
dip. Histogram count, sum and per-bound increases are spread the same way. A
delta longer than the 10-minute lookback is a gap in the series: it is not
spread, and its whole increase belongs to the bucket of its later point, at
the rate increase ÷ elapsed. That bounds the spread to the buckets ten
minutes can span, however sparse a series is. For
`rate()` only, a series whose latest delta ends before a bucket carries that
rate into the bucket while the delta's end is at most 5 minutes old, so the
newest buckets of a live chart do not dip while reports are in flight;
increases are never extended.
Gauge and updown series in a bucket without a point use their latest earlier
value when it is at most 5 minutes old, the staleness rule `last` uses. The
automatic bucket for metric panels is never shorter than 1 minute.

A series' value at a bucket's end is its latest point strictly before the
earlier of the bucket end and the window end (both ends are exclusive, so a
point exactly on a boundary belongs to the next bucket), and the series counts only if
that point is at most 5 minutes old, as Prometheus treats staleness. A
container that stopped or restarted therefore stops adding to `last` after
5 minutes, and a stat over a whole day shows what is reporting now.

A measure that is not defined for the series' kind returns no value, and the
empty-panel diagnosis names the kind and the measures that fit, for example
"rate() needs a counter; system.memory.usage is an updown; use last(value) or
avg(value)". OTLP summary metrics have no aggregate measures, because their
stored quantiles cannot be combined.

Validation adds one rule: an aggregate metric query with any measure other
than `count()` or `count_distinct()` must fix the metric per group, with a `name = ...` filter on one value or `name`
among `by`. A `name = $var` filter counts only when the variable is
single-valued and has no All option, because All drops the filter. Different
metrics never sum into one number.

Histogram quantile interpolation follows Prometheus: the lowest bucket's lower
bound is 0 when its upper bound is positive, and an observation in the
overflow bucket returns the highest finite bound.

Histogram and heatmap panels use the same increase rule. Their `temporality`
option is removed, because the data now carries it.

### Phase A changes outside the panel compiler

- The agent's spec guide and examples teach metric kinds and the measure
  table, including one counter-rate and one histogram-percentile example.
- The dashboards guide documents metric kinds and measures.
- Outside the repository, the private benchmark replay restores monotonicity
  for the stored demo data from OpenTelemetry's semantic conventions, because
  that source lost it. The committed demo-data cross-check runs when that data
  is supplied and skips otherwise, like milestone 4's read benchmarks.

## Phase B: the system page

The system page is the existing default dashboard, "System overview"
(`internal/dashboard/defaults.go`), extended with the sections below. `/`
always opens it (through `/dashboards`), whether or not an agent is
configured. There is no separate page, route or rail entry. It stays an
ordinary saved dashboard: run through the panel executor in one batch request,
rendered by the dashboard renderer, editable by its owner. A panel whose
receiver sends no data shows the empty state, and its description names the
Collector receiver that supplies it, linked to the setup guide.

Sections, in the page's question order:

1. **Reachable.** From `httpcheck` metrics: a state timeline per endpoint and
   a latency timeseries. Each check names where it ran from, the reporting
   Collector's `host.name`. A check with no result for two of its intervals
   shows "no recent result" in the bad state, never a blank: silence is not
   health. A check run on the same machine as the services cannot see DNS,
   CDN or proxy failures that outside visitors hit, so the section says when
   every check runs on a host that also runs observed services.
2. **Failing or slow.** The health panel and the service map, which already
   show external dependencies from client spans.
3. **Running out.** Two tables, worst first, with warning at 80% and bad at
   90%. Hosts: CPU busy from `system.cpu.time` rates by state, memory used
   from `system.memory.usage` by state, and the fullest filesystem from
   `system.filesystem.usage` by mountpoint. Containers: CPU from
   `container.cpu.utilization`, memory as `container.memory.usage.total` over
   `container.memory.usage.limit`. These are SQL panels written by the server
   over the metrics view, using the Phase A increase and latest-value rules.
   A host or container with no data for two of its reporting intervals shows
   "not reporting" in the bad state: Phase A carries a stopped series in
   totals for up to 5 minutes so totals do not dip, and this row is what
   makes a crash visible at once.
4. **What changed.** Deploy markers on the health trend, and a versions table:
   service, `service.version` and when it was first seen, runtime name and
   version, and SDK language and version, from resource attributes.

The documentation gains a guide to watching a system: a Collector
configuration with `hostmetrics`, `docker_stats`, `httpcheck` and OTLP
forwarding, and what each section needs. It recommends running `httpcheck`
from a second Collector on a machine outside the observed stack, and states
the limit plainly: if the machine running Fanout fails, nothing on the page
can report it, so Fanout itself still needs an outside monitor.

### The self-observation test

The page is right if it would have shown the problems a real stack had. S20's
fixture stack replays the demo plus six scenarios through OTLP: a filesystem
filling past 90%, an external dependency returning errors, an `httpcheck`
target going down, a check that stops reporting, a host that stops
reporting, and a new `service.version`. Playwright checks that each
appears in its section within two refreshes. Once, on a development machine, a
real Collector observing real local containers confirms the same page.

## Phase C: final verification

S1 to S16, S20 and S22 run on the final commit: the eval harness (within $5,
with new public host-metric prompts for S22), the browser collector, the
Playwright smoke, accessibility and performance tests at demo volume, and the
demo-data metric cross-check. Results go into
`docs/benchmarks/2026-10-agent-dashboards-m5.md`.

## Testing

- Every SQL-producing change is tested by executing on the pinned engine,
  not by comparing SQL text.
- Counter and histogram logic have golden tests with known answers:
  resets, interval changes, delta series, late and out-of-order points,
  series first seen inside a window, empty buckets and overflow buckets.
- The fixture-stack e2e test runs in CI. The real-Collector run is recorded
  in the results document.

## Out of scope

Alert rules created from system-page sections, incidents and notifications,
reachability checks run by Fanout itself (a Collector with `httpcheck` on an
outside machine covers them), cost and usage tracking, vulnerability matching for runtime
versions. Each is designed separately if the page shows it is needed.

Performance is deferred to a later milestone: a benchmark at a realistic
volume (1,000 to 5,000 spans per second), per-minute summaries for spans and
logs, and making metric queries faster. Phase A's correct per-series metric
math costs 1.5 to 2.6 times the old per-point math on the measured shapes.

## Risks

- **Lookback cost.** Reading 10 minutes before the window adds work to every
  counter query.
- **Histogram percentile approximations.** Interpolation is linear within a
  bucket, as in Prometheus. Two known effects: a quantile that falls in an
  exponential histogram's zero bucket interpolates across
  (−threshold, threshold], so positive-only data can read slightly below 0, by
  at most the zero threshold (usually 0); and series with different bucket
  layouts in one group are combined on the union of their bounds, which can
  lean a quantile upward by up to one coarse bucket width.
- **Metric query cost.** Phase A's per-series math costs more than the old
  per-point math, and its speed work is deferred with the rest of performance. Known
  costs: decoding and comparing exponential histogram bucket lists per point
  (about 600–740 ms at one hour for 160-bucket series), gauge and counter
  panels also scanning for histogram rows, and stat panels running the series
  builder for both the trend and the totals.
- **Format break.** Format 4 refuses existing local data. Deployed instances
  are already on a release whose data needs a wipe before upgrading.
