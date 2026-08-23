# Benchmarking Fanout

`cmd/bench` is the supported load generator. It sends traces, metrics, and logs,
ramps to find the ingest boundary, and confirms the result at the selected rate.
It can also add authenticated dashboard reads.

Fanout does not currently publish a throughput headline. The earlier two-vCPU
report was withdrawn before the first public release because its raw JSON reports
and exact driver commit were not retained, and its ingest run predated mandatory
ingest authentication. The prose was detailed, but detail is not provenance.

A future published result must include:

- the Fanout image digest and benchmark-driver commit;
- complete machine, network, container, and Fanout configuration;
- raw `ingest.json` and `mixed.json` reports from the same run;
- an authenticated OTLP path matching the current release;
- repeated runs or an explicit single-run limitation;
- separate ingest-only and mixed read/write results.

For local capacity testing, build the driver with `just build`, create an ingest
token through first-admin setup, and run:

```sh
./bin/bench \
  -endpoint localhost:4317 \
  -token "$INGEST_TOKEN" \
  -report ingest.json
```

Use `./bin/bench -help` for fixed-rate, query-load, and metrics options. Results
describe the tested hardware and dataset, not an equivalent vCPU count on every
provider.
