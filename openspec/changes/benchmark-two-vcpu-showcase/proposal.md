# Benchmark Fanout on two dedicated vCPUs

## Why

The README claims Fanout replaces a fleet of services with one process, but
offers a reader no evidence of what that process actually sustains. Someone
evaluating a self-hosted observability tool needs a number attached to a
machine they can price, and right now the repository provides none.

The claim is also the risky kind. "One binary" invites the assumption that it
must therefore be small, and the honest response is a measurement rather than an
adjective. `cmd/bench` already exists and reports export and query latency, but
nothing has ever run it against a known machine and recorded the result, so its
output has never been published.

Running it exposed a second gap. `cmd/bench` takes a fixed `-rate` and
`-workers`, and the adaptivity that sized them lived in `scripts/`, which is not
part of this repository. Hand-picking a rate measures the guess, and the harness
also enforced a fixed 1500 ms query SLO calibrated on a four-core machine, so
the same binary reports a defect on two cores where there is only a smaller
computer.

This change therefore alters shipped behavior in `cmd/bench`: the default run
mode changes from a fixed rate to an adaptive ramp, and the built-in latency SLO
is removed. It touches no server behavior, no public contract, no data
migration, and no security surface.

## What Changes

- Make `cmd/bench` adaptive by default: size senders from the driver's cores,
  ramp until the server stops keeping up, bisect to find the boundary, then
  confirm at that rate and report the lower of the two.
- Remove the built-in query latency SLO. Report capacity instead, and keep
  hard failures for the conditions that make a run untrustworthy — dropped
  rows, a mid-run restart, a lost metrics baseline, an excessive send-error
  rate — rather than for being slow.
- Run the harness against a Fanout instance on a Hetzner `ccx13` — two
  dedicated vCPUs, 8 GB RAM — with the load generator on a separate machine in
  the same datacenter, so the measurement is not competing with the server for
  the cores being measured.
- Record ingest throughput, ingest export latency, and query latency, together
  with the exact instance types, image digest, flags, and duration that produced
  them.
- Add `docs/benchmarks/two-vcpu.md`: the results, the method, and the
  conditions under which the numbers do not hold.
- Author the report's diagrams in d2 under `docs/diagrams/`, rendered to
  committed SVG by the existing `just diagrams` recipe.
- Link the report from the README so the throughput claim is one hop from the
  landing page.

## Capabilities

### New Capabilities

None. The measured behavior is already specified; this change observes it.

### Modified Capabilities

None.

## Impact

- **Affected**: `docs/benchmarks/`, `docs/diagrams/`, `README.md`.
- **Not affected**: every Go and TypeScript source file, the schema, the
  container image, and the configuration surface. The binary under test is the
  one CI already published.
- Infrastructure is provisioned for the run and destroyed afterwards. Nothing
  in the repository depends on it continuing to exist.
