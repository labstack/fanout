export type BrowserEvidence = {
  head: string;
  model: string;
  source: string;
  replay: { spans: number; logs: number; metrics: number; shift_ns: string };
  panels: {
    theme: "light" | "dark";
    viz: string;
    visible: boolean;
    aria: boolean;
    inspect: boolean;
    console_errors: number;
  }[];
  refreshes: {
    theme: "light" | "dark";
    panels: number;
    panel_calls: number;
    annotation_calls: number;
    cells: number;
    elapsed_ms: number;
  }[];
  interactions: { name: string; passed: boolean; evidence: string }[];
  parity: {
    capability: string;
    preview_evidence: string;
    m2_evidence: string;
    gap: string;
  }[];
  screenshots: string[];
  defects: string[];
  evaluations: {
    set: "benchmark" | "holdout";
    baseline_s1: number;
    candidate_s1: number;
    baseline_intent: number;
    candidate_intent: number;
    errors: number;
    unchecked: number;
  }[];
};
const types = [
  "stat",
  "gauge",
  "timeseries",
  "bar",
  "table",
  "text",
  "heatmap",
  "histogram",
  "scatter",
  "state_timeline",
  "logs",
  "log_patterns",
  "traces",
  "service_map",
  "health",
];
const checks = [
  "loading",
  "empty",
  "error",
  "partial",
  "inspect",
  "filter_chip",
  "linked_crosshair",
  "brush_history",
  "panel_time",
  "drill_url",
  "waterfall_logs",
  "cancel_drill",
  "deploy_markers",
  "anomaly_areas",
  "deploy_split",
  "column_formats",
  "stat_sparkline",
];
export function checkBrowserEvidence(e: BrowserEvidence): string[] {
  const errors: string[] = [];
  if (!e.head || !e.source || e.model !== "claude-sonnet-5-5")
    errors.push("missing build/source or incorrect model");
  if (
    !/^[-]?\d+$/.test(e.replay.shift_ns) ||
    [e.replay.spans, e.replay.logs, e.replay.metrics].some(
      (n) => !Number.isFinite(n) || n <= 0,
    )
  )
    errors.push(
      "replay must contain all three signals and a signed nanosecond shift",
    );
  for (const theme of ["light", "dark"] as const)
    for (const viz of types) {
      const row = e.panels.find((p) => p.theme === theme && p.viz === viz);
      if (
        !row ||
        !row.visible ||
        (viz !== "text" && (!row.aria || !row.inspect)) ||
        row.console_errors !== 0
      )
        errors.push(
          `${theme}/${viz}: incomplete rendering/aria/Inspect evidence`,
        );
    }
  for (const theme of ["light", "dark"] as const) {
    if (!e.refreshes.some((r) => r.theme === theme && r.panels >= 20))
      errors.push(`${theme}: no 20-panel refresh`);
  }
  for (const r of e.refreshes) {
    if (r.panel_calls !== 1 || r.annotation_calls !== 1)
      errors.push(
        `S8 ${r.theme}: expected one panel call and one annotations call`,
      );
    if (!Number.isFinite(r.cells) || r.cells < 0 || r.cells > 200000)
      errors.push("frame exceeds 200000 cells");
    if (!Number.isFinite(r.elapsed_ms) || r.elapsed_ms < 0)
      errors.push("missing refresh timing evidence");
  }
  for (const name of checks) {
    const row = e.interactions.find((i) => i.name === name);
    if (!row?.passed || !row.evidence.trim())
      errors.push(`${name}: missing or failed interaction evidence`);
  }
  for (const set of ["benchmark", "holdout"] as const) {
    const row = e.evaluations.find((i) => i.set === set);
    if (!row) {
      errors.push(`${set}: missing evaluation`);
      continue;
    }
    for (const n of [
      row.baseline_s1,
      row.candidate_s1,
      row.baseline_intent,
      row.candidate_intent,
    ])
      if (!Number.isFinite(n) || n < 0 || n > 1)
        errors.push(`${set}: unknown rate`);
    if (row.candidate_s1 < row.baseline_s1) errors.push(`${set} S1 regressed`);
    if (row.candidate_intent < row.baseline_intent)
      errors.push(`${set} intent regressed`);
    if (row.errors || row.unchecked)
      errors.push(`${set}: incomplete execution/validation evidence`);
  }
  for (const capability of [
    "heatmap",
    "log_patterns",
    "deploy_markers",
    "drill_drawer",
    "split_bars",
    "table_formats",
  ]) {
    const row = e.parity.find((p) => p.capability === capability);
    if (
      !row?.preview_evidence.trim() ||
      !row.m2_evidence.trim() ||
      !row.gap.trim()
    )
      errors.push(`S13 ${capability}: missing comparison or gap record`);
    else if (row.gap !== "none") errors.push(`S13 ${capability}: ${row.gap}`);
  }
  if (e.defects.length) errors.push(`${e.defects.length} unresolved defects`);
  if (e.screenshots.length < 2) errors.push("missing screenshots");
  return errors;
}
function cell(s: unknown): string {
  return String(s).replaceAll("|", "\\|").replaceAll("\n", " ");
}
export function browserReport(e: BrowserEvidence): string {
  const errors = checkBrowserEvidence(e);
  return [
    "# Agent dashboards Milestone 2 verification",
    "",
    `Build: ${cell(e.head)}. Model: ${cell(e.model)}. Source: ${cell(e.source)}.`,
    `Status: ${errors.length ? "BLOCKED" : "PASS"}.`,
    "",
    `Replay: ${e.replay.spans} spans, ${e.replay.logs} logs, ${e.replay.metrics} metric points; shift_ns=${e.replay.shift_ns}.`,
    "",
    "## Browser type coverage",
    "",
    "| Theme | Type | Visible | Aria | Inspect | Console errors |",
    "|---|---|---|---|---|---|",
    ...e.panels.map(
      (p) =>
        `| ${p.theme} | ${cell(p.viz)} | ${p.visible} | ${p.aria} | ${p.inspect} | ${p.console_errors} |`,
    ),
    "",
    "## S8 and bounded refresh",
    "",
    "| Theme | Panels | Panel requests | Annotation requests | Cells | ms |",
    "|---|---:|---:|---:|---:|---:|",
    ...e.refreshes.map(
      (r) =>
        `| ${r.theme} | ${r.panels} | ${r.panel_calls} | ${r.annotation_calls} | ${r.cells} | ${r.elapsed_ms} |`,
    ),
    "",
    "## Interaction evidence",
    "",
    ...e.interactions.map(
      (i) =>
        `- ${cell(i.name)}: ${i.passed ? "PASS" : "FAIL"}; ${cell(i.evidence)}`,
    ),
    "",
    "## Agent evaluation",
    "",
    "Holdout is never used for tuning. Matching repeats use the same frozen telemetry and account isolation.",
    "",
    "| Set | Baseline S1 | Candidate S1 | Baseline intent | Candidate intent | Errors | Unchecked |",
    "|---|---:|---:|---:|---:|---:|---:|",
    ...e.evaluations.map(
      (r) =>
        `| ${r.set} | ${r.baseline_s1} | ${r.candidate_s1} | ${r.baseline_intent} | ${r.candidate_intent} | ${r.errors} | ${r.unchecked} |`,
    ),
    "",
    "## S13 M2 capability preview comparison",
    "",
    "| Capability | Preview evidence | M2 evidence | Gap |",
    "|---|---|---|---|",
    ...e.parity.map(
      (p) =>
        `| ${cell(p.capability)} | ${cell(p.preview_evidence)} | ${cell(p.m2_evidence)} | ${cell(p.gap)} |`,
    ),
    "",
    "## Screenshots",
    "",
    ...e.screenshots.map((s) => `- ${cell(s)}`),
    "",
    "## Remaining defects and gates",
    "",
    ...(errors.length
      ? errors.map((s) => `- ${cell(s)}`)
      : ["All gates passed against measured evidence."]),
    "",
  ].join("\n");
}
