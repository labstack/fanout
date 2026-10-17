// Authored benchmark claims must agree with the generated data, including in
// Markdown/llms.txt. Match claims by meaning, never by an allowed-number list.
import { readFile } from "node:fs/promises";
import { benchmark, acceptedTotal, peak } from "../src/data/benchmark.ts";

const files = ["../src/content/docs/index.mdx", "../src/content/docs/explanation/performance.mdx", "../../README.md"];
const failures = [];
const digits = "([0-9][0-9,]*(?:\\.[0-9]+)?)";
const amount = value => Number(value.replaceAll(",", ""));
function equal(file, claim, actual, expected) {
  if (actual !== expected) failures.push(`${file}: ${claim} is ${actual.toLocaleString("en-US")}; data requires ${expected.toLocaleString("en-US")}`);
}

for (const relative of files) {
  const file = new URL(relative, import.meta.url);
  const source = await readFile(file, "utf8");
  const text = source.replaceAll("**", "").replace(/\s+/g, " ");
  let rates = 0;
  for (const match of text.matchAll(new RegExp(`${digits} (?:mixed (?:OpenTelemetry )?)?items/s`, "gi"))) {
    equal(relative, "sustained/preview items/s", amount(match[1]), benchmark.sustained.accepted_per_second);
    rates++;
  }
  // The landing replay renders its headline directly from the benchmark data.
  const dataHeadline = relative === "../src/content/docs/index.mdx" && /^<ThroughputReplay \/>$/m.test(source);
  if (!rates && !dataHeadline) failures.push(`${relative}: missing authored headline items/s`);
  const perSignal = [
    ["spans", benchmark.sustained.spans_per_second],
    ["log records", benchmark.sustained.log_records_per_second],
    ["metric points", benchmark.sustained.metric_points_per_second],
  ];
  for (const [label, rate] of perSignal) {
    // The composition sentence may share its final "per second" across all
    // three signals; suite counts have their own explicitly marked sentence.
    const composition = text.match(/(?:composition (?:was|is)|rates? (?:were|are)) (.+?per second)/i)?.[1];
    if (composition) for (const match of composition.matchAll(new RegExp(`${digits} ${label}\\b`, "gi"))) {
      equal(relative, `${label}/s`, amount(match[1]), rate);
    }
    for (const match of text.matchAll(new RegExp(`${digits} ${label} (?:per second|/s)`, "gi"))) {
      equal(relative, `${label}/s`, amount(match[1]), rate);
    }
  }
  for (const match of text.matchAll(new RegExp(`${digits} million items`, "gi"))) {
    equal(relative, "suite total (million)", amount(match[1]), Number((acceptedTotal / 1e6).toFixed(1)));
  }
  for (const match of text.matchAll(new RegExp(`suite (?:accepted|total(?: is)?) ${digits} items`, "gi"))) {
    equal(relative, "suite total", amount(match[1]), acceptedTotal);
  }
  // Tables are scoped by their header so a mixed-sweep row cannot masquerade
  // as a median peak or the mixed sustained total.
  let table = "";
  for (const line of source.split("\n")) {
    if (!line.trim().startsWith("|")) { table = ""; continue; }
    const cells = line.split("|").slice(1, -1).map(cell => cell.trim().replaceAll("**", ""));
    if (cells[0] === "Signal") { table = "sustained"; continue; }
    if (cells[0] === "Workload") { table = "peaks"; continue; }
    if (table === "sustained") {
      const rate = { Spans: benchmark.sustained.spans_per_second, "Log records": benchmark.sustained.log_records_per_second,
        "Metric points": benchmark.sustained.metric_points_per_second, "Mixed total": benchmark.sustained.accepted_per_second }[cells[0]];
      if (rate !== undefined) equal(relative, cells[0], amount(cells[1]), rate);
    }
    if (table === "peaks") {
      const signal = { Mixed: "mixed", Traces: "traces", Logs: "logs", Metrics: "metrics" }[cells[0]];
      if (signal) equal(relative, `${cells[0]} median peak`, amount(cells[1]), peak(signal).median);
    }
  }
}
if (failures.length) {
  console.error("check-benchmark-numbers:\n" + failures.map(failure => `  ${failure}`).join("\n"));
  process.exitCode = 1;
} else console.log("check-benchmark-numbers: authored throughput, composition, median peaks and suite totals match generated data");
