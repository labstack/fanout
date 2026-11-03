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
rate is the accepted count over the preceding second. With no history, the
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
Below the debt band admission is uncapped unless reads require a cap.

At the debt limit refuse every new export with a two-second retry delay. Other
rate refusals carry the estimated token wait, clamped to one through five
seconds. OTLP/gRPC returns RESOURCE_EXHAUSTED with RetryInfo; OTLP/HTTP returns
429 with Retry-After. The existing decoded-byte budget remains a separate bound.
Already admitted exports drain and preserve durable acknowledgement semantics.

Reserve max(1, slots/6) read slots for interactive requests, with interactive
waiters served first on releases. Panels queue up to eight times the slot count;
interactive requests queue up to twice it. A one-slot pool has no reservation,
so panels remain usable. The engine still retains its two separate connections
for other readers where the pool permits. No CPU reservation is introduced.

Under debt pressure group commits target 200,000 rows and a one-second window,
with the existing 64 MiB group bound. Sparse retries keep the full bounded
window rather than closing after an idle gap, so throttling does not multiply
small files and their read and compaction overhead. Physical row groups
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
