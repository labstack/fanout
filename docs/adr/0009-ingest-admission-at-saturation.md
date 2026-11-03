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
first export seeds the bucket so cold pressure cannot strand it. Three seconds of sustained
read pressure, or interactive refusals, reduces admission once per second to a
floor of 10% of the recent peak; recovery increases it by 10% per second.
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
with a 20 ms idle cutoff and the existing 64 MiB group bound. Physical row groups
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
