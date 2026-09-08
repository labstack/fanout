# Single-port gRPC comparison — 2026-09-08

## Final implementation: Go's built-in HTTP/2 server

The implementation uses `internal/server.New` with HTTP/1, HTTP/2 over TLS and unencrypted HTTP/2 enabled through `http.Protocols`. It needs no h2c wrapper. The benchmark harness now calls this production factory directly.

All twelve durable-write cases passed: three repetitions each for native and shared gRPC at one and sixteen concurrent exporters. All acknowledgements matched the server row counts, and all Parquet repositories passed verification. The workload, machine and measurement method match the prototype experiment below.

| Exporters | Native rows/s | Shared rows/s | Change | Native p95 | Shared p95 | Native p99 | Shared p99 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | 24,526 | 25,028 | +2.0% | 48.83 ms | 44.98 ms | 50.18 ms | 48.19 ms |
| 16 | 325,896 | 326,300 | +0.1% | 55.38 ms | 54.53 ms | 59.41 ms | 58.53 ms |

Throughput and tail latency showed no regression in these local durable workloads. Treat the small throughput differences as run variation. At sixteen exporters, combined client/server CPU rose from 1.177 to 1.327 cores (12.8%); allocated bytes per accepted row rose from 4,409 to 4,442 (0.8%). The transport is not cost-free. These measurements do not isolate server CPU and do not certify production capacity or performance under concurrent dashboard traffic.

Raw final evidence: [test log](single-port-stdlib-2026-09-08.log), [JSON measurements](single-port-stdlib-2026-09-08.json).

Reproduce the final durable comparison:

```sh
rtk just test '-tags transportbench ./internal/ingest -run ^TestTransportComparison$/durable=true -v -count=1 -timeout=10m'
```

Functional validation also passed: full Go suite, 122 UI tests, 13 focused shared-listener tests under the race detector, lint, dependency notices, release-script tests, UI dependency audits, generated docs, diagram consistency and the documentation build. Those tests cover HTTP and gRPC on one port, plaintext and TLS, credential separation, invalid ingest routes, export draining and forced cancellation of long-lived HTTP/2 streams.

## Original prototype experiment

The following measurements are retained as historical evidence for the earlier `h2c.NewHandler` prototype. The current harness uses the final factory above, so rerunning it exercises the final implementation rather than reproducing that wrapper verbatim.


The local experiment found no durable-ingest throughput or tail-latency regression from using gRPC ServeHTTP behind the proposed h2c dispatcher. Median durable throughput was 1.1% higher with one exporter and 2.2% higher with sixteen; these small differences should be treated as run variation, not a demonstrated speedup. This is local engineering evidence, not a production capacity certification.

## Durable ingest results

Values are medians of three runs. Each latency value is the median of the three per-run percentiles, not a percentile pooled across runs.

| Exporters | Native rows/s | Shared rows/s | Change | Native p95 | Shared p95 | Native p99 | Shared p99 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | 25,171 | 25,450 | +1.1% | 45.81 ms | 44.98 ms | 49.39 ms | 47.98 ms |
| 16 | 323,135 | 330,112 | +2.2% | 56.04 ms | 54.98 ms | 60.27 ms | 58.01 ms |

## All measurements

CPU includes the generator and server in the same process. Allocated bytes are cumulative allocation per accepted row, not retained memory or peak RSS. The non-durable runs count converted rows without storing them, and must not be interpreted as Fanout's durable ingest capacity.

| Durable | Exporters | Mode | Median rows/s | Rows/s range | CPU cores | Allocated bytes/row |
|---|---:|---|---:|---|---:|---:|
| False | 1 | native | 900,673 | 892,355–906,928 | 1.686 | 2,065 |
| False | 1 | shared | 866,387 | 835,664–876,795 | 1.785 | 2,235 |
| False | 16 | native | 4,744,632 | 4,609,333–5,335,230 | 9.113 | 1,961 |
| False | 16 | shared | 5,165,203 | 4,975,016–5,194,013 | 10.292 | 1,982 |
| True | 1 | native | 25,171 | 21,316–25,572 | 0.152 | 3,955 |
| True | 1 | shared | 25,450 | 24,953–25,586 | 0.160 | 3,965 |
| True | 16 | native | 323,135 | 312,194–326,997 | 1.204 | 4,395 |
| True | 16 | shared | 330,112 | 320,596–332,346 | 1.249 | 4,437 |

Without storage, one exporter was 3.8% slower on the shared transport and allocated approximately 8.2% more bytes per row. At sixteen exporters the shared median was higher, but the throughput ranges overlapped. This does not establish a universal transport advantage or penalty. With durable writes and sixteen exporters, combined CPU medians were 1.204 versus 1.249 cores, and allocation per row rose approximately 1.0%.

## Method

- Fanout revision: `9c6567c24a591f233faaaa950a4be2e7912efc32`; production code unchanged.
- Go 1.27.0, darwin/arm64, 14 logical CPUs and GOMAXPROCS=14; grpc-go v1.83.2.
- Real loopback TCP, plaintext gRPC. Native `grpc.Server.Serve` versus `grpc.Server.ServeHTTP` wrapped in `h2c.NewHandler` and the proposed content-type/path dispatcher.
- Same production ingest authentication interceptor, temporary SQLite settings store, and OTLP conversion handlers in both modes. Generated temporary credentials are never logged.
- 1,000-row exports cycling through traces, logs and gauge metric points. Each worker owns a persistent gRPC connection with one outstanding export. Requests reuse the existing small test fixtures; values have low cardinality and are repeated.
- Each connection and all three signals are warmed before timing. Each measured case issues requests for ten seconds, then waits for in-flight acknowledgements; that drain time is included in throughput.
- Three repetitions for each mode at one and sixteen workers, with native/shared order reversed in repetition two. Runs are sequential, with a fresh store for every case.
- Durable mode uses the production writer, default 50,000-row batch target, and a fresh temporary Parquet repository. Counting occurs only after Submit succeeds. Accepted server rows must exactly equal successful exports times 1,000.
- Zero export errors in all 24 cases. Every durable repository passed VerifyBatches after writer shutdown. The earlier four-second smoke matrix also passed, but its timings are not included here.

## Limits and decision

Proceeding with a single-port prototype is supported by these results: the tested durable workloads did not show a performance regression. They do not prove zero overhead or establish equivalence across workloads. There are only three short repetitions, the laptop is not a dedicated benchmark host, and client/server CPU contention and filesystem behavior affect the measurements.

TLS, an actual reverse proxy, remote network latency, concurrent dashboard/API queries, RSS, OTLP/HTTP load, compression, larger or higher-cardinality payloads, sustained saturation and production lifecycle behavior were not measured. The normal app server was not started: its fallback in the isolated dispatcher returns 404. Production auth-boundary and shutdown integration still require their own tests when implementing the feature.

The independent fanout-bench suite was inspected but not used: its managed path requires a provisioned Linux host. Before claiming deployment-wide performance equivalence, repeat the comparison on the target Linux host with representative telemetry and concurrent read load.

## Reproduce

The experiment is build-tagged and excluded from normal tests:

```sh
rtk proxy env GOTOOLCHAIN=go1.27.0 go test -tags transportbench ./internal/ingest -run '^TestTransportComparison$' -v -count=1 -timeout=10m
```

Raw evidence: [test log](single-port-2026-09-08.log), [JSON measurements](single-port-2026-09-08.json), [harness](../../internal/ingest/transport_bench_test.go).
