import { describe, expect, it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { withAnnotations } from "../../../panels/annotations";
import { barOption, chartThemeFor, timeseriesOption } from "../../../panels/compile";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import { performanceAxisTooltip, performanceHeatTooltip, topologyMatrixTooltip } from "../../../panels/tooltips";

const attack = '<img src=x onerror=alert(1)>';
const theme = chartThemeFor(false);
const frame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: attack, type: "number", role: "measure" }], values: [[1000], [attack], [3]], rows: 1 };
const result: PanelResult = { id: "x", status: "ok", elapsed_ms: 1, frame };
type Tooltip = { formatter(p: unknown): string };
const safe = (text: string) => { expect(text).toContain("&lt;img"); expect(text).not.toContain("<img"); };

describe("telemetry tooltip HTML security", () => {
  it("preserves time/value and mixed-unit scatter tooltip semantics at the escaping boundary", () => {
    const timeFrame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "p95", type: "number", role: "measure", unit: "ms" }], values: [[1000], [1500]], rows: 1 };
    const time = timeseriesOption({ id: "x", title: "x", viz: "timeseries" }, { ...result, frame: timeFrame }, theme);
    const text = (time.tooltip as Tooltip).formatter({ name: "Jan 1", seriesName: "p95", value: [1000,1500] });
    expect(text).toContain("1.50s"); expect(text).not.toContain("1.00s");
    const scatterFrame: Frame = { columns: [{ name: "item", role: "dimension", type: "string" }, { name: "calls", role: "measure", type: "number", unit: "count" }, { name: "latency", role: "measure", type: "number", unit: "ms" }], values: [[attack],[12],[1500]], rows: 1 };
    const scatter = analysisOption({ id: "x", title: "x", viz: "scatter" }, { ...result, frame: scatterFrame }, theme);
    const point = (scatter.tooltip as Tooltip).formatter({ name: attack, seriesName: "Items", value: [12,1500] });
    safe(point); expect(point).toContain("calls: 12"); expect(point).toContain("latency: 1.50s");
  });
  it("escapes every app HTML formatter including service and dimension values", () => {
    safe(performanceAxisTooltip([{ seriesName: attack, value: 1, axisValueLabel: attack }], () => attack));
    safe(performanceHeatTooltip(attack, attack));
    safe(topologyMatrixTooltip(attack, attack, attack, attack, attack));
  });
  it.each(["timeseries", "bar", "scatter", "heatmap", "state_timeline"] as const)("escapes %s names and categories in tooltips", viz => {
    const panel: Panel = { id: "x", title: attack, viz };
    const option = viz === "timeseries" ? timeseriesOption(panel, result, theme) : viz === "bar" ? barOption(panel, frame, theme) : analysisOption(panel, result, theme);
    safe((option.tooltip as Tooltip).formatter({ name: attack, seriesName: attack, axisValueLabel: attack, value: [1000, 0, 3] }));
    if (viz === "timeseries" || viz === "bar" || viz === "scatter") safe((option.legend as { tooltip: Tooltip }).tooltip.formatter({ name: attack }));
    if (viz === "state_timeline") {
      const data = (option.series as { data: { tooltip: Tooltip }[] }[])[0].data;
      safe(data[0].tooltip.formatter({}));
    }
  });
  it("escapes annotation details (service map uses React text/title boundaries)", () => {
    const marked = withAnnotations({ series: [{}] }, { id: "x", title: "x", viz: "timeseries" }, result, {
      deploys: [{ namespace: attack, service: attack, version: attack, at: new Date(1000).toISOString() }],
      anomalies: [{ namespace: attack, service: attack, kind: attack, title: attack, severity: attack, from: new Date(1000).toISOString(), to: new Date(2000).toISOString() }],
    }, {}, theme) as { series: { markLine: { data: { tooltip: Tooltip }[] }; markArea: { data: { tooltip: Tooltip }[][] } }[] };
    safe(marked.series[0].markLine.data[0].tooltip.formatter({}));
    safe(marked.series[0].markArea.data[0][0].tooltip.formatter({}));
  });
});
