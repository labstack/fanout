import { describe, expect, it } from "vitest";
import { withAnnotations, type AnnotationsResponse } from "../../../panels/annotations";
import { chartThemeFor } from "../../../panels/compile";
import type { Panel, PanelResult } from "../../../panels/types";
const panel: Panel = { id: "p", title: "P", viz: "timeseries", query: { from: "spans", where: ["service = $service"], measures: ["count()"] } };
const result: PanelResult = { id: "p", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 10000, annotation_scope: { services: [{ namespace: "shop", service: "checkout" }] } };
describe("annotations and formats", () => {
  it("draws matching deploy lines and anomaly bands, retaining thresholds", () => {
    const option = { series: [{ type: "line", markLine: { data: [{ yAxis: 3 }] } }] };
    const annotations = { deploys: [{ namespace: "shop", service: "checkout", version: "v2", at: new Date(1000).toISOString() }, { namespace: "shop", service: "frontend", version: "v3", at: new Date(2000).toISOString() }], anomalies: [{ namespace: "shop", service: "checkout", kind: "latency", from: new Date(3000).toISOString(), to: new Date(5000).toISOString(), title: "Slow", severity: "bad" }] };
    const got = withAnnotations(option, panel, result, annotations, { service: "checkout" }, chartThemeFor(false));
    const series = (got.series as { markLine: { data: unknown[] }; markArea: { data: unknown[] } }[])[0]; expect(series.markLine.data).toHaveLength(2); expect(series.markArea.data).toHaveLength(1);
  });
});

const theme = chartThemeFor(false);
const at = (ms: number) => new Date(ms).toISOString();
const history: AnnotationsResponse = {
  deploys: [
    { namespace: "shop", service: "checkout", version: "v2", at: at(1000) },
    { namespace: "shop", service: "frontend", version: "v3", at: at(2000) },
    { namespace: "ops", service: "checkout", version: "v4", at: at(3000) },
    { namespace: "", service: "checkout", version: "v5", at: at(4000) },
  ],
  anomalies: [{ namespace: "shop", service: "checkout", kind: "latency", from: at(-1000), to: at(11000), title: "Slow", severity: "bad" }],
};
type Mark = { xAxis: number; lineStyle: { type: string }; tooltip: { formatter(): string } };
type Area = [{ xAxis: number; itemStyle: { color: string; opacity: number }; tooltip: { formatter(): string } }, { xAxis: number }];
function markers(data: PanelResult = result, viz: Panel["viz"] = "timeseries", annotations = history) {
  const got = withAnnotations({ series: [{ type: "line" }], tooltip: { trigger: "axis" } }, { ...panel, viz }, data, annotations, { service: "unrelated" }, theme);
  return got as { tooltip: { renderMode: string }; series: { markLine: { silent: boolean; data: Mark[] }; markArea: { silent: boolean; data: Area[] } }[] };
}

