import { describe, expect, it } from "vitest";
import {
  browserReport,
  checkBrowserEvidence,
  type BrowserEvidence,
} from "../../tests/browser-evidence";

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
function fixture(): BrowserEvidence {
  return {
    head: "fixture",
    model: "claude-sonnet-5-5",
    source: "test fixture only",
    replay: { spans: 60, logs: 10, metrics: 10, shift_ns: "1000000000" },
    panels: ["light", "dark"].flatMap((theme) =>
      types.map((viz) => ({
        theme: theme as "light" | "dark",
        viz,
        visible: true,
        aria: true,
        inspect: true,
        console_errors: 0,
      })),
    ),
    refreshes: [
      {
        theme: "light",
        panels: 20,
        panel_calls: 1,
        annotation_calls: 1,
        cells: 180000,
        elapsed_ms: 1000,
      },
      {
        theme: "dark",
        panels: 20,
        panel_calls: 1,
        annotation_calls: 1,
        cells: 180000,
        elapsed_ms: 1000,
      },
    ],
    interactions: checks.map((name) => ({
      name,
      passed: true,
      evidence: "isolated test fixture",
    })),
    parity: [
      "heatmap",
      "log_patterns",
      "deploy_markers",
      "drill_drawer",
      "split_bars",
      "table_formats",
    ].map((capability) => ({
      capability,
      preview_evidence: "isolated preview fixture",
      m2_evidence: "isolated M2 fixture",
      gap: "none",
    })),
    screenshots: ["light.png", "dark.png"],
    defects: [],
    evaluations: ["benchmark", "holdout"].map((set) => ({
      set: set as "benchmark" | "holdout",
      baseline_s1: 1,
      candidate_s1: 1,
      baseline_intent: 1,
      candidate_intent: 1,
      errors: 0,
      unchecked: 0,
    })),
  };
}
describe("acceptance evidence", () => {
  it("accepts complete evidence and renders measured counts", () => {
    const e = fixture();
    expect(checkBrowserEvidence(e)).toEqual([]);
    const report = browserReport(e);
    expect(report).toContain("60 spans");
    expect(report).toContain("S8");
    expect(report).toContain("holdout");
  });
  it("rejects annotation fanout, missing themes, oversized frames and regressed intent", () => {
    const e = fixture();
    e.refreshes[0].annotation_calls = 20;
    e.refreshes[0].cells = 200001;
    e.panels = e.panels.filter(
      (p) => !(p.theme === "dark" && p.viz === "health"),
    );
    e.evaluations[1].candidate_intent = 0.9;
    const errors = checkBrowserEvidence(e).join(" ");
    expect(errors).toContain("S8");
    expect(errors).toContain("200000");
    expect(errors).toContain("dark/health");
    expect(errors).toContain("holdout intent");
  });
  it("requires S13 comparisons and records capability gaps", () => {
    const e = fixture();
    e.parity[0].gap = "heatmap lacks numeric bucket ordering";
    expect(checkBrowserEvidence(e).join(" ")).toContain("S13 heatmap");
    expect(browserReport(e)).toContain("heatmap lacks numeric bucket ordering");
    e.parity = [];
    expect(checkBrowserEvidence(e).join(" ")).toContain("missing comparison");
  });
  it("rejects silent empty/error/loading omissions and unresolved defects", () => {
    const e = fixture();
    e.interactions = e.interactions.filter((x) => x.name !== "empty");
    e.defects = ["heatmap did not clear an empty refresh"];
    expect(checkBrowserEvidence(e).join(" ")).toContain("empty");
    expect(checkBrowserEvidence(e).join(" ")).toContain("unresolved");
  });
});
