import { describe, expect, it } from "vitest";
import { timeseriesOption, chartThemeFor } from "../../../panels/compile";
import { withAnnotations } from "../../../panels/annotations";
import type { Panel, PanelResult } from "../../../panels/types";

const panel: Panel = { id: "x", title: "Latency", viz: "timeseries", unit: "ms", thresholds: [{ value: 1500, status: "bad", label: "p99 budget" }] };
const result: PanelResult = { id: "x", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 3600000, frame: { columns: [{ name: "time", type: "time", role: "time" }, ...["p50", "p95", "p99"].map(name => ({ name, type: "number" as const, role: "measure" as const, unit: "ms" }))], values: [[0, 3599999], [500, 500], [700, 700], [701, 701]], rows: 2 } };
type Series = { endLabel?: { show: boolean; formatter: string; color: string; fontSize: number }; labelLayout: { moveOverlap: string }; lineStyle: { width: number }; showSymbol: boolean; markLine: { data: { xAxis?: number; label: { show: boolean; formatter: string; rotate?: number; position: string; textBorderColor?: string; fontSize: number } }[] }; markArea: { data: { label: { show: boolean; formatter: string; position: string }; tooltip: { formatter(): string }; itemStyle: { opacity: number } }[][] } };
describe("timeseries end labels", () => {
  it.each([false, true])("uses collision-managed direct end labels and formatted threshold labels (%s)", dark => {
    const theme = chartThemeFor(dark); const option = timeseriesOption(panel, result, theme);
    expect((option.grid as { right: number }).right).toBeGreaterThanOrEqual(48);
    for (const series of option.series as Series[]) {
      expect(series.lineStyle.width).toBe(2); expect(series.showSymbol).toBe(false);
      expect(series.endLabel).toMatchObject({ show: true, color: theme.muted, fontSize: 12, formatter: "{a}" });
      expect(series.labelLayout).toEqual({ moveOverlap: "shiftY" });
    }
    expect((option.series as Series[])[0].markLine.data[0].label).toMatchObject({ formatter: "p99 budget 1.5 s", position: "insideStartTop", fontSize: 12 });
  });
  it("omits end labels for more than six source series", () => {
    const columns = Array.from({ length: 7 }, (_, i) => ({ name: `s${i}`, type: "number" as const, role: "measure" as const }));
    const o = timeseriesOption(panel, { ...result, frame: { columns: [result.frame!.columns[0], ...columns], values: [[0], ...columns.map(() => [1])], rows: 1 } }, chartThemeFor(false));
    expect((o.series as Series[]).every(s => !s.endLabel?.show)).toBe(true);
  });
  it.each([false, true])("merges deploys within 12px and places deploy/anomaly labels in the top lane (%s)", dark => {
    const theme = chartThemeFor(dark), base = timeseriesOption(panel, result, theme);
    const got = withAnnotations(base, panel, result, { deploys: [600000, 610000, 1800000].map(at => ({ namespace: "shop", service: "payments", version: "v2.14.0", at: new Date(at).toISOString() })), anomalies: [{ namespace: "shop", service: "payments", title: "Slow", severity: "bad", kind: "latency", from: new Date(800000).toISOString(), to: new Date(2000000).toISOString() }] }, {}, theme, { width: 500, height: 248 });
    expect((got.grid as {top:number}).top-(base.grid as {top:number}).top).toBeLessThanOrEqual(18);
    const first = (got.series as Series[])[0]; const deploys = first.markLine.data.filter(mark => mark.xAxis !== undefined);
    expect(deploys).toHaveLength(2);
    expect((got.graphic as {annotation?:boolean;style:{text:string}}[]).filter(g=>g.annotation).map(g=>g.style.text)).toEqual(["2 deploys","payments v2.14.0","anomaly"]);
    for (const deploy of deploys) expect(deploy.label).toEqual({show:false});
    const area = first.markArea.data[0][0]; expect(area.label).toEqual({show:false});
    expect(area.tooltip.formatter()).toContain("Slow · bad"); expect(area.tooltip.formatter()).toContain(new Date(800000).toISOString()); expect(area.tooltip.formatter()).toContain(new Date(2000000).toISOString());
    expect(area.itemStyle.opacity).toBeLessThanOrEqual(.12);
  });
});