describe("annotation scope and windows", () => {
  it("P3c/P3d reserve the top lane without mutating the base", () => {
    const option = {grid:{top:12,left:8,right:16,bottom:8},series:[{type:"line"}]};
    const got = withAnnotations(option,panel,result,history,{},theme) as {grid:{top:number};toolbox?:unknown;series:{markLine:{data:{label:{position:string;distance:number;rotate:number;verticalAlign:string}}[]}}[]};
    expect(got.toolbox).toBeUndefined();
    expect(got.grid.top).toBe(30);
    expect(got.series[0].markLine.data[0].label).toMatchObject({position:"end",distance:0,rotate:0,textBorderColor:theme.surface,backgroundColor:theme.surface});
    expect(option.grid.top).toBe(12);
  });
  it("preserves tooltip mode when no annotation markers match", () => {
    const option = { series: [{ type: "line", markLine: { data: [{ yAxis: 3 }] } }], tooltip: { trigger: "axis", renderMode: "html" } };
    const cases: [PanelResult, AnnotationsResponse][] = [
      [result, { deploys: [], anomalies: [] }],
      [{ ...result, annotation_scope: { services: [] } }, history],
      [{ ...result, from_ms: 20000, to_ms: 30000 }, history],
    ];
    for (const [data, annotations] of cases) {
      expect(withAnnotations(option, panel, data, annotations, {}, theme).tooltip).toEqual(option.tooltip);
    }
    expect(markers(result, "timeseries", { deploys: [], anomalies: [] }).tooltip).not.toHaveProperty("renderMode");
  });
  it.each(["deploys", "anomalies"] as const)("uses escaped HTML when only %s markers are drawn", kind => {
    expect(markers(result, "timeseries", { deploys: kind === "deploys" ? history.deploys : [], anomalies: kind === "anomalies" ? history.anomalies : [] }).tooltip.renderMode).toBe("html");
  });
  it.each(["timeseries", "heatmap", "state_timeline"] as const)("draws all in-window records with nil scope on %s", viz => {
    const got = markers({ ...result, annotation_scope: undefined }, viz);
    expect(got.series[0].markLine.data).toHaveLength(4);
    expect(got.series[0].markArea.data).toHaveLength(1);
  });
  it("uses the checked scope rather than a dashboard service variable", () => {
    const got = markers();
    expect(got.series[0].markLine.data.map(mark => mark.xAxis)).toEqual([1000, 4000]);
    expect(got.series[0].markLine.data[0].lineStyle.type).toBe("dashed");
    expect(got.series[0].markLine.data[0].tooltip.formatter()).toBe(`checkout · v2 · ${at(1000)}`);
    const band = got.series[0].markArea.data[0];
    expect(band[0].xAxis).toBe(0); expect(band[1].xAxis).toBe(10000);
    expect(band[0].itemStyle).toEqual({ color: theme.status.warn, opacity: .09 });
    expect(band[0].tooltip.formatter()).toBe(`checkout · Slow · bad · ${at(-1000)} – ${at(11000)}`);
    expect(got.tooltip.renderMode).toBe("html");
    expect(got.series[0].markLine.silent).toBe(false); expect(got.series[0].markArea.silent).toBe(false);
  });
  it("enforces namespaces and keeps an empty checked scope empty", () => {
    expect(markers({ ...result, annotation_scope: { ...result.annotation_scope!, namespace_scoped: true } }).series[0].markLine.data.map(mark => mark.xAxis)).toEqual([1000]);
    const empty = markers({ ...result, annotation_scope: { services: [] } });
    expect(empty.series[0].markLine.data).toHaveLength(0); expect(empty.series[0].markArea.data).toHaveLength(0);
  });
  it("uses half-open windows for deploys and overlapping anomaly bands", () => {
    const got = markers({ ...result, from_ms: 1000, to_ms: 4000 });
    expect(got.series[0].markLine.data.map(mark => mark.xAxis)).toEqual([1000]);
    expect(got.series[0].markArea.data[0].map(bound => bound.xAxis)).toEqual([1000, 4000]);
    const touching = { deploys: [], anomalies: [
      { ...history.anomalies[0], from: at(-1000), to: at(0) },
      { ...history.anomalies[0], from: at(10000), to: at(11000) },
    ] };
    expect(markers(result, "timeseries", touching).series[0].markArea.data).toHaveLength(0);
    expect(markers(result, "timeseries", { deploys: [], anomalies: [{ ...history.anomalies[0], severity: "warn" }] }).series[0].markArea.data[0][0].itemStyle.color).toBe(theme.status.warn);
  });
  it("leaves non-time, scope-error, and series-free options unchanged", () => {
    const option = { series: [{ type: "bar" }] };
    for (const viz of ["bar", "scatter", "histogram", "stat"] as const) expect(withAnnotations(option, { ...panel, viz }, result, history, {}, theme)).toBe(option);
    expect(withAnnotations(option, panel, { ...result, annotation_error: "Scope unavailable" }, history, {}, theme)).toBe(option);
    const empty = { series: [] }; expect(withAnnotations(empty, panel, result, history, {}, theme)).toBe(empty);
  });
  it("retains existing markers and other series without mutating compiled options", () => {
    const option = { series: [{ type: "line", markLine: { data: [{ yAxis: 3 }], label: { show: true } }, markArea: { data: [[{ yAxis: 2 }, { yAxis: 3 }]] } }, { type: "line", name: "previous" }] };
    const got = withAnnotations(option, panel, result, history, {}, theme);
    const first = (got.series as { markLine: { data: unknown[]; label: unknown }; markArea: { data: unknown[] } }[])[0];
    expect(first.markLine.data[0]).toEqual({ yAxis: 3 }); expect(first.markLine.label).toEqual({ show: true });
    expect(first.markArea.data).toHaveLength(2); expect((got.series as unknown[])[1]).toBe(option.series[1]);
    expect(option.series[0].markLine?.data).toHaveLength(1); expect(option.series[0].markArea?.data).toHaveLength(1);
  });
});
