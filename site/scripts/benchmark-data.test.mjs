import { test } from "node:test";
import assert from "node:assert/strict";
import { deriveBenchmark } from "./benchmark-data.mjs";
import { benchmark, sustainedSeries } from "../src/data/benchmark.ts";

function fixture() {
  const trial = (signal, repetition, rate, cpu) => ({
    settings: { name: `${signal}-p1-r${repetition}`, kind: "sweep", signal, repetition, processes_per_signal: 1 },
    elapsed_seconds: 10, accepted_items_per_second: rate,
    accepted: { spans: 200, log_records: 300, metric_points: 500 },
    resources: { average_cpu_cores: cpu, peak_rss_bytes: 2 ** 30 },
    generator_cpu: { average_cpu_cores: cpu / 2 }, classification: "target-sustained",
  });
  const sustained = trial("mixed", 1, 100, 4);
  sustained.settings = { ...sustained.settings, name: "mixed-sustained", kind: "sustained" };
  const report = { schema_version: "fanout-bench/v1", build: {
    fanout_version: "v2026.10.8-alpha", fanout_artifact: `https://example.test/artifact#sha256=${"a".repeat(64)}`,
    harness_commit: "b".repeat(40), telemetrygen_version: "v0.157.0",
  }, host: { description: "Hetzner ccx33", generator_description: "Hetzner ccx33",
    cpu_model: "AMD EPYC", logical_cpus: 8, memory_bytes: 32e9 }, settings: { repeat: 2, batch_size: 1000 },
  trials: [sustained, ...["traces", "logs", "metrics", "mixed"].flatMap(s => [trial(s, 1, 80, 2), trial(s, 2, 120, 6)])],
  summary: { accepted_total: { spans: 2000, log_records: 3000, metric_points: 5000 },
    quality_gates: [{ name: "run-completed", passed: true }], passed: true } };
  const sample = (time, spans, logs, metrics) => ({ trial: "mixed-sustained", at_seconds: time,
    ingest_rows_total: { spans, logs, metrics } });
  // Unequal intervals include a 62.5 ms counter jump. Boundary samples must
  // not supply a baseline or a point outside the measured phase.
  const samples = [sample(-0.125, 10, 20, 30), sample(0.5, 100, 200, 300),
    sample(2.5, 140, 280, 420), sample(4.5, 180, 360, 540),
    sample(5.4375, 190, 380, 570), sample(5.5, 200, 400, 600),
    sample(5.5625, 400, 800, 1200), sample(7.75, 600, 1200, 1800),
    sample(9.5, 700, 1400, 2100), sample(11, 1000, 2000, 3000)];
  return { report, samples };
}
const derive = ({ report, samples }, phase) => deriveBenchmark(report, samples, "https://example.test/evidence", phase);

test("trailing five-second rates start at the first full window and use the latest eligible baseline", () => {
  const result = derive(fixture());
  assert.deepEqual(result.sustainedSeries, [
    { at_seconds: 5.5, interval_seconds: 5, spans: 20, logs: 40, metrics: 60 },
    { at_seconds: 5.5625, interval_seconds: 5.0625, spans: 59, logs: 119, metrics: 178 },
    { at_seconds: 7.75, interval_seconds: 5.25, spans: 88, logs: 175, metrics: 263 },
    { at_seconds: 9.5, interval_seconds: 5, spans: 104, logs: 208, metrics: 312 },
  ]);
  assert.equal(result.sweeps.mixed[0].median, 100);
  assert.equal(result.sweeps.mixed[0].min, 80);
  assert.equal(result.sweeps.mixed[0].max, 120);
  assert.equal(result.sweeps.mixed[0].fanout_cpu_median, 4);
  assert.equal(result.sweeps.mixed[0].generator_cpu_median, 2);
  assert.equal(result.all_gates_passed, true);
});

test("a phase without a full five-second sample window fails instead of inventing points", () => {
  const input = fixture();
  input.samples = input.samples.filter(sample => sample.at_seconds < 5.5);
  assert.throws(() => derive(input), /No complete 5-second sample windows/);
});

test("composition uses the same accepted-row window as the total", () => {
  const input = fixture();
  input.report.trials[0].elapsed_seconds = 9;
  const result = derive(input);
  assert.equal(result.sustained.spans_per_second, 20);
  assert.equal(result.sustained.log_records_per_second, 30);
  assert.equal(result.sustained.metric_points_per_second, 50);
});

test("missing counters, reset counters and unordered samples fail instead of inventing data", () => {
  let input = fixture();
  delete input.samples[1].ingest_rows_total.logs;
  assert.throws(() => derive(input), /ingest_rows_total.logs/);
  input = fixture(); input.samples[2].ingest_rows_total.spans = 0;
  assert.throws(() => derive(input), /Counter moved backwards/);
  input = fixture(); input.samples[2].at_seconds = input.samples[1].at_seconds;
  assert.throws(() => derive(input), /Non-increasing/);
});

test("partial runs require an explicit phase and preserve failed gates", () => {
  const input = fixture();
  input.report.trials.shift();
  input.report.summary.quality_gates[0].passed = false;
  input.report.summary.passed = false;
  input.samples.forEach(sample => sample.trial = "mixed-p1-r1");
  assert.throws(() => derive(input), /Required phase mixed-sustained/);
  const result = derive(input, "mixed-p1-r1");
  assert.equal(result.development_preview, true);
  assert.equal(result.all_gates_passed, false);
});

test("identity and repetition data are required", () => {
  let input = fixture(); delete input.report.host.memory_bytes;
  assert.throws(() => derive(input), /host.memory_bytes/);
  input = fixture(); input.report.trials.pop();
  assert.throws(() => derive(input), /Incomplete mixed/);
  input = fixture(); input.report.summary.quality_gates = [];
  assert.throws(() => derive(input), /quality_gates/);
});

test("a five-minute run retains every sample after the first full rate window for real-time replay", () => {
  const input = fixture();
  input.report.trials[0].elapsed_seconds = 303;
  input.samples = Array.from({ length: 304 }, (_, i) => ({
    trial: "mixed-sustained", at_seconds: i,
    ingest_rows_total: { spans: i * 20, logs: i * 30, metrics: i * 50 },
  }));
  const result = derive(input);
  assert.equal(result.series_seconds, 303);
  assert.equal(result.sustainedSeries.length, 299);
  assert.deepEqual(result.sustainedSeries[0], { at_seconds: 5, interval_seconds: 5, spans: 20, logs: 30, metrics: 50 });
  assert.deepEqual(result.sustainedSeries.at(-1), { at_seconds: 303, interval_seconds: 5, spans: 20, logs: 30, metrics: 50 });
  assert.equal(result.development_preview, false);
});

test("published replay data has finite rates, ordered times and real interval bounds", () => {
  assert.ok(benchmark.series_seconds > 0);
  assert.ok(sustainedSeries.length > 1);
  sustainedSeries.forEach((point, i) => {
    assert.ok(point.at_seconds > 0 && point.at_seconds <= benchmark.series_seconds);
    assert.ok(point.interval_seconds >= 5 && point.interval_seconds <= point.at_seconds);
    if (i) {
      assert.ok(point.at_seconds > sustainedSeries[i - 1].at_seconds);
    }
    for (const key of ["spans", "logs", "metrics"]) assert.ok(Number.isInteger(point[key]) && point[key] >= 0);
  });
  assert.equal(benchmark.development_preview, benchmark.series_phase !== "mixed-sustained");
});
