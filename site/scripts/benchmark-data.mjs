#!/usr/bin/env node
// node site/scripts/benchmark-data.mjs report.json samples.jsonl EVIDENCE_URL
// --phase mixed-p32-r1 explicitly enables a development preview of a partial run.
import { readFile, writeFile } from "node:fs/promises";
import { parseArgs } from "node:util";
import { pathToFileURL } from "node:url";

function required(value, path, type) {
  if (typeof value !== type || (type === "number" && (!Number.isFinite(value) || value < 0)) ||
      (type === "string" && !value.trim())) throw new Error(`Missing or invalid required field: ${path}`);
  return value;
}
const number = (value, path) => required(value, path, "number");
const string = (value, path) => required(value, path, "string");
const rounded = (value, digits = 0) => Number(value.toFixed(digits));
function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
}
const counts = ["spans", "log_records", "metric_points"];
const signals = ["traces", "logs", "metrics", "mixed"];
const RATE_WINDOW_SECONDS = 5;

export function deriveBenchmark(report, samples, evidence, phase = "mixed-sustained") {
  if (report.schema_version !== "fanout-bench/v1") throw new Error("Unsupported report schema_version");
  const url = new URL(evidence);
  if (!['https:', 'http:'].includes(url.protocol)) throw new Error("Evidence must be an HTTP(S) URL");
  const build = report.build ?? {}, host = report.host ?? {}, settings = report.settings ?? {};
  const trials = report.trials;
  if (!Array.isArray(trials) || !trials.length) throw new Error("Missing required field: trials");
  const selected = trials.find(t => t.settings?.name === phase);
  if (!selected) throw new Error(`Required phase ${phase} is absent; use --phase explicitly for a development preview`);
  if (selected.settings.signal !== "mixed") throw new Error(`Phase ${phase} must contain all three signals`);
  const sustainedTrial = trials.find(t => t.settings?.name === "mixed-sustained");
  const sustained = sustainedTrial ?? selected;
  const preview = phase !== "mixed-sustained";
  const repeat = number(settings.repeat, "settings.repeat");
  if (!Number.isInteger(repeat) || repeat < 1) throw new Error("settings.repeat must be a positive integer");
  const sweeps = Object.fromEntries(signals.map(signal => {
    const groups = new Map();
    for (const t of trials.filter(t => t.settings?.kind === "sweep" && t.settings.signal === signal)) {
      const processes = number(t.settings.processes_per_signal, `${t.settings.name}.processes_per_signal`);
      if (processes < 1) throw new Error("processes_per_signal must be positive");
      if (!groups.has(processes)) groups.set(processes, []);
      groups.get(processes).push(t);
    }
    if (!groups.size) throw new Error(`Missing required ${signal} sweeps`);
    return [signal, [...groups.entries()].sort(([a], [b]) => a - b).map(([processes, repetitions]) => {
      const rates = repetitions.map(t => number(t.accepted_items_per_second, `${t.settings.name}.accepted_items_per_second`));
      const fanout = repetitions.map(t => number(t.resources?.average_cpu_cores, `${t.settings.name}.resources.average_cpu_cores`));
      const generator = repetitions.map(t => number(t.generator_cpu?.average_cpu_cores, `${t.settings.name}.generator_cpu.average_cpu_cores`));
      if (!preview && repetitions.length !== repeat) throw new Error(`Incomplete ${signal} p${processes}: expected ${repeat} repetitions`);
      const ids = repetitions.map(t => number(t.settings.repetition, `${t.settings.name}.repetition`));
      if (new Set(ids).size !== ids.length) throw new Error(`Duplicate ${signal} p${processes} repetition`);
      return { processes_per_signal: processes, median: rounded(median(rates)), min: rounded(Math.min(...rates)),
        max: rounded(Math.max(...rates)), fanout_cpu_median: rounded(median(fanout), 2),
        generator_cpu_median: rounded(median(generator), 2), repetitions: repetitions.length };
    })];
  }));
  const seconds = number(sustained.elapsed_seconds, `${sustained.settings.name}.elapsed_seconds`);
  const rate = number(sustained.accepted_items_per_second, `${sustained.settings.name}.accepted_items_per_second`);
  const accepted = Object.fromEntries(counts.map(key => [key, number(sustained.accepted?.[key], `${sustained.settings.name}.accepted.${key}`)]));
  const total = Object.values(accepted).reduce((a, b) => a + b, 0);
  if (!seconds || !rate || !total) throw new Error("Measured phase must have positive duration and accepted throughput");
  // The report's rate uses its scrape-to-scrape window, which includes the final
  // scrape. elapsed_seconds is the generator window. Preserve the authoritative
  // denominator for the composition instead of inflating it using elapsed_seconds.
  const window = total / rate;
  const selectedSeconds = number(selected.elapsed_seconds, `${phase}.elapsed_seconds`);
  const phaseSamples = samples.filter(sample => sample.trial === phase);
  if (phaseSamples.length < 2) throw new Error(`Required samples missing for ${phase}`);
  for (const [i, sample] of phaseSamples.entries()) {
    const at = sample.at_seconds;
    if (typeof at !== "number" || !Number.isFinite(at)) throw new Error(`Missing or invalid required field: ${phase}.samples[${i}].at_seconds`);
    if (i && at <= phaseSamples[i - 1].at_seconds) throw new Error(`Non-increasing sample time in ${phase}`);
    for (const key of ["spans", "logs", "metrics"]) {
      const value = number(sample.ingest_rows_total?.[key], `${phase}.samples[${i}].ingest_rows_total.${key}`);
      if (i && value < phaseSamples[i - 1].ingest_rows_total[key]) throw new Error(`Counter moved backwards: ${phase}.${key}`);
    }
  }
  const sustainedSeries = [];
  // Sample timestamps are relative to this trial's started_at. No fabricated
  // baseline at t=0: wait for a full trailing window inside the phase.
  const windowSamples = phaseSamples.filter(sample => sample.at_seconds >= 0 && sample.at_seconds <= selectedSeconds);
  for (let i = 1, j = 0; i < windowSamples.length; i++) {
    const after = windowSamples[i];
    while (j + 1 < i && after.at_seconds - windowSamples[j + 1].at_seconds >= RATE_WINDOW_SECONDS) j++;
    const before = windowSamples[j];
    const delta = after.at_seconds - before.at_seconds;
    if (delta < RATE_WINDOW_SECONDS) continue;
    sustainedSeries.push({ at_seconds: after.at_seconds, interval_seconds: delta,
      ...Object.fromEntries(["spans", "logs", "metrics"].map(key => [key,
        Math.round((after.ingest_rows_total[key] - before.ingest_rows_total[key]) / delta)])) });
  }
  if (!sustainedSeries.length) throw new Error(`No complete ${RATE_WINDOW_SECONDS}-second sample windows for ${phase}`);
  const gates = report.summary?.quality_gates;
  if (!Array.isArray(gates) || !gates.length) throw new Error("Missing required summary.quality_gates");
  const quality_gates = gates.map((gate, i) => ({ name: string(gate.name, `quality_gates[${i}].name`),
    passed: required(gate.passed, `quality_gates[${i}].passed`, "boolean") }));
  const passed = required(report.summary.passed, "summary.passed", "boolean");
  if (passed !== quality_gates.every(gate => gate.passed)) throw new Error("summary.passed disagrees with quality_gates");
  const artifact = string(build.fanout_artifact, "build.fanout_artifact");
  const checksum = artifact.match(/#sha256=([a-f0-9]{64})$/)?.[1];
  if (!checksum) throw new Error("build.fanout_artifact requires a SHA-256 checksum");
  return {
    release: string(build.fanout_version, "build.fanout_version"), evidence: url.href, artifact, artifact_sha256: checksum,
    harness_commit: string(build.harness_commit, "build.harness_commit"),
    telemetrygen: string(build.telemetrygen_version, "build.telemetrygen_version"),
    host: { description: string(host.description, "host.description"),
      generator_description: string(host.generator_description, "host.generator_description"),
      cpu_model: string(host.cpu_model, "host.cpu_model"), logical_cpus: number(host.logical_cpus, "host.logical_cpus"),
      memory_bytes: number(host.memory_bytes, "host.memory_bytes") },
    repeat, batch_size: number(settings.batch_size, "settings.batch_size"),
    accepted_total: Object.fromEntries(counts.map(key => [key, number(report.summary?.accepted_total?.[key], `summary.accepted_total.${key}`)])),
    sustained: { phase: sustained.settings.name, processes_per_signal: number(sustained.settings.processes_per_signal, "sustained.processes_per_signal"),
      seconds: rounded(seconds, 1), accepted_per_second: rounded(rate),
      spans_per_second: rounded(accepted.spans / window), log_records_per_second: rounded(accepted.log_records / window),
      metric_points_per_second: rounded(accepted.metric_points / window),
      fanout_cpu: rounded(number(sustained.resources?.average_cpu_cores, "sustained.resources.average_cpu_cores"), 2),
      peak_rss_gib: rounded(number(sustained.resources?.peak_rss_bytes, "sustained.resources.peak_rss_bytes") / 2 ** 30, 1),
      generator_cpu: rounded(number(sustained.generator_cpu?.average_cpu_cores, "sustained.generator_cpu.average_cpu_cores"), 2),
      classification: string(sustained.classification, "sustained.classification") },
    sweeps, series_phase: phase, series_seconds: selectedSeconds, development_preview: preview,
    quality_gates, all_gates_passed: quality_gates.every(gate => gate.passed), sustainedSeries,
  };
}

function moduleSource(data) {
  return `// Generated by site/scripts/benchmark-data.mjs. Do not edit numbers by hand.
// --phase selects development series explicitly; development_preview must stay visible.
export const benchmark = ${JSON.stringify(data, null, 2)};

export const sustainedSeries = benchmark.sustainedSeries;
export type Signal = keyof typeof benchmark.sweeps;
export const signalNames: Record<Signal, string> = {
  traces: "Traces", logs: "Logs", metrics: "Metrics", mixed: "Mixed",
};
export const formatRate = (value: number) => Math.round(value).toLocaleString("en-US");
export const peak = (signal: Signal) =>
  benchmark.sweeps[signal].reduce((best, point) => point.median > best.median ? point : best);
export const acceptedTotal = Object.values(benchmark.accepted_total).reduce((sum, value) => sum + value, 0);
export const composition = [
  { label: "Spans", rate: benchmark.sustained.spans_per_second },
  { label: "Log records", rate: benchmark.sustained.log_records_per_second },
  { label: "Metric points", rate: benchmark.sustained.metric_points_per_second },
];
`;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const { values, positionals } = parseArgs({ allowPositionals: true, options: {
      phase: { type: "string", default: "mixed-sustained" }, output: { type: "string" },
    } });
    if (positionals.length !== 3) throw new Error("Usage: node site/scripts/benchmark-data.mjs REPORT SAMPLES EVIDENCE_URL [--phase mixed-p32-r1] [--output FILE]");
    const [reportFile, samplesFile, evidence] = positionals;
    const report = JSON.parse(await readFile(reportFile, "utf8"));
    const samples = (await readFile(samplesFile, "utf8")).trim().split(/\r?\n/).map((line, i) => {
      try { return JSON.parse(line); } catch { throw new Error(`Invalid samples JSON at line ${i + 1}`); }
    });
    const data = deriveBenchmark(report, samples, evidence, values.phase);
    await writeFile(values.output ?? new URL("../src/data/benchmark.ts", import.meta.url), moduleSource(data));
    console.log(`benchmark-data: ${data.release} · ${data.series_phase}${data.development_preview ? " · DEVELOPMENT PREVIEW (not a sustained result)" : ""}`);
    console.log(`benchmark-data: ${data.sustainedSeries.length} intervals · ${data.sustained.seconds}s · ${data.sustained.accepted_per_second.toLocaleString("en-US")} items/s`);
    console.log(`benchmark-data: suite total ${Object.values(data.accepted_total).reduce((a, b) => a + b, 0).toLocaleString("en-US")} · gates ${data.all_gates_passed ? "PASS" : "FAIL"}`);
    console.log(`benchmark-data: wrote ${values.output ?? "site/src/data/benchmark.ts"}`);
  } catch (error) {
    console.error(`benchmark-data: ${error.message}`);
    process.exitCode = 1;
  }
}
