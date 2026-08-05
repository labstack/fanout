## 1. Make the harness adaptive

- [x] 1.1 Add a ramp policy as pure functions so it can be tested without a server: seed rate and worker count from the driver's cores, double while delivery holds, stop at a latency knee or the step limit. Evidence: `cmd/bench/adaptive.go`, `cmd/bench/adaptive_test.go`.
- [x] 1.2 Bisect between the last sustained rate and the first missed one instead of stopping at the first miss. A doubling ramp that halts on one noisy step understates capacity by up to half: measured 1991/s and 3946/s on the same machine minutes apart before this. Evidence: `bracket`, `decideNextStep`, `TestDecideNextStepBisectsInsteadOfStoppingAtTheFirstMiss`.
- [x] 1.3 Raise the delivery floor to 0.95. At 0.90 a step delivering 90.8% was reported sustainable at 7267/s and the confirmation pass held only 6503/s. Evidence: `defaultRampPolicy`, and the comment recording the measurement.
- [x] 1.4 Confirm the discovered rate with a full pass and report the lower of ramp estimate and confirmed rate, because capacity falls as the dataset grows. Evidence: `capacityReport`, ramp estimate 5810/s vs confirmed 4372/s in the run that motivated it.
- [x] 1.5 Remove the built-in 1500 ms query SLO while keeping every integrity failure. Evidence: `cmd/bench/verdict.go`, `TestEvaluateReportDoesNotJudgeLatencyWithoutACallerThreshold`.
- [x] 1.6 Verify the whole harness against a local Fanout before trusting it remotely. Evidence: local run self-sized to 224 workers on 14 cores, ramped 3500→56000/s, reported 27,875/s sustainable and confirmed 27,865/s.

## 2. Measure

- [x] 2.1 Provision the machine under test (`ccx13`, two dedicated vCPUs) and the load driver (`cpx22`) in the same datacenter, on a private network. Evidence: Hetzner `nbg1`, private network `10.10.1.0/24`.
- [x] 2.2 Pull the published image and record its digest rather than its tag. Evidence: `ghcr.io/labstack/fanout@sha256:feebc9cf...`.
- [x] 2.3 Measure ingest on a clean instance. Evidence: `ingest.json` — 5,138 traces/s sustained, 22,612 rows/s, export p95 3 ms, 0 dropped, 1.10 of 2 cores.
- [x] 2.4 Seed a time-spread dataset and measure query latency under concurrent ingest. Evidence: `mixed.json` — 1.31 M rows over 2 h, 2,718 traces/s with 5 q/s, query p95 2031 ms overall, `overview` p95 38 ms.
- [x] 2.5 Record the operational findings the runs exposed: the OOM at 7.5 GB RSS with `DUCKDB_MEMORY` unset, capacity declining as the lake grows, and backfill costing 832 traces/s against 5,138 with 9.37 GB on disk for ~660 k rows.

## 3. Report

- [x] 3.1 Write `docs/benchmarks/two-vcpu.md` with the results, the method, and what the numbers do not cover. Evidence: the file, including its "What this does not tell you" section.
- [x] 3.2 Author the report's diagrams in d2 and render them. Evidence: `docs/diagrams/benchmark-setup.d2`, `docs/diagrams/benchmark-results.d2`, rendered via `just diagrams`.
- [x] 3.3 Link the report from `README.md`.

## 4. Verify and clean up

- [x] 4.1 Confirm every number in the report traces to a captured report file rather than to recollection. Evidence: `ingest.json` and `mixed.json`; the operational findings cite the runs that produced them.
- [x] 4.2 Destroy both machines and the private network, and confirm nothing else was touched. Evidence: `hcloud server delete` for both, network deleted, the five unrelated servers still running.
- [x] 4.3 Run `just check` and confirm the change validates.
