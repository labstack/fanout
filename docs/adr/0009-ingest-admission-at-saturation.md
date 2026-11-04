# 0009. Ingest admission at saturation

Status: Accepted
Date: 2026-11-03

## Context

Blocking the ingest writer at the compaction debt limit leaves decoded exports
queued until their client deadlines. Under sustained overload this can kill
exporters and consume CPU needed to retire debt. A shared FIFO for panels,
exemplars and trace detail also lets long panel queues exhaust drill deadlines.

## Decision

Reject whole exports before converting protobuf items into storage rows. A
rows-per-second token bucket adapts every 250 ms. At half the unchanged 1,024
reclaimable-batch limit, rising debt reduces the rate by 15%; falling debt
increases it by 10%, bounded by 1.5 times the recent 60-second peak. The entry
rate uses the preceding second and the recent measured peak, so retry gaps
cannot seed a warm store at one row per second. The band cap stops decreasing
at one normal batch per pressure window (50,000 rows/s), or the measured entry
rate if lower. Below that, further row cuts shrink files without reducing their
creation frequency. The hard debt stop remains unconditional. With no history, the
first export seeds the bucket so cold pressure cannot strand it. Rising interactive
refusals (including queue-wait timeouts) indicate starvation for three seconds
after the latest refusal. Panel queueing alone never reduces ingest. Starvation
reduces a separate read cap once per second to 50% of the measured peak before
starvation; recovery increases it by 10% per second. Freeze that reference during
the incident so a long incident cannot erase its own floor. Half the healthy
rate bounds the throughput sacrificed to read feedback while leaving room for
reads to recover. Continued starvation at that floor requires measuring read
cost; a lower ingest rate requires compaction debt, not just read demand. Apply
the smaller of the independent debt and read caps so the read floor cannot
defeat debt protection.
Read-driven refusals are bounded in time as well: after the first refusal,
allow them for at most one second, then suspend the read cap for ten seconds.
Both the [Collector exporter helper](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/exporter/exporterhelper/internal/retry_sender.go)
and Go OTLP exporters wait for the greater of the server delay and exponential
backoff. Their default initial backoff is five seconds with 50% jitter. Every
first retry from the entire refusal window therefore lands in the open interval,
with 2.5 seconds of scheduling/transport margin, within telemetrygen's configured
30-second export timeout. Reducing RetryInfo alone does not shorten that client
backoff. A percentage rate floor alone also
cannot prevent the same exporter losing repeated token-bucket races. The
independent debt cap, hard debt stop and in-flight byte budget still apply during
the open interval. This favors exporter progress over prolonged read-driven
ingest reduction; admission priority must protect interactive latency.

Below the debt band admission is uncapped unless reads require a cap.

At the debt limit refuse every new export with a two-second retry delay. Other
rate refusals carry the estimated token wait, clamped to one through five
seconds. OTLP/gRPC returns RESOURCE_EXHAUSTED with RetryInfo; OTLP/HTTP returns
429 with Retry-After. The existing decoded-byte budget remains a separate bound.
Already admitted exports drain and preserve durable acknowledgement semantics.

Reserve max(1, slots/6) read slots for interactive requests, with interactive
waiters served first on releases. Under contention retain only one panel
progress slot: a running panel satisfies it, so its sibling cannot prevent an
interactive replacement taking the next released slot. A refresh runs at most
two siblings concurrently and holds a bounded lifetime ticket while accepted;
idle or coalesced work holds no global slot. Panels queue within that bounded
refresh population; interactive requests queue up to twice the slot count. A one-slot pool has no reservation,
so panels remain usable. The engine still retains its two separate connections
for other readers where the pool permits. No CPU reservation is introduced.

Under debt pressure group commits target 200,000 rows and a one-second window,
with the existing 64 MiB group bound. Sparse retries below 50,000 total
rows keep the full bounded window rather than closing after an idle gap, so
throttling does not multiply small files and their read and compaction overhead.
Once a normal-size 50,000-row batch is available, a 20 ms idle gap releases
closed-loop exporters to send again without waiting the full second. Physical row groups
remain 50,000 rows. Compaction continues under read pressure without algorithmic
changes or a higher debt limit.

## Consequences

Senders need retries enabled and must honor the protocol's delay. Retry-disabled
exporters can drop refused work. Admission is feedback control, not a guarantee
that arbitrary offered load or arbitrary-size exports meet a client deadline.
The hard stop remains a safety valve; bounded backlog and read latency require
load measurements. Larger groups can retain more decoded memory, still within
the in-flight byte budget. Interactive floods can reduce panel throughput.

The backpressure-seconds metric now measures time with an admission cap active,
not time blocked in the writer. Historical values across this change are not
directly comparable. Throttling reasons, current rate, compaction pass duration
and retired inputs expose whether the controller and compactor are keeping up.
